package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// runMockLLM serves a fake model provider for the smoke test: it
// echoes the last user message back ("echo: <text>") in OpenAI or
// Anthropic shape, streamed or not, and appends every text it received
// to a log file so the test can prove what left serve. It is test
// tooling, hidden from the usage line, and binds loopback only.
//
// MOCKLLM_LISTEN (default 127.0.0.1:7701) and MOCKLLM_LOG (default
// /tmp/mockllm.log) configure it.
func runMockLLM(stderr io.Writer) int {
	addr := os.Getenv("MOCKLLM_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:7701"
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		fmt.Fprintf(stderr, "mockllm: MOCKLLM_LISTEN %q must be a loopback address: it is test tooling\n", addr)
		return exitError
	}
	logPath := os.Getenv("MOCKLLM_LOG")
	if logPath == "" {
		logPath = "/tmp/mockllm.log"
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "mockllm: "+err.Error())
		return exitError
	}
	defer f.Close()
	if err := http.ListenAndServe(addr, mockLLM(f)); err != nil {
		fmt.Fprintln(stderr, "mockllm: "+err.Error())
		return exitError
	}
	return exitOK
}

// mockLLM is the fake provider's handler; seen receives one line per
// request with the text the provider got.
func mockLLM(seen io.Writer) http.Handler {
	var mu sync.Mutex
	record := func(text string) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(text)
		_, _ = fmt.Fprintf(seen, "%s\n", b)
	}
	lastUser := func(body map[string]any) string {
		msgs, _ := body["messages"].([]any)
		for i := len(msgs) - 1; i >= 0; i-- {
			m, _ := msgs[i].(map[string]any)
			if m["role"] == "user" {
				if s, ok := m["content"].(string); ok {
					return s
				}
			}
		}
		return ""
	}
	event := func(w io.Writer, name string, v any) {
		b, _ := json.Marshal(v)
		if name != "" {
			fmt.Fprintf(w, "event: %s\n", name)
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, `{"error":{"message":"no key","type":"authentication_error"}}`, http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		stream, _ := body["stream"].(bool)
		text := lastUser(body)
		reply := "echo: " + text
		switch r.URL.Path {
		case "/v1/chat/completions":
			record(text)
			if !stream {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "mock-1", "object": "chat.completion", "model": body["model"],
					"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"}}})
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, piece := range splitEvery(reply, 4) {
				event(w, "", map[string]any{"id": "mock-1", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": piece}, "finish_reason": nil}}})
			}
			event(w, "", map[string]any{"id": "mock-1", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "/v1/messages":
			record(text)
			if !stream {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "mock-1", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": reply}}, "stop_reason": "end_turn"})
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			event(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "mock-1", "type": "message", "role": "assistant", "content": []any{}}})
			event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			for _, piece := range splitEvery(reply, 4) {
				event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": piece}})
			}
			event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			event(w, "message_stop", map[string]any{"type": "message_stop"})
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"mock-model","object":"model","created":0,"owned_by":"devcules"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
}

func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, strings.Clone(s))
}
