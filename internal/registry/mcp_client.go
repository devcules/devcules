package registry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// mcp_client.go — ONE MCP client for the whole control-plane. mcp_inspect.go
// dry-runs a not-yet-installed stdio server through it; discover.go and
// CallMCPTool drive already-installed servers (stdio OR remote http/sse)
// through the same code. Adding a second client would mean two places to get
// JSON-RPC framing, id correlation and error mapping wrong.

// mcpSession is one live, initialized MCP connection.
type mcpSession interface {
	// call sends a request and returns the decoded JSON-RPC envelope,
	// correlated by id. A JSON-RPC "error" member is returned as a Go error.
	call(ctx context.Context, method string, params any) (map[string]any, error)
	notify(ctx context.Context, method string, params any) error
	Close() error
}

// rpcError is a JSON-RPC error member surfaced as a Go error.
type rpcError struct {
	Code    int
	Message string
}

func (e *rpcError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("mcp: server error %d: %s", e.Code, e.Message)
	}
	return "mcp: server error: " + e.Message
}

// resultOf pulls the "result" object out of an envelope, mapping an "error"
// member to a Go error so no caller has to remember to check both.
func resultOf(env map[string]any) (map[string]any, error) {
	if raw, ok := env["error"]; ok && raw != nil {
		e := &rpcError{}
		if m, ok := raw.(map[string]any); ok {
			if c, ok := m["code"].(float64); ok {
				e.Code = int(c)
			}
			e.Message, _ = m["message"].(string)
		}
		if e.Message == "" {
			e.Message = fmt.Sprintf("%v", raw)
		}
		return nil, e
	}
	res, _ := env["result"].(map[string]any)
	if res == nil {
		res = map[string]any{}
	}
	return res, nil
}

// ---- stdio transport

type stdioSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	cancel context.CancelFunc
	mu     sync.Mutex
	nextID int
}

func dialStdio(ctx context.Context, command string, args []string, timeout time.Duration) (*stdioSession, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)

	// Run through an interactive login-ish shell (bash -ic), not the raw
	// command: the control-plane's own PATH (systemd, deliberately minimal)
	// has no reason to include a developer's node/nvm/pyenv paths, but the
	// SAME server, once installed, is launched by the harness inside the
	// user's own shell, which does have them via .bashrc.
	shellCmd := shellQuoteArgs(append([]string{command}, args...))
	cmd := exec.CommandContext(cctx, "bash", "-ic", shellCmd)
	// Deliberately NOT inheriting Devcules' own environment (which may hold
	// its own secrets) -- just HOME, so bash finds the right rc files.
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "TERM=dumb"}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not open stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("could not start (is the runtime installed?): %w", err)
	}
	return &stdioSession{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout), cancel: cancel}, nil
}

func (s *stdioSession) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *stdioSession) call(ctx context.Context, method string, params any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	if params == nil {
		params = map[string]any{}
	}
	if err := s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("could not send %s: %w", method, err)
	}
	// Correlate by id: a well-behaved server can (and the official reference
	// server does) interleave asynchronous notifications with responses.
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("no response to %s: %w", method, err)
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue // stray non-JSON output, keep waiting
		}
		idVal, hasID := m["id"]
		if !hasID {
			continue // a notification
		}
		if idF, ok := idVal.(float64); !ok || int(idF) != id {
			continue // a reply to some other request
		}
		return m, nil
	}
}

func (s *stdioSession) notify(ctx context.Context, method string, params any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return s.send(msg)
}

func (s *stdioSession) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// ---- streamable HTTP / SSE transport

type httpSession struct {
	url       string
	client    *http.Client
	sessionID string
	mu        sync.Mutex
	nextID    int
}

func dialHTTP(url string, timeout time.Duration) *httpSession {
	return &httpSession{url: url, client: &http.Client{Timeout: timeout}}
}

func (s *httpSession) post(ctx context.Context, body map[string]any) (map[string]any, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		s.sessionID = sid
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil // notification ack, no envelope
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(truncateBody(raw)))
	}
	// A streamable-HTTP server may answer with SSE instead of plain JSON.
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw = sseData(raw)
	}
	var env map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &env); err != nil {
		return nil, fmt.Errorf("unreadable response: %w", err)
	}
	return env, nil
}

// sseData concatenates the "data:" payload lines of an SSE body.
func sseData(raw []byte) []byte {
	var out bytes.Buffer
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			out.WriteString(strings.TrimSpace(after))
		}
	}
	return out.Bytes()
}

func truncateBody(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func (s *httpSession) call(ctx context.Context, method string, params any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	if params == nil {
		params = map[string]any{}
	}
	env, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if env == nil {
		return nil, fmt.Errorf("%s: server returned no response", method)
	}
	return env, nil
}

func (s *httpSession) notify(ctx context.Context, method string, params any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	_, err := s.post(ctx, msg)
	return err
}

func (s *httpSession) Close() error { return nil }

// ---- handshake + operations

// openMCPSession connects to an installed server (stdio or remote) and
// completes initialize -> notifications/initialized. The caller MUST Close.
func openMCPSession(ctx context.Context, spec MCPInstallSpec, timeout time.Duration) (mcpSession, error) {
	if timeout <= 0 {
		timeout = DefaultInspectTimeout
	}
	var sess mcpSession
	switch spec.Type {
	case "", "stdio":
		if strings.TrimSpace(spec.Command) == "" {
			return nil, fmt.Errorf("mcp: stdio server has no command")
		}
		s, err := dialStdio(ctx, spec.Command, spec.Args, timeout)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		sess = s
	case "http", "sse", "streamable-http":
		if strings.TrimSpace(spec.URL) == "" {
			return nil, fmt.Errorf("mcp: remote server has no url")
		}
		sess = dialHTTP(spec.URL, timeout)
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q", spec.Type)
	}

	env, err := sess.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "devcules", "version": "0.1"},
	})
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("mcp: initialize failed: %w", err)
	}
	if _, err := resultOf(env); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("mcp: server rejected initialize: %w", err)
	}
	_ = sess.notify(ctx, "notifications/initialized", nil)
	return sess, nil
}

// DiscoveredTool is one tool as the server itself declares it, including the
// raw input schema (kept as JSON text so nothing is lost in translation --
// it is what the router shows an LLM to build arguments from).
type DiscoveredTool struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	InputSchema string `json:"input_schema"`
}

// listMCPTools performs tools/list on an open session.
func listMCPTools(ctx context.Context, sess mcpSession) ([]DiscoveredTool, error) {
	env, err := sess.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/list failed: %w", err)
	}
	res, err := resultOf(env)
	if err != nil {
		return nil, fmt.Errorf("mcp: server rejected tools/list: %w", err)
	}
	raw, _ := res["tools"].([]any)
	out := make([]DiscoveredTool, 0, len(raw))
	for _, rt := range raw {
		tm, ok := rt.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		t := DiscoveredTool{Name: name}
		t.Title, _ = tm["title"].(string)
		t.Description, _ = tm["description"].(string)
		if schema, ok := tm["inputSchema"]; ok && schema != nil {
			if b, err := json.Marshal(schema); err == nil {
				t.InputSchema = string(b)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// CallMCPTool connects to one MCP server, invokes a single tool, and returns
// its text content joined by newlines.
//
// Every failure mode is a plain Go error, never a panic: the server may be
// down, the tool may have been removed since discovery, or the tool may
// answer isError:true (a tool-level failure, which is still a failure the
// caller must not narrate as success).
func CallMCPTool(ctx context.Context, spec MCPInstallSpec, tool string, args map[string]any, timeout time.Duration) (string, error) {
	if strings.TrimSpace(tool) == "" {
		return "", fmt.Errorf("mcp: no tool name given")
	}
	sess, err := openMCPSession(ctx, spec, timeout)
	if err != nil {
		return "", err
	}
	defer sess.Close()

	if args == nil {
		args = map[string]any{}
	}
	env, err := sess.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return "", fmt.Errorf("mcp: calling %s failed: %w", tool, err)
	}
	res, err := resultOf(env)
	if err != nil {
		// e.g. "Unknown tool" when the tool was removed since discovery.
		return "", fmt.Errorf("mcp: calling %s failed: %w", tool, err)
	}
	text := joinTextContent(res["content"])
	if isErr, _ := res["isError"].(bool); isErr {
		if strings.TrimSpace(text) == "" {
			text = "the tool reported an error but returned no message"
		}
		return "", fmt.Errorf("mcp: tool %s reported an error: %s", tool, text)
	}
	return text, nil
}

// joinTextContent flattens MCP content blocks to their text parts. Non-text
// blocks (images, resources) are skipped rather than rendered as noise.
func joinTextContent(v any) string {
	blocks, _ := v.([]any)
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := bm["type"].(string); t != "" && t != "text" {
			continue
		}
		if s, ok := bm["text"].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}
