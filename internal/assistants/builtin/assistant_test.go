package builtin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistantsopenai"
	"github.com/devcules/devcules/internal/assistants/assistanttest"
	"github.com/devcules/devcules/internal/assistants/builtin"
	"github.com/devcules/devcules/internal/mcp"
)

// modelServer plays each script step back as one chat-completions
// reply, one POST per loop model call.
func modelServer(t *testing.T, script *assistanttest.Script) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []map[string]any `json:"messages"`
			Tools    []map[string]any `json:"tools"`
			Model    string           `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad model request: %v", err)
		}
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

// wired builds the assistant the suite drives: same gate shape serve
// uses, but decided by the `allow` map, with every call recorded.
func wired(t *testing.T, script *assistanttest.Script, allow map[string]string) (assistants.Assistant, *sync.Map) {
	t.Helper()
	upstream := modelServer(t, script)
	caller, err := assistantsopenai.NewCaller(builtin.DefaultModel, upstream.URL, "test-key", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	ran := &sync.Map{}
	gate := func(_ context.Context, ask assistants.ToolCallAsk) assistants.GateResult {
		text, ok := allow[ask.Name]
		if !ok {
			ran.Store(ask.Name, "denied:"+string(ask.Args))
			return assistants.GateResult{Decision: "deny",
				Result: mcp.Result{Text: "the owner denied this call through the test gate", IsError: true}}
		}
		ran.Store(ask.Name, "ran:"+string(ask.Args))
		return assistants.GateResult{Decision: "allow", Result: mcp.Result{Text: text}}
	}
	loop, err := assistants.NewLoop("builtin", assistants.Options{
		Model: caller, Tools: assistanttest.Tools, Gate: gate,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := builtin.New(loop)
	if err != nil {
		t.Fatal(err)
	}
	return a, ran
}

// TestScriptedConversation is the M5.1 gate: the contract suite's
// seeded conversations play against the real HTTP model with approval
// enforced by the gate.
func TestScriptedConversation(t *testing.T) {
	assistanttest.Run(t, wired)
}

// TestUpstreamError: a failed model call ends the turn with the
// assistants-level error, never a fake reply.
func TestUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"spillover"}}`))
	}))
	t.Cleanup(upstream.Close)
	caller, err := assistantsopenai.NewCaller(builtin.DefaultModel, upstream.URL, "k", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	loop, err := assistants.NewLoop("builtin", assistants.Options{
		Model: caller, Tools: assistanttest.Tools,
		Gate: func(context.Context, assistants.ToolCallAsk) assistants.GateResult {
			return assistants.GateResult{Decision: "deny", Result: mcp.Result{Text: "x", IsError: true}}
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := builtin.New(loop)
	_, err = a.Chat(context.Background(), "", "hello")
	if err == nil || !strings.Contains(err.Error(), "helper model") {
		t.Fatalf("err = %v", err)
	}
}

// TestTokenGate: the caller sends the bearer, and only the bearer —
// no other credential is constructed or sent.
func TestTokenGate(t *testing.T) {
	var gotAuth, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(upstream.Close)
	caller, err := assistantsopenai.NewCaller("m", upstream.URL, "secret-bearer", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = caller.Chat(context.Background(), assistants.ChatRequest{Messages: []assistants.ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-bearer" || gotCookie != "" {
		t.Fatalf("Authorization=%q Cookie=%q", gotAuth, gotCookie)
	}
}

// TestPairMissedCallWiresToolResult: a tool result whose call left the
// resent history is rewritten as user text, never sent as a bare tool
// message (OpenAI would reject the turn).
func TestPairMissedCallWiresToolResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		for _, m := range req.Messages {
			if m["role"] == "tool" {
				t.Errorf("an orphan tool message left the loop: %v", m)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(upstream.Close)
	caller, err := assistantsopenai.NewCaller("m", upstream.URL, "", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = caller.Chat(context.Background(), assistants.ChatRequest{Messages: []assistants.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCallID: "gone", Content: "the tool said up"},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNewCallerValidation: base URL and model are checked up front.
func TestNewCallerValidation(t *testing.T) {
	if _, err := assistantsopenai.NewCaller("", "http://x", "", nil); err == nil {
		t.Fatal("empty model accepted")
	}
	if _, err := assistantsopenai.NewCaller("m", "gopher://x", "", nil); err == nil {
		t.Fatal("gopher base accepted")
	}
}
