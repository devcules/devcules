package assistanttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/mcp"
)

// AdapterRunner is how an OpenAI-style adapter registers for Run:
// it hands back the wired assistant over an httptest model playing
// the script, with the `allow` map deciding the gate.
type AdapterRunner func(t *testing.T, upstream *httptest.Server, gate func(context.Context, assistants.ToolCallAsk) assistants.GateResult) assistants.Assistant

// RunUpstream plays the whole suite against an adapter whose model is
// an httptest server serving the scripted replies. gateOf builds the
// adapter's gate from the allow map; the recorder the suite checks
// comes back from the same call.
func RunUpstream(t *testing.T, build AdapterRunner,
	gateOf func(t *testing.T, allow map[string]string) (func(context.Context, assistants.ToolCallAsk) assistants.GateResult, *sync.Map)) {
	t.Helper()
	Run(t, func(t *testing.T, script *Script, allow map[string]string) (assistants.Assistant, *sync.Map) {
		t.Helper()
		ups := Upstream(t, script)
		gate, ran := gateOf(t, allow)
		return build(t, ups, gate), ran
	})
}

// Upstream is the mock OpenAI-style model: one chat-completions reply
// per POST, drawn from the script, with the bearer checked.
func Upstream(t *testing.T, script *Script) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		reply, err := script.Chat(r.Context(), assistants.ChatRequest{})
		if err != nil {
			t.Errorf("script: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		msg := map[string]any{"role": "assistant"}
		if reply.Text != "" {
			msg["content"] = reply.Text
		}
		if len(reply.ToolCalls) > 0 {
			var calls []map[string]any
			for _, c := range reply.ToolCalls {
				calls = append(calls, map[string]any{
					"id": c.ID, "type": "function",
					"function": map[string]any{"name": c.Name, "arguments": string(c.Args)},
				})
			}
			msg["tool_calls"] = calls
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}},
		})
	}))
	t.Cleanup(s.Close)
	return s
}

// GateOf is the standard gate for a contract run: names in allow run
// and record their arguments, everything else is denied and recorded.
// Serve's real gate is the approvals service; the suite runs the same
// decisions through this stub.
func GateOf(t *testing.T, allow map[string]string) (func(context.Context, assistants.ToolCallAsk) assistants.GateResult, *sync.Map) {
	t.Helper()
	ran := &sync.Map{}
	return func(_ context.Context, ask assistants.ToolCallAsk) assistants.GateResult {
		text, ok := allow[ask.Name]
		if !ok {
			ran.Store(ask.Name, "denied:"+string(ask.Args))
			return assistants.GateResult{Decision: "deny", Result: mcp.Result{
				Text: "the owner denied this call through the test gate", IsError: true}}
		}
		ran.Store(ask.Name, "ran:"+string(ask.Args))
		return assistants.GateResult{Decision: "allow", Result: mcp.Result{Text: text}}
	}, ran
}
