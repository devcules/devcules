package router

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
)

type recTracer struct {
	mu    sync.Mutex
	calls []store.TraceCall
}

func (r *recTracer) Trace(ctx context.Context, c store.TraceCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *recTracer) only(t *testing.T) store.TraceCall {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) != 1 {
		t.Fatalf("%d traces, want 1", len(r.calls))
	}
	return r.calls[0]
}

// checkMasked proves no trace field holds the real key.
func checkMasked(t *testing.T, c store.TraceCall) {
	t.Helper()
	all := c.Question + c.Reply
	for _, tc := range c.ToolCalls {
		all += tc.Name + tc.Args
	}
	if strings.Contains(all, fakeAWS) {
		t.Fatalf("trace holds the real key: %+v", c)
	}
}

func TestTraceChatStream(t *testing.T) {
	f := newFixture(t, 4, nil)
	w := f.do("POST", "/llm/mock/v1/chat/completions", `{"model":"muse-spark-1.3","stream":true,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"`+userText+`"}]}`, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), fakeAWS) {
		t.Fatalf("status %d, want the restored stream: %s", w.Code, w.Body)
	}
	c := f.traces.only(t)
	checkMasked(t, c)
	if c.LaunchID == "" || c.Provider != "mock" || c.Path != "v1/chat/completions" || c.Model != "muse-spark-1.3" ||
		c.Status != 200 || !c.Stream || c.Redacted["SECRET"] != 1 {
		t.Fatalf("trace = %+v", c)
	}
	if !strings.HasPrefix(c.Question, "deploy with REDACTED_SECRET_") || !strings.HasPrefix(c.Reply, "you said: deploy with REDACTED_SECRET_") {
		t.Fatalf("question %q reply %q, want the placeholder versions", c.Question, c.Reply)
	}
	if len(c.ToolCalls) != 1 || !strings.Contains(c.ToolCalls[0].Args, "REDACTED_SECRET_") {
		t.Fatalf("tool calls %+v", c.ToolCalls)
	}
}

func TestTraceFastPathAndOtherStyles(t *testing.T) {
	f := newFixture(t, 3, nil)
	f.do("POST", "/llm/mock/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, nil)
	f.do("POST", "/llm/mock/v1/messages", `{"model":"m","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`, nil)
	f.do("POST", "/llm/mock/v1/messages", `{"model":"m","messages":[{"role":"user","content":"plain `+fakeAWS+`"}]}`, nil)
	f.do("POST", "/llm/mock/v1/responses", `{"model":"m","stream":true,"input":"yo"}`, nil)
	f.do("GET", "/llm/mock/v1/models", "", nil)
	want := []struct{ q, reply string }{
		{"hi", "you said: hi"},
		{"hello", ""}, // the mock's Anthropic stream echoes the string form only
		{"plain REDACTED_SECRET_", "you said: plain REDACTED_SECRET_"},
		{"yo", "you said: yo"},
		{"", ""},
	}
	f.traces.mu.Lock()
	defer f.traces.mu.Unlock()
	if len(f.traces.calls) != len(want) {
		t.Fatalf("%d traces, want %d", len(f.traces.calls), len(want))
	}
	for i, w := range want {
		c := f.traces.calls[i]
		checkMasked(t, c)
		if !strings.HasPrefix(c.Question, w.q) || !strings.HasPrefix(c.Reply, w.reply) {
			t.Errorf("call %d: question %q reply %q, want %q / %q", i, c.Question, c.Reply, w.q, w.reply)
		}
	}
}

func TestTraceSkipsUnauthenticatedAndKeepsBlockedEmpty(t *testing.T) {
	block := redact.DefaultPolicy()
	block[redact.Secret] = redact.Block
	f := newFixture(t, 0, block)
	f.do("POST", "/llm/mock/v1/chat/completions", `{}`, map[string]string{"Authorization": "Bearer nope"})
	if len(f.traces.calls) != 0 {
		t.Fatal("a call with a bad token was traced")
	}
	w := f.do("POST", "/llm/mock/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"`+userText+`"}]}`, nil)
	if w.Code != 400 {
		t.Fatalf("status %d, want the block", w.Code)
	}
	c := f.traces.only(t)
	if c.Status != 400 || c.Redacted["SECRET"] != 1 || c.Question != "" || c.Model != "" {
		t.Fatalf("blocked trace = %+v, want counts and no text", c)
	}
}

// observe runs raw provider bytes through an observer.
func observe(t *testing.T, stream bool, raw string) store.TraceCall {
	t.Helper()
	o := newObserver()
	if stream {
		// Byte at a time: events must survive any split.
		for i := range len(raw) {
			o.streamWrite([]byte{raw[i]})
		}
	} else {
		_, _ = o.Write([]byte(raw))
		o.finishPlain()
	}
	var c store.TraceCall
	o.record(&c)
	return c
}

func TestObserverShapes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stream  bool
		raw     string
		reply   string
		tools   string
		in, out int64
	}{
		{"chat json", false,
			`{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok","tool_calls":[{"id":"t","type":"function","function":{"name":"grep","arguments":"{\"q\":1}"}}]}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`,
			"ok", `grep{"q":1}`, 12, 3},
		{"anthropic json", false,
			`{"type":"message","content":[{"type":"text","text":"a"},{"type":"tool_use","id":"t","name":"read","input":{"p":"x"}}],"usage":{"input_tokens":7,"output_tokens":2}}`,
			"a", `read{"p":"x"}`, 7, 2},
		{"responses json", false,
			`{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"r"}]},{"type":"function_call","name":"ls","arguments":"{}"}],"usage":{"input_tokens":4,"output_tokens":1}}`,
			"r", `ls{}`, 4, 1},
		{"error json", false, `{"error":{"message":"rate limited","type":"rate_limit"}}`, "error: rate limited", "", 0, 0},
		{"not json", false, `<html>`, "", "", 0, 0},
		{"chat stream", true,
			"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"he\"}}]}\r\n\r\n" +
				"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\"\"}}]}}]}\n\n" +
				"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"y\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":2}\"}}]}}]}\n\n" +
				"data: {\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":5}}\n\n" +
				"data: [DONE]\n\n",
			"hey", `f{"a":2}`, 9, 5},
		{"anthropic stream", true,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":1}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"name\":\"bash\",\"input\":{}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"c\\\":\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"1}\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":15}}\n\n" +
				": keep-alive\n\n",
			"hi", `bash{"c":1}`, 20, 15},
		{"responses stream", true,
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n" +
				"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"name\":\"run\"}}\n\n" +
				"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n",
			"x", `run{}`, 3, 2},
		{"stream error", true, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\ndata: not json\n\n", "error: busy", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := observe(t, tc.stream, tc.raw)
			var tools string
			for _, x := range c.ToolCalls {
				tools += x.Name + x.Args
			}
			if c.Reply != tc.reply || tools != tc.tools || c.InputTokens != tc.in || c.OutputTokens != tc.out {
				t.Fatalf("got reply %q tools %q tokens %d/%d", c.Reply, tools, c.InputTokens, c.OutputTokens)
			}
		})
	}
}

func TestObserverRequestShapes(t *testing.T) {
	for _, tc := range []struct {
		body, want string
	}{
		{`{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"x"},{"role":"user","content":[{"type":"tool_result","content":"out"}]}]}`, "first"},
		{`{"input":[{"role":"user","content":[{"type":"input_text","text":"a"},{"type":"input_image","image_url":"data:x"},{"type":"input_text","text":"b"}]}]}`, "a\nb"},
		{`{"input":"plain"}`, "plain"},
		{`[1,2]`, ""},
	} {
		v, err := decodeJSON([]byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		o := newObserver()
		o.request(v)
		if o.question != tc.want {
			t.Errorf("%s: question %q, want %q", tc.body, o.question, tc.want)
		}
	}
}

func TestObserverCaps(t *testing.T) {
	o := newObserver()
	big := strings.Repeat("x", maxObserved/2+1)
	_, _ = o.Write([]byte(big))
	_, _ = o.Write([]byte(big))
	if !o.overflow || o.body != nil {
		t.Fatal("an oversized plain reply was kept")
	}
	o.finishPlain()
	o.streamWrite([]byte(big))
	o.streamWrite([]byte(big))
	if o.pending != nil {
		t.Fatal("an endless event was kept")
	}
	appendCapped(&o.reply, big)
	appendCapped(&o.reply, big)
	if o.reply.Len() != len(big) {
		t.Fatalf("reply grew to %d", o.reply.Len())
	}
}
