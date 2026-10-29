package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/devcules/devcules/internal/redact"
)

// FuzzChatStream: whatever the model says and however the provider
// cuts it into events, the client's concatenated deltas equal the
// whole reply restored at once.
func FuzzChatStream(f *testing.F) {
	f.Add("plain reply", 3)
	f.Add("key {P} twice {P}", 1)
	f.Add("RRR{P}R", 2)
	f.Add("REDACTED_SECRET_{P}", 5)
	r, _ := redact.New(bytes.Repeat([]byte{9}, 32))
	f.Fuzz(func(t *testing.T, text string, chunk int) {
		// JSON carries only valid UTF-8, and providers cut on rune
		// boundaries, so the fuzzer does too.
		if chunk < 1 || chunk > 64 || len(text) > 2000 || !utf8.ValidString(text) {
			return
		}
		s := r.NewTestSession(redact.DefaultPolicy())
		masked, findings, _ := s.Redact("k " + fakeAWS)
		if len(findings) != 1 {
			t.Fatalf("setup: %v", findings)
		}
		ph := findings[0].Placeholder
		_ = masked
		reply := strings.ReplaceAll(text, "{P}", ph)
		var up strings.Builder
		for _, p := range runePieces(reply, chunk) {
			b, _ := json.Marshal(map[string]any{"object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": p}, "finish_reason": nil}}})
			fmt.Fprintf(&up, "data: %s\n\n", b)
		}
		fmt.Fprint(&up, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		w := httptest.NewRecorder()
		if err := pipeSSE(w, strings.NewReader(up.String()), s); err != nil {
			t.Fatal(err)
		}
		var got string
		for _, ev := range readSSE(t, w.Body.String()) {
			for _, c := range ev["choices"].([]any) {
				if s, ok := c.(map[string]any)["delta"].(map[string]any)["content"].(string); ok {
					got += s
				}
			}
		}
		if want := s.Restore(reply); got != want {
			t.Fatalf("streamed %q, want %q", got, want)
		}
	})
}

func runePieces(s string, n int) []string {
	var out []string
	r := []rune(s)
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return append(out, string(r))
}
