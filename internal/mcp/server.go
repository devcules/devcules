package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
)

const (
	// maxBody caps one JSON-RPC message.
	maxBody = 1 << 20
	// listTTL is how long clients may cache tools/list (2026-07-28).
	listTTL = 5 * time.Minute
	// callBurst and callRate bound tool calls per launch.
	callBurst = 30
	callRate  = 5.0 // per second
)

// Options wires a Server. Tokens and Tools are required.
type Options struct {
	Tokens *launch.Tokens
	Tools  *Registry
	// AuditLog records each tool call; nil records nothing.
	AuditLog *audit.Log
	// PublicURL is the one browser origin allowed to call; requests
	// with any other Origin header are refused (DNS rebinding).
	PublicURL string
	// Version is serverInfo.version.
	Version string
	// Instructions is shown to the model by clients that support it.
	Instructions string
	Log          *slog.Logger
	// PageSize is tools/list's page; 0 means 50.
	PageSize int
}

// Server serves POST /mcp.
type Server struct {
	o       Options
	origin  string
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

// New builds a Server.
func New(o Options) (*Server, error) {
	if o.Tokens == nil || o.Tools == nil {
		return nil, errors.New("mcp: tokens and tools are required")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.PageSize <= 0 {
		o.PageSize = 50
	}
	s := &Server{o: o, buckets: map[string]*bucket{}, now: time.Now}
	if u, err := url.Parse(o.PublicURL); err == nil && u.Scheme != "" && u.Host != "" {
		s.origin = u.Scheme + "://" + u.Host
	}
	return s, nil
}

func (s *Server) info() implementation {
	v := s.o.Version
	if v == "" {
		v = "dev"
	}
	return implementation{Name: "devcules", Title: "Devcules", Version: v}
}

func (s *Server) capabilities() map[string]any {
	// listChanged false: the tool set is fixed for the life of serve.
	return map[string]any{"tools": map[string]any{"listChanged": false}}
}

// exchange is what the log line records about one request.
type exchange struct {
	method, tool, version, launch string
	status                        int
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	x := exchange{}
	defer func() {
		attrs := []any{"method", x.method, "status", x.status, "ms", s.now().Sub(start).Milliseconds()}
		for _, kv := range [][2]string{{"tool", x.tool}, {"version", x.version}, {"launch", x.launch}} {
			if kv[1] != "" {
				attrs = append(attrs, kv[0], kv[1])
			}
		}
		s.o.Log.Info("mcp request", attrs...)
	}()
	send := func(status int, id json.RawMessage, result any, f *failure) {
		if f != nil {
			status = f.status
		}
		x.status = status
		if id == nil {
			id = json.RawMessage("null")
		}
		resp := response{JSONRPC: "2.0", ID: id, Result: result}
		if f != nil {
			resp.Result, resp.Error = nil, &f.err
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}

	if r.Method != http.MethodPost {
		// No GET stream and no sessions to DELETE, in either era.
		w.Header().Set("Allow", http.MethodPost)
		send(0, nil, nil, fail(http.StatusMethodNotAllowed, codeInvalidRequest, "the MCP endpoint takes POST only"))
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != s.origin {
		send(0, nil, nil, fail(http.StatusForbidden, codeInvalidRequest, "origin not allowed"))
		return
	}
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	l, err := s.o.Tokens.Check(r.Context(), strings.TrimSpace(token))
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="devcules"`)
		msg := "missing or invalid launch token: send it as Authorization: Bearer <token>"
		if errors.Is(err, launch.ErrExpired) {
			msg = "launch token expired: start the agent again for a fresh one"
		} else if errors.Is(err, launch.ErrRevoked) {
			msg = "launch token revoked"
		}
		send(0, nil, nil, fail(http.StatusUnauthorized, codeInvalidRequest, msg))
		return
	}
	caller := Caller{LaunchID: l.ID, Label: l.Label, Provider: l.Provider}
	x.launch = l.ID

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		send(0, nil, nil, fail(http.StatusRequestEntityTooLarge, codeInvalidRequest, "message larger than 1 MiB"))
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		send(0, nil, nil, fail(http.StatusBadRequest, codeInvalidRequest, "one JSON-RPC message per POST: batches are not supported"))
		return
	}
	var req request
	if err := json.Unmarshal(trimmed, &req); err != nil {
		send(0, nil, nil, fail(http.StatusBadRequest, codeParse, "parse error: the body is not a JSON-RPC message"))
		return
	}
	x.method = req.Method
	switch {
	case req.JSONRPC != "2.0":
		send(0, req.ID, nil, fail(http.StatusBadRequest, codeInvalidRequest, `jsonrpc must be "2.0"`))
		return
	case req.Result != nil || req.Error != nil:
		send(0, nil, nil, fail(http.StatusBadRequest, codeInvalidRequest, "clients must not send responses: this server sends no requests"))
		return
	case req.Method == "":
		send(0, req.ID, nil, fail(http.StatusBadRequest, codeInvalidRequest, "method is required"))
		return
	case string(req.ID) == "null":
		send(0, nil, nil, fail(http.StatusBadRequest, codeInvalidRequest, "id must be a string or a number, never null"))
		return
	}
	if req.isNotification() {
		// notifications/initialized, cancelled and the rest: nothing
		// to do, and a notification gets no JSON-RPC answer.
		x.status = http.StatusAccepted
		w.WriteHeader(http.StatusAccepted)
		return
	}

	params := map[string]json.RawMessage{}
	if len(req.Params) > 0 && string(req.Params) != "null" {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			send(0, req.ID, nil, fail(http.StatusBadRequest, codeInvalidParams, "params must be an object"))
			return
		}
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := params["_meta"]; ok {
		_ = json.Unmarshal(raw, &meta)
	}
	_, metaVersionSet := meta[metaVersion]
	if metaVersionSet || slices.Contains(modernVersions, r.Header.Get("MCP-Protocol-Version")) {
		result, f := s.modern(r, req, params, meta, caller, &x)
		send(http.StatusOK, req.ID, result, f)
		return
	}
	result, f := s.legacy(r, req, params, caller, &x)
	send(http.StatusOK, req.ID, result, f)
}

// modern serves one stateless 2026-07-28 request.
func (s *Server) modern(r *http.Request, req request, params, meta map[string]json.RawMessage, c Caller, x *exchange) (any, *failure) {
	var version string
	var caps map[string]any
	if json.Unmarshal(meta[metaVersion], &version) != nil || version == "" ||
		json.Unmarshal(meta[metaCapabilities], &caps) != nil || caps == nil {
		return nil, fail(http.StatusBadRequest, codeInvalidParams,
			`params._meta needs "`+metaVersion+`" (a string) and "`+metaCapabilities+`" (an object)`)
	}
	x.version = version
	if !slices.Contains(modernVersions, version) {
		f := fail(http.StatusBadRequest, codeUnsupportedVersion, "Unsupported protocol version")
		f.err.Data = map[string]any{"supported": supported(), "requested": version}
		return nil, f
	}
	if h := r.Header.Get("MCP-Protocol-Version"); h != version {
		return nil, fail(http.StatusBadRequest, codeHeaderMismatch, "MCP-Protocol-Version header "+strconv.Quote(h)+" does not match params._meta ("+version+")")
	}
	if h := r.Header.Get("Mcp-Method"); h != req.Method {
		return nil, fail(http.StatusBadRequest, codeHeaderMismatch, "Mcp-Method header "+strconv.Quote(h)+" does not match the method "+strconv.Quote(req.Method))
	}
	if req.Method == "tools/call" {
		var name string
		_ = json.Unmarshal(params["name"], &name)
		if h := r.Header.Get("Mcp-Name"); h != name {
			return nil, fail(http.StatusBadRequest, codeHeaderMismatch, "Mcp-Name header "+strconv.Quote(h)+" does not match params.name")
		}
	}
	var result map[string]any
	var f *failure
	switch req.Method {
	case "server/discover":
		result = map[string]any{
			"supportedVersions": supported(),
			"capabilities":      s.capabilities(),
			"ttlMs":             listTTL.Milliseconds(),
			"cacheScope":        "public",
		}
		if s.o.Instructions != "" {
			result["instructions"] = s.o.Instructions
		}
	case "tools/list":
		result, f = s.list(params)
		if f == nil {
			result["ttlMs"] = listTTL.Milliseconds()
			result["cacheScope"] = "public"
		}
	case "tools/call":
		result, f = s.call(r.Context(), params, c, x)
	default:
		return nil, fail(http.StatusNotFound, codeMethodNotFound, "Method not found: "+req.Method)
	}
	if f != nil {
		return nil, f
	}
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{metaServerInfo: s.info()}
	return result, nil
}

// supported lists every version this server speaks, newest first.
func supported() []string {
	return append(slices.Clone(modernVersions), legacyVersions...)
}

// legacy serves one request from an initialize-era client.
func (s *Server) legacy(r *http.Request, req request, params map[string]json.RawMessage, c Caller, x *exchange) (any, *failure) {
	if req.Method == "initialize" {
		var requested string
		_ = json.Unmarshal(params["protocolVersion"], &requested)
		// Negotiation: the same version when supported, else the
		// newest legacy one; the client disconnects if it cannot
		// speak it. A modern client never sends initialize.
		version := legacyVersions[0]
		if slices.Contains(legacyVersions, requested) {
			version = requested
		}
		x.version = version
		res := map[string]any{
			"protocolVersion": version,
			"capabilities":    s.capabilities(),
			"serverInfo":      s.info(),
		}
		if s.o.Instructions != "" {
			res["instructions"] = s.o.Instructions
		}
		return res, nil
	}
	version := r.Header.Get("MCP-Protocol-Version")
	if version == "" {
		// The spec's rule for clients that predate the header.
		version = Version20250326
	}
	x.version = version
	if !slices.Contains(legacyVersions, version) {
		f := fail(http.StatusBadRequest, codeInvalidRequest, "unsupported MCP-Protocol-Version "+strconv.Quote(version))
		f.err.Data = map[string]any{"supported": supported(), "requested": version}
		return nil, f
	}
	switch req.Method {
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		res, f := s.list(params)
		if f != nil {
			return nil, f
		}
		return res, nil
	case "tools/call":
		res, f := s.call(r.Context(), params, c, x)
		if f != nil {
			return nil, f
		}
		return res, nil
	}
	return nil, fail(http.StatusOK, codeMethodNotFound, "Method not found: "+req.Method)
}

// list pages through the tools. Cursors are opaque to clients: here,
// base64url of the offset.
func (s *Server) list(params map[string]json.RawMessage) (map[string]any, *failure) {
	tools := s.o.Tools.sorted()
	offset := 0
	if raw, ok := params["cursor"]; ok && string(raw) != "null" {
		var cursor string
		if json.Unmarshal(raw, &cursor) != nil {
			return nil, invalidParams("invalid cursor")
		}
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		n, aerr := strconv.Atoi(strings.TrimPrefix(string(b), "o:"))
		if err != nil || aerr != nil || !strings.HasPrefix(string(b), "o:") || n < 0 || n > len(tools) {
			return nil, invalidParams("invalid cursor")
		}
		offset = n
	}
	end := min(offset+s.o.PageSize, len(tools))
	out := make([]toolJSON, 0, end-offset)
	for _, t := range tools[offset:end] {
		out = append(out, t.listed())
	}
	res := map[string]any{"tools": out}
	if end < len(tools) {
		res["nextCursor"] = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(end)))
	}
	return res, nil
}

// call runs one tool. Unknown tools and malformed params are protocol
// errors; everything the tool itself reports is a result with isError.
func (s *Server) call(ctx context.Context, params map[string]json.RawMessage, c Caller, x *exchange) (map[string]any, *failure) {
	var name string
	if json.Unmarshal(params["name"], &name) != nil || name == "" {
		return nil, invalidParams("params.name (the tool) is required")
	}
	x.tool = name
	t, ok := s.o.Tools.get(name)
	if !ok {
		return nil, invalidParams("Unknown tool: " + name)
	}
	var res Result
	if !s.allow(c.LaunchID) {
		res = Errorf("rate limited: at most %d tool calls in a burst, %.0f a second after that; wait a moment and retry", callBurst, callRate)
	} else {
		var err error
		res, err = t.Call(ctx, c, params["arguments"])
		if err != nil {
			s.o.Log.Error("mcp tool failed", "tool", name, "launch", c.LaunchID, "err", err.Error())
			res = Errorf("internal error in %s", name)
		}
	}
	if s.o.AuditLog != nil {
		detail := map[string]string{"label": c.Label}
		if res.IsError {
			detail["error"] = "true"
		}
		_, _ = s.o.AuditLog.Record(ctx, audit.Event{Actor: "launch:" + c.LaunchID, Action: audit.MCPCall, Target: name, Detail: detail})
	}
	text := res.Text
	out := map[string]any{"isError": res.IsError}
	if res.Structured != nil {
		out["structuredContent"] = res.Structured
		if text == "" {
			// Older clients read only content: give them the JSON.
			b, _ := json.Marshal(res.Structured)
			text = string(b)
		}
	}
	out["content"] = []map[string]string{{"type": "text", "text": text}}
	return out, nil
}

// bucket is a token bucket for one launch's tool calls.
type bucket struct {
	tokens float64
	at     time.Time
}

func (s *Server) allow(launchID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	b, ok := s.buckets[launchID]
	if !ok {
		b = &bucket{tokens: callBurst, at: now}
		s.buckets[launchID] = b
	}
	b.tokens = min(callBurst, b.tokens+now.Sub(b.at).Seconds()*callRate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
