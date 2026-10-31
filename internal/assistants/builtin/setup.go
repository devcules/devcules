package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/mcp"
	"github.com/devcules/devcules/internal/redact"
)

// ChatCap bounds one assistant turn end-to-end: the model gets this
// long to finish, so a wedged upstream cannot pin a chat even with
// the gate answering. It must cover an approval answered by hand.
const ChatCap = 10 * time.Minute

// RunnerTarget is the approvals target the built-in assistant's calls
// count as: it gets its own row in Access, beside the terminal
// runners, because a setup helper is not any runner's agent.
const RunnerTarget = "assistant:builtin"

// LaunchID ties the assistant's approval records together in audit.
const LaunchID = "assist:builtin"

// SetupOptions wires Builtin: everything the assistant needs from
// serve, taken as constructors late so a part that was never built
// fails its own message, not the whole desk.
type SetupOptions struct {
	// Model, BaseURL and Key pick the helper model and credential
	// serve resolved at startup (the default profile, META_API_KEY).
	Model, BaseURL, Key string
	// MCP is the serve MCP server the tools run against in-process.
	MCP http.Handler
	// Launches mints the launch token the in-process tool calls
	// carry; nil runs tool calls tokenless when the MCP server accepts
	// it (tests).
	Launches *launch.Tokens
	// Decider is the desk's approvals service; nil runs the
	// assistant in shadow mode (answers, never calls a tool), which is
	// still useful on a read-only desk.
	Decider *approvals.Service
	// Redact and Settings drive the user-message round trip; nil
	// passes messages through.
	Redact   *redact.Redactor
	Settings *redact.Settings
	// Client is how the model call is made; http.DefaultClient by
	// default.
	Client *http.Client
	// OnEvent hooks the loop's events to the audit log.
	OnEvent assistants.Broadcaster
	Now     func() time.Time
}

// Builtin builds the built-in assistant: the shared loop over a
// chat-completions Caller, the MCP tools it may use behind the
// approvals gate, and the redaction round trip on user messages.
//
// It never fails to construct because a dependency is nil: a missing
// approvals service or redactor narrows the assistant (shadow mode,
// no masking) instead of breaking serve. A bad model/base URL is an
// error because that is a wiring bug, not a runtime condition.
func Builtin(o SetupOptions) (*Assistant, error) {
	cli := o.Client
	if cli == nil {
		cli = http.DefaultClient
	}
	// The model call never follows a redirect: an upstream that
	// answers one is asking for the bearer elsewhere, which the
	// router's contract forbids. Callers get the 3xx body instead.
	cli = &http.Client{
		Transport:     cli.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       CallTimeout,
	}
	caller, err := NewCaller(o.Model, o.BaseURL, o.Key, cli)
	if err != nil {
		return nil, err
	}
	if o.Decider != nil {
		// The assistant's own target must let its calls through even
		// before the owner has visited Access; "Ask on critical" makes
		// plain reads run and a change wait, which is the built-in's
		// advertised shape. A target the Access page already shows is
		// left alone.
		if _, seen := o.Decider.Modes()[approvals.Target("builtin")]; !seen {
			if err := o.Decider.SetMode(context.Background(), approvals.Target("builtin"), approvals.ModeCritical, "setup", ""); err != nil {
				return nil, fmt.Errorf("builtin: %w", err)
			}
		}
	}
	// The tool calls carry a launch token minted for the assistant
	// (the assistant id is the label a trace shows), so the MCP
	// path's checks, rate limits and audit see one more launch, not a
	// sidestep. A nil Launches leaves the token empty for tests whose
	// MCP server accepts it.
	token := ""
	if o.Launches != nil {
		tok, _, err := o.Launches.Mint(context.Background(), "builtin", "", ChatCap)
		if err != nil {
			return nil, fmt.Errorf("builtin: mint the assistant's token: %w", err)
		}
		token = tok
	}
	runner := toolsRunner{MCP: o.MCP, Token: token}
	opts := assistants.Options{
		Model:   caller,
		Tools:   runner.tools,
		Now:     o.Now,
		OnEvent: o.OnEvent,
	}
	var red assistants.RedactIO
	if o.Redact != nil && o.Settings != nil {
		red = assistants.NewSessionRedact(o.Redact, o.Settings)
	}
	if o.Decider == nil {
		// Shadow mode: the model answers from its own knowledge and
		// never calls a tool. The gate denies everything, and the
		// assistant says so, instead of failing to load on a desk
		// that has no approvals service yet.
		opts.Gate = func(context.Context, assistants.ToolCallAsk) assistants.GateResult {
			return assistants.GateResult{Decision: "deny", Result: mcp.Result{
				Text: "this desk has no approvals service, so the assistant cannot use tools; it can still answer", IsError: true}}
		}
	} else {
		opts.Gate = assistants.ApprovalsGate(assistants.PolicyOptions{
			Decider:  o.Decider,
			Runner:   "builtin",
			Agent:    "builtin",
			LaunchID: LaunchID,
		}, runner.run)
	}
	loop, err := assistants.NewLoop("builtin", opts, red)
	if err != nil {
		return nil, err
	}
	return New(loop)
}

// toolsRunner is the assistant's execution view of the MCP server:
// tools/list and tools/call over httptest, never leaving the process.
type toolsRunner struct {
	MCP   http.Handler
	Token string
}

func (r toolsRunner) run(ctx context.Context, ask assistants.ToolCallAsk) (mcp.Result, error) {
	body := map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "tools/call",
		"params": map[string]any{"name": ask.Name, "arguments": json.RawMessage(ask.Args)},
	}
	res, err := r.rpc(ctx, body)
	if err != nil {
		return mcp.Result{}, err
	}
	var wire struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res, &wire); err != nil {
		return mcp.Result{}, fmt.Errorf("builtin: bad tool reply: %w", err)
	}
	if wire.Error != nil {
		return mcp.Result{Text: wire.Error.Message, IsError: true}, nil
	}
	if wire.Result == nil {
		return mcp.Result{}, errors.New("builtin: the tool reply had no result")
	}
	var b strings.Builder
	for _, c := range wire.Result.Content {
		b.WriteString(c.Text)
	}
	return mcp.Result{Text: b.String(), IsError: wire.Result.IsError}, nil
}

// tools lists what the assistant may name, from the live MCP registry.
func (r toolsRunner) tools() []mcp.Tool {
	if r.MCP == nil {
		return nil
	}
	res, err := r.rpc(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "tools/list", "params": map[string]any{}})
	if err != nil {
		return nil
	}
	var wire struct {
		Result *struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &wire); err != nil || wire.Result == nil {
		return nil
	}
	out := make([]mcp.Tool, 0, len(wire.Result.Tools))
	for _, t := range wire.Result.Tools {
		out = append(out, mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// rpc does one POST against the in-process MCP handler.
func (r toolsRunner) rpc(ctx context.Context, body map[string]any) ([]byte, error) {
	if r.MCP == nil {
		return nil, errors.New("builtin: MCP server not configured")
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	rec := httptest.NewRecorder()
	r.MCP.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return out, nil
}
