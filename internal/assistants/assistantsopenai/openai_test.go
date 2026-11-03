package assistantsopenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/mcp"
)

func TestNewCaller(t *testing.T) {
	if _, err := NewCaller(" ", "http://x", "", nil); err == nil {
		t.Fatal("an empty model was accepted")
	}
	if _, err := NewCaller("m", "ftp://x", "", nil); err == nil {
		t.Fatal("a non-http base URL was accepted")
	}
	c, err := NewCaller("m-1", "https://api.example/", "", nil)
	if err != nil || c.Model() != "m-1" || c.base != "https://api.example" || c.cli.CheckRedirect == nil || c.cli.Timeout != CallTimeout {
		t.Fatalf("NewCaller = %+v, %v", c, err)
	}
}

// TestChatWire checks what goes up (system prompt, history, repaired
// tool pairs, MCP tools as functions, the bearer) and what comes back.
func TestChatWire(t *testing.T) {
	var got map[string]any
	var auth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done","tool_calls":[
			{"id":"c9","function":{"name":"status","arguments":""}},
			{"id":"c10","function":{"name":"terminals","arguments":"{\"runner\":\"laptop\"}"}}]}}]}`))
	}))
	t.Cleanup(up.Close)
	c, err := NewCaller("m-1", up.URL, "tok-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := c.Chat(context.Background(), assistants.ChatRequest{
		System: "be brief",
		Messages: []assistants.ChatMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []assistants.ToolCallAsk{{ID: "c1", Name: "status", Args: json.RawMessage(`{}`)}}},
			{Role: "tool", ToolCallID: "c1", Content: "ok"},
			// Its call was compacted away: folded into user text.
			{Role: "tool", ToolCallID: "gone", Content: "old"},
		},
		Tools: []mcp.Tool{{Name: "status", Description: "the status", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok-1" {
		t.Fatalf("auth = %q", auth)
	}
	if reply.Text != "done" || len(reply.ToolCalls) != 2 || string(reply.ToolCalls[0].Args) != "{}" ||
		reply.ToolCalls[1].Name != "terminals" || string(reply.ToolCalls[1].Args) != `{"runner":"laptop"}` {
		t.Fatalf("reply = %+v", reply)
	}
	msgs, _ := got["messages"].([]any)
	if got["model"] != "m-1" || got["tool_choice"] != "auto" || len(msgs) != 5 {
		t.Fatalf("request = %v", got)
	}
	first, _ := msgs[0].(map[string]any)
	call, _ := msgs[2].(map[string]any)
	tool, _ := msgs[3].(map[string]any)
	orphan, _ := msgs[4].(map[string]any)
	if first["role"] != "system" || call["tool_calls"] == nil || tool["tool_call_id"] != "c1" ||
		orphan["role"] != "user" || orphan["content"] != "(earlier tool result) old" {
		t.Fatalf("messages = %v", msgs)
	}
	tools, _ := got["tools"].([]any)
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "status" || fn["description"] != "the status" {
		t.Fatalf("tools = %v", tools)
	}
}

func TestChatErrors(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(elsewhere.Close)
	cases := map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"error message": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
		}, "429 slow down"},
		"bare status": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }, "502 Bad Gateway"},
		"not json":    {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }, "bad reply"},
		"no choice":   {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }, "no choice"},
		// A redirect is an answer, never followed with the bearer.
		"redirect": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
		}, "307"},
	}
	for name, c := range cases {
		up := httptest.NewServer(c.handler)
		caller, _ := NewCaller("m", up.URL, "", nil)
		_, err := caller.Chat(context.Background(), assistants.ChatRequest{Messages: []assistants.ChatMessage{{Role: "user", Content: "x"}}})
		up.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("the redirect was followed")
	}
	// No upstream at all, and a cancelled context.
	caller, _ := NewCaller("m", "http://127.0.0.1:1", "", NoRedirect(http.DefaultTransport))
	if _, err := caller.Chat(context.Background(), assistants.ChatRequest{}); err == nil {
		t.Fatal("an unreachable upstream answered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := caller.Chat(ctx, assistants.ChatRequest{}); err == nil {
		t.Fatal("a cancelled call answered")
	}
}

func TestEncodeWithoutToolsOrSystem(t *testing.T) {
	raw, err := encode(assistants.ChatRequest{Messages: []assistants.ChatMessage{{Role: "user", Content: "hi"}}}, "m")
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); strings.Contains(s, "tools") || strings.Contains(s, "system") {
		t.Fatalf("encode = %s: no tools and no system message when there are none", s)
	}
}
