package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// fakeAWS is a made-up key in the AWS shape, built from parts so no
// file holds the whole shape.
const fakeAWS = "AKIA" + "Z7QWERTY4UIOP3XZ"

const upstreamKey = "upstream-test-key"

type testProvider struct{ base string }

func (p testProvider) ID() string      { return "mock" }
func (p testProvider) BaseURL() string { return p.base }
func (p testProvider) Auth(h http.Header, key string) {
	h.Set("Authorization", "Bearer "+key)
}

type keys map[string]string

func (k keys) Get(_ context.Context, name string) ([]byte, error) {
	v, ok := k[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return []byte(v), nil
}

// upstream is a mock provider. It records what reached it and echoes
// the user's text back, streamed in chunk-byte pieces when asked.
type upstream struct {
	mu      sync.Mutex
	seen    []string
	headers http.Header
	chunk   int
}

func (u *upstream) record(r *http.Request, text string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seen = append(u.seen, text)
	u.headers = r.Header.Clone()
}

func (u *upstream) last() (string, http.Header) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.seen) == 0 {
		return "", nil
	}
	return u.seen[len(u.seen)-1], u.headers
}

func pieces(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func sse(w http.ResponseWriter, event string, v any) {
	b, _ := json.Marshal(v)
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	w.(http.Flusher).Flush()
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	stream, _ := body["stream"].(bool)
	switch r.URL.Path {
	case "/v1/models":
		u.record(r, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ratelimit-Remaining-Requests", "2999")
		w.Header().Set("Set-Cookie", "leak=1")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"muse-spark-1.3","object":"model","created":1,"owned_by":"meta"}]}`))
	case "/v1/chat/completions":
		msgs, _ := body["messages"].([]any)
		text, _ := msgs[len(msgs)-1].(map[string]any)["content"].(string)
		u.record(r, text)
		reply := "you said: " + text
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		head := map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "m"}
		for _, p := range pieces(reply, u.chunk) {
			sse(w, "", merge(head, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": p}, "finish_reason": nil}}}))
		}
		for _, p := range pieces(`{"q":"`+text+`"}`, u.chunk) {
			sse(w, "", merge(head, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": p}}}}, "finish_reason": nil}}}))
		}
		sse(w, "", merge(head, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	case "/v1/messages":
		msgs, _ := body["messages"].([]any)
		text, _ := msgs[len(msgs)-1].(map[string]any)["content"].(string)
		u.record(r, text)
		reply := "you said: " + text
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "content": []any{map[string]any{"type": "text", "text": reply}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "m1"}})
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		for _, p := range pieces(reply, u.chunk) {
			sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": p}})
		}
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		sse(w, "message_stop", map[string]any{"type": "message_stop"})
	case "/v1/responses":
		text, _ := body["input"].(string)
		u.record(r, text)
		reply := "you said: " + text
		w.Header().Set("Content-Type", "text/event-stream")
		for i, p := range pieces(reply, u.chunk) {
			sse(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": p, "sequence_number": i})
		}
		sse(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": "msg_1", "output_index": 0, "content_index": 0, "text": reply})
		sse(w, "response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"output_text": reply}})
	default:
		http.NotFound(w, r)
	}
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

type fixture struct {
	router *Router
	up     *upstream
	tokens *launch.Tokens
	token  string
	logs   *bytes.Buffer
	keys   keys
}

func newFixture(t *testing.T, chunk int, policy redact.Policy) *fixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	up := &upstream{chunk: chunk}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	r, err := redact.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	if policy != nil {
		if err := settings.SetPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
	}
	tokens := launch.New(s)
	token, _, err := tokens.Mint(ctx, "test agent", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	k := keys{KeySecret("mock"): upstreamKey}
	rt, err := New(Options{
		Providers: []Provider{testProvider{base: srv.URL}},
		Keys:      k, Tokens: tokens, Redactor: r, Settings: settings,
		Log: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{router: rt, up: up, tokens: tokens, token: token, logs: &logs, keys: k}
}

func (f *fixture) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Cookie", "devcules_session=browser-cookie")
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

// checkUpstream proves the provider saw a placeholder, the real key
// and none of the client's own credentials.
func (f *fixture) checkUpstream(t *testing.T) {
	t.Helper()
	seen, h := f.up.last()
	if strings.Contains(seen, fakeAWS) || !strings.Contains(seen, "REDACTED_SECRET_") {
		t.Fatalf("upstream saw %q, want the key masked", seen)
	}
	if h.Get("Authorization") != "Bearer "+upstreamKey || h.Get("Cookie") != "" || strings.Contains(fmt.Sprint(h), f.token) {
		t.Fatalf("upstream headers %v, want the provider key and no client credentials", h)
	}
	for _, leak := range []string{fakeAWS, f.token, upstreamKey, "you said"} {
		if strings.Contains(f.logs.String(), leak) {
			t.Fatalf("log leaks %q: %s", leak, f.logs.String())
		}
	}
}

const userText = "deploy with " + fakeAWS + " please"

func TestChatNonStream(t *testing.T) {
	f := newFixture(t, 0, nil)
	w := f.do("POST", "/llm/mock/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"`+userText+`"}]}`, nil)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	var res struct {
		Choices []struct {
			Message struct{ Content string }
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Choices[0].Message.Content != "you said: "+userText {
		t.Fatalf("reply %q", w.Body)
	}
	f.checkUpstream(t)
	if !strings.Contains(f.logs.String(), `"redacted_secret":1`) {
		t.Fatalf("log line lacks the redaction count: %s", f.logs)
	}
}

// readSSE parses the router's stream into data payloads.
func readSSE(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		d, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || d == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(d), &m); err != nil {
			t.Fatalf("bad event %q: %v", d, err)
		}
		out = append(out, m)
	}
	return out
}

func TestChatStreamSplitPlaceholders(t *testing.T) {
	for _, chunk := range []int{1, 2, 3, 5, 7, 11, 64} {
		f := newFixture(t, chunk, nil)
		w := f.do("POST", "/llm/mock/v1/chat/completions", `{"model":"m","stream":true,"messages":[{"role":"user","content":"`+userText+`"}]}`, nil)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("chunk %d: status %d %q", chunk, w.Code, w.Header())
		}
		var content, args string
		for _, ev := range readSSE(t, w.Body.String()) {
			for _, c := range ev["choices"].([]any) {
				d := c.(map[string]any)["delta"].(map[string]any)
				if s, ok := d["content"].(string); ok {
					content += s
				}
				if tc, ok := d["tool_calls"].([]any); ok {
					args += tc[0].(map[string]any)["function"].(map[string]any)["arguments"].(string)
				}
			}
		}
		if content != "you said: "+userText || args != `{"q":"`+userText+`"}` {
			t.Fatalf("chunk %d: content %q args %q", chunk, content, args)
		}
		if !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
			t.Fatalf("chunk %d: stream does not end with [DONE]", chunk)
		}
		f.checkUpstream(t)
	}
}

func TestMessagesStream(t *testing.T) {
	for _, chunk := range []int{1, 4, 9, 100} {
		f := newFixture(t, chunk, nil)
		w := f.do("POST", "/llm/mock/v1/messages", `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"`+userText+`"}]}`, map[string]string{"Authorization": "", "X-Api-Key": ""})
		if w.Code != 401 || !strings.Contains(w.Body.String(), `"type":"error"`) {
			t.Fatalf("no token: %d %s", w.Code, w.Body)
		}
		w = f.do("POST", "/llm/mock/v1/messages", `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"`+userText+`"}]}`, map[string]string{"Authorization": "", "X-Api-Key": f.token, "Anthropic-Version": "2023-06-01"})
		var text string
		stopAfterText := false
		for _, ev := range readSSE(t, w.Body.String()) {
			switch ev["type"] {
			case "content_block_delta":
				if stopAfterText {
					t.Fatal("a delta after content_block_stop")
				}
				text += ev["delta"].(map[string]any)["text"].(string)
			case "content_block_stop":
				stopAfterText = true
			}
		}
		if text != "you said: "+userText {
			t.Fatalf("chunk %d: text %q", chunk, text)
		}
		if !strings.Contains(w.Body.String(), "event: content_block_delta\n") {
			t.Fatal("event names were dropped")
		}
		_, h := f.up.last()
		if h.Get("Anthropic-Version") != "2023-06-01" || h.Get("X-Api-Key") != "" {
			t.Fatalf("upstream headers %v", h)
		}
		f.checkUpstream(t)
	}
}

func TestResponsesStream(t *testing.T) {
	for _, chunk := range []int{1, 6, 100} {
		f := newFixture(t, chunk, nil)
		w := f.do("POST", "/llm/mock/v1/responses", `{"model":"m","stream":true,"input":"`+userText+`"}`, nil)
		var deltas, done, completed string
		for _, ev := range readSSE(t, w.Body.String()) {
			switch ev["type"] {
			case "response.output_text.delta":
				deltas += ev["delta"].(string)
			case "response.output_text.done":
				done = ev["text"].(string)
			case "response.completed":
				completed = ev["response"].(map[string]any)["output_text"].(string)
			}
		}
		want := "you said: " + userText
		if deltas != want || done != want || completed != want {
			t.Fatalf("chunk %d: deltas %q done %q completed %q", chunk, deltas, done, completed)
		}
		f.checkUpstream(t)
	}
}

func TestModelsPassthrough(t *testing.T) {
	f := newFixture(t, 0, nil)
	w := f.do("GET", "/llm/mock/v1/models?client=x", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "muse-spark-1.3") {
		t.Fatalf("models: %d %s", w.Code, w.Body)
	}
	if q, _ := f.up.last(); q != "client=x" {
		t.Fatalf("query %q not forwarded", q)
	}
	if w.Header().Get("X-Ratelimit-Remaining-Requests") != "2999" || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("response headers %v", w.Header())
	}
}

func TestRefusals(t *testing.T) {
	f := newFixture(t, 0, nil)
	ctx := context.Background()
	other, _, _ := f.tokens.Mint(ctx, "other provider", "meta", time.Hour)
	revoked, rl, _ := f.tokens.Mint(ctx, "revoked", "", time.Hour)
	_ = f.tokens.Revoke(ctx, rl.ID)
	chat := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	for _, tc := range []struct {
		name, method, path, body string
		hdr                      map[string]string
		status                   int
		want                     string
	}{
		{"unknown provider", "POST", "/llm/nope/v1/chat/completions", chat, nil, 404, "unknown provider"},
		{"unrouted path", "POST", "/llm/mock/v1/files", chat, nil, 404, "does not forward"},
		{"nested model path", "GET", "/llm/mock/v1/models/a/b", "", nil, 404, "does not forward"},
		{"wrong method", "GET", "/llm/mock/v1/chat/completions", "", nil, 405, "not allowed"},
		{"no token", "POST", "/llm/mock/v1/chat/completions", chat, map[string]string{"Authorization": ""}, 401, "missing launch token"},
		{"bad token", "POST", "/llm/mock/v1/chat/completions", chat, map[string]string{"Authorization": "Bearer dvl_nope"}, 401, "invalid launch token"},
		{"revoked", "POST", "/llm/mock/v1/chat/completions", chat, map[string]string{"Authorization": "Bearer " + revoked}, 401, "revoked"},
		{"other provider", "POST", "/llm/mock/v1/chat/completions", chat, map[string]string{"Authorization": "Bearer " + other}, 403, `for provider \"meta\"`},
		{"not json", "POST", "/llm/mock/v1/chat/completions", "hello", nil, 400, "must be one JSON value"},
		{"two values", "POST", "/llm/mock/v1/chat/completions", "{} {}", nil, 400, "must be one JSON value"},
	} {
		w := f.do(tc.method, tc.path, tc.body, tc.hdr)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want %d %q", tc.name, w.Code, w.Body, tc.status, tc.want)
		}
	}
	delete(f.keys, KeySecret("mock"))
	w := f.do("POST", "/llm/mock/v1/chat/completions", chat, nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "no API key") {
		t.Fatalf("no key: %d %s", w.Code, w.Body)
	}
	if n := len(f.up.seen); n != 0 {
		t.Fatalf("%d refused calls reached the provider", n)
	}
}

func TestBlockedNeverLeaves(t *testing.T) {
	p := redact.DefaultPolicy()
	p[redact.Secret] = redact.Block
	f := newFixture(t, 0, p)
	w := f.do("POST", "/llm/mock/v1/messages", `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":"`+userText+`"}]}`, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"type":"invalid_request_error"`) || !strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("blocked: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), fakeAWS) {
		t.Fatal("the block error repeats the value")
	}
	if len(f.up.seen) != 0 {
		t.Fatal("a blocked request reached the provider")
	}
}

func TestTooLargeAndUnreachable(t *testing.T) {
	f := newFixture(t, 0, nil)
	big := `{"x":"` + strings.Repeat("a", maxRequestBody) + `"}`
	if w := f.do("POST", "/llm/mock/v1/chat/completions", big, nil); w.Code != 413 {
		t.Fatalf("too large: %d", w.Code)
	}
	f.router.providers["mock"] = testProvider{base: "http://127.0.0.1:1"}
	if w := f.do("POST", "/llm/mock/v1/chat/completions", `{}`, nil); w.Code != 502 || !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatalf("unreachable: %d %s", w.Code, w.Body)
	}
}

func TestStructuralFieldsAndDataURIsUntouched(t *testing.T) {
	r, _ := redact.New(bytes.Repeat([]byte{9}, 32))
	s := r.NewSession(redact.DefaultPolicy())
	v, _ := decodeJSON([]byte(`{"id":"` + fakeAWS + `","messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + fakeAWS + `"}},{"type":"text","text":"` + fakeAWS + `"}]}],"n":12345678901234567890}`))
	out, findings, err := redactValue(v, s)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings %v err %v", findings, err)
	}
	b, _ := encodeJSON(out)
	got := string(b)
	if strings.Count(got, fakeAWS) != 2 || !strings.Contains(got, "12345678901234567890") {
		t.Fatalf("structural or data URI value changed, or a number lost precision: %s", got)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("empty options accepted")
	}
	f := newFixture(t, 0, nil)
	o := f.router.o
	o.Providers = []Provider{testProvider{}, testProvider{}}
	if _, err := New(o); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate provider: %v", err)
	}
}

func TestReadEventEdges(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(": comment\nevent: x\ndata: a\ndata: b\r\n\nid: 7\ndata: tail-without-blank"))
	ev, err := readEvent(br)
	if err != nil || ev.data != "a\nb" || len(ev.meta) != 2 {
		t.Fatalf("first: %+v %v", ev, err)
	}
	var b strings.Builder
	_ = ev.write(&b)
	if b.String() != ": comment\nevent: x\ndata: a\ndata: b\n\n" {
		t.Fatalf("write: %q", b.String())
	}
	ev, err = readEvent(br)
	if err != nil || ev.data != "tail-without-blank" {
		t.Fatalf("second: %+v %v", ev, err)
	}
	if _, err := readEvent(br); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
}
