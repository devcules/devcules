// Package assistantsopenai is the shared chat-completions caller
// every OpenAI-style assistant adapter speaks: builtin on the desk's
// helper model, hermes and openclaw on their own services. It is one
// assistants.ModelCaller over POST {base}/v1/chat/completions with
// the bearer handed to it, translating the loop's history and MCP
// tool list into that format and back. Gating, redaction and sessions
// stay in internal/assistants; this package adds only the wire
// format, the call timeout, body caps and the no-redirect rule.
//
// Adapters construct a Caller with their endpoint and pass it as the
// loop's ModelCaller; they never hold a provider key anywhere but in
// memory beside serve, and the desk's redaction boundary is the
// router's.
package assistantsopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/mcp"
)

// CallTimeout caps one model call so a stuck endpoint cannot pin a
// turn's lock. It must leave room for an approval answered by hand.
const CallTimeout = 90 * time.Second

// maxBody caps both directions of the chat call.
const maxBody = 4 << 20

// Caller is assistants.ModelCaller for one OpenAI-compatible
// chat-completions endpoint.
type Caller struct {
	model string
	base  string
	token string
	cli   *http.Client
}

// NewCaller wires a Caller: model and baseURL of the upstream
// ("/v1/chat/completions" is appended), bearer its credential. The
// bearer being empty is not an error here: the call then fails
// upstream and the turn ends with the assistants-level "helper model
// could not be reached", matching the never-fails-to-load rule.
func NewCaller(model, baseURL, bearer string, cli *http.Client) (*Caller, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("assistantsopenai: a model is required")
	}
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("assistantsopenai: the base URL must be http(s): %q", baseURL)
	}
	if cli == nil {
		cli = NoRedirect(nil)
	}
	return &Caller{model: model, base: base, token: bearer, cli: cli}, nil
}

// NoRedirect is the client every adapter uses: a 3xx from the
// upstream is an answer, not a follow, because the bearer must never
// travel to where a redirect points. Wrap a tuned Transport; nil gets
// the default one.
func NoRedirect(t http.RoundTripper) *http.Client {
	if t == nil {
		t = http.DefaultTransport
	}
	return &http.Client{
		Transport:     t,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       CallTimeout,
	}
}

// Model names the model each call asks for.
func (c *Caller) Model() string { return c.model }

// Chat implements assistants.ModelCaller. The history is re-encoded
// on every call (a compaction may have rewritten it), so the loop,
// not this caller, owns what the model sees.
func (c *Caller) Chat(ctx context.Context, req assistants.ChatRequest) (assistants.ModelReply, error) {
	body, err := encode(req, c.model)
	if err != nil {
		return assistants.ModelReply{}, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return assistants.ModelReply{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.cli.Do(r)
	if err != nil {
		return assistants.ModelReply{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return assistants.ModelReply{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return assistants.ModelReply{}, upstreamError(resp.StatusCode, raw)
	}
	return decode(raw)
}

// upstreamError reads the wire's error.message when present, else the
// status; the body is capped in the message too.
func upstreamError(code int, raw []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := http.StatusText(code)
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	return fmt.Errorf("assistantsopenai: upstream answered %d %s", code, assistants.Trim(msg))
}

// encode builds one chat-completions request: system prompt, history
// with pair-validated tool calls, and the MCP tools in OpenAI's shape.
func encode(req assistants.ChatRequest, model string) ([]byte, error) {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range pair(req.Messages) {
		one := map[string]any{"role": m.Role}
		if m.Content != "" {
			one["content"] = m.Content
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, t := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id": t.ID, "type": "function",
					"function": map[string]any{"name": t.Name, "arguments": string(t.Args)},
				})
			}
			one["tool_calls"] = calls
		}
		if m.Role == "tool" {
			one["tool_call_id"] = m.ToolCallID
		}
		msgs = append(msgs, one)
	}
	tools := toolDefs(req.Tools)
	body := map[string]any{"model": model, "messages": msgs}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	return json.Marshal(body)
}

// pair repairs the one invariant OpenAI-style endpoints enforce: a
// tool message must name the id of an immediately-preceding assistant
// tool_call. A compaction may rewrite the call out with its arguments
// kept in the loop's own record, so an orphan result here is folded
// into user text rather than breaking the call.
func pair(in []assistants.ChatMessage) []assistants.ChatMessage {
	out := make([]assistants.ChatMessage, 0, len(in))
	known := map[string]bool{}
	for _, m := range in {
		if m.Role == "assistant" {
			for _, t := range m.ToolCalls {
				known[t.ID] = true
			}
		}
		if m.Role == "tool" && !known[m.ToolCallID] {
			m = assistants.ChatMessage{Role: "user",
				Content: "(earlier tool result) " + m.Content, ToolCalls: nil, ToolCallID: ""}
		}
		out = append(out, m)
	}
	return out
}

// toolDefs renders the MCP registry as OpenAI function tools, keeping
// the MCP name and input schema.
func toolDefs(tools []mcp.Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return out
}

// decode reads the first choice of a chat-completions reply into a
// ModelReply. A missing shape is a hard error (the loop ends the turn
// with "the helper model could not be reached").
func decode(raw []byte) (assistants.ModelReply, error) {
	var wire struct {
		Choices []struct {
			Message struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return assistants.ModelReply{}, fmt.Errorf("assistantsopenai: bad reply: %w", err)
	}
	if len(wire.Choices) == 0 {
		return assistants.ModelReply{}, errors.New("assistantsopenai: the model returned no choice")
	}
	m := wire.Choices[0].Message
	var reply assistants.ModelReply
	if m.Content != nil {
		reply.Text = *m.Content
	}
	for _, t := range m.ToolCalls {
		// An empty arguments string is "{}", because the gate's
		// command and the model's own re-reading need valid JSON.
		args := t.Function.Arguments
		if args == "" {
			args = "{}"
		}
		reply.ToolCalls = append(reply.ToolCalls, assistants.ToolCallAsk{
			ID:   t.ID,
			Name: t.Function.Name,
			Args: json.RawMessage(args),
		})
	}
	return reply, nil
}
