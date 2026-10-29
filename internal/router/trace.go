package router

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/devcules/devcules/internal/store"
)

// Tracer receives one record per call that passed the token check.
// internal/trace is the real one. Trace runs after the reply is done
// and must not block for long.
type Tracer interface {
	Trace(ctx context.Context, c store.TraceCall)
}

// maxObserved caps how much reply the observer keeps to parse. A
// bigger plain JSON reply is traced without its content.
const maxObserved = 4 << 20

// observer collects what a trace keeps from one call: the model, the
// question, the reply text, tool calls and token counts. It reads the
// redacted request and the provider's own reply bytes, so everything
// it holds carries placeholders, never restored values.
type observer struct {
	model    string
	question string
	reply    strings.Builder
	tools    []*toolCall
	byIndex  map[string]*toolCall
	in, out  int64
	errText  string
	pending  []byte // stream bytes not yet split into events
	body     []byte // a plain reply, kept up to maxObserved
	overflow bool
}

type toolCall struct {
	name string
	args strings.Builder
}

func newObserver() *observer {
	return &observer{byIndex: map[string]*toolCall{}}
}

// request reads the model and the last user text from the redacted
// request body.
func (o *observer) request(v any) {
	m, _ := v.(map[string]any)
	o.model, _ = m["model"].(string)
	if msgs, ok := m["messages"].([]any); ok {
		o.question = lastUserText(msgs)
		return
	}
	switch in := m["input"].(type) {
	case string:
		o.question = in
	case []any:
		o.question = lastUserText(in)
	}
}

// lastUserText is the text of the last user message that has any:
// a message of only tool results is the agent talking, not the user.
func lastUserText(items []any) string {
	for i := len(items) - 1; i >= 0; i-- {
		msg, _ := items[i].(map[string]any)
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}
		if t := partsText(msg["content"]); t != "" {
			return t
		}
	}
	return ""
}

// partsText joins the text of a content value: a string, or parts of
// type text, input_text or output_text.
func partsText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			pm, _ := p.(map[string]any)
			switch pm["type"] {
			case "text", "input_text", "output_text":
				if t, _ := pm["text"].(string); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Write takes a copy of the reply as it streams past.
func (o *observer) Write(p []byte) (int, error) {
	if !o.overflow {
		if len(o.body)+len(p) > maxObserved {
			o.overflow, o.body = true, nil
		} else {
			o.body = append(o.body, p...)
		}
	}
	return len(p), nil
}

// streamWrite splits streamed bytes into events and reads each one.
// It keeps only the bytes of an unfinished event.
func (o *observer) streamWrite(p []byte) {
	o.pending = append(o.pending, bytes.ReplaceAll(p, []byte("\r"), nil)...)
	for {
		i := bytes.Index(o.pending, []byte("\n\n"))
		if i < 0 {
			if len(o.pending) > maxObserved {
				o.pending = nil
			}
			return
		}
		o.event(o.pending[:i])
		o.pending = o.pending[i+2:]
	}
}

// streamWriter adapts streamWrite to io.Writer for a tee.
type streamWriter struct{ o *observer }

func (s streamWriter) Write(p []byte) (int, error) {
	s.o.streamWrite(p)
	return len(p), nil
}

// event reads one SSE event's data as JSON.
func (o *observer) event(raw []byte) {
	var data []string
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
		}
	}
	joined := strings.Join(data, "\n")
	if joined == "" || joined == "[DONE]" {
		return
	}
	v, err := decodeJSON([]byte(joined))
	if err != nil {
		return
	}
	obj, _ := v.(map[string]any)
	if obj == nil {
		return
	}
	typ, _ := obj["type"].(string)
	switch {
	case obj["object"] == "chat.completion.chunk":
		o.chatChunk(obj)
	case typ == "message_start":
		msg, _ := obj["message"].(map[string]any)
		o.usage(msg["usage"])
	case typ == "content_block_start":
		block, _ := obj["content_block"].(map[string]any)
		if block["type"] == "tool_use" {
			o.tool("a"+numKey(obj["index"]), block["name"])
		}
	case typ == "content_block_delta":
		d, _ := obj["delta"].(map[string]any)
		switch d["type"] {
		case "text_delta":
			o.text(d["text"])
		case "input_json_delta":
			if t := o.byIndex["a"+numKey(obj["index"])]; t != nil {
				s, _ := d["partial_json"].(string)
				appendCapped(&t.args, s)
			}
		}
	case typ == "message_delta":
		o.usage(obj["usage"])
	case typ == "response.output_text.delta":
		o.text(obj["delta"])
	case typ == "response.output_item.added":
		item, _ := obj["item"].(map[string]any)
		if item["type"] == "function_call" {
			o.tool("r"+numKey(obj["output_index"]), item["name"])
		}
	case typ == "response.function_call_arguments.delta":
		if t := o.byIndex["r"+numKey(obj["output_index"])]; t != nil {
			s, _ := obj["delta"].(string)
			appendCapped(&t.args, s)
		}
	case typ == "response.completed", typ == "response.incomplete", typ == "response.failed":
		resp, _ := obj["response"].(map[string]any)
		o.usage(resp["usage"])
	case typ == "error" || obj["error"] != nil:
		o.errorText(obj)
	}
}

func (o *observer) chatChunk(obj map[string]any) {
	o.usage(obj["usage"])
	choices, _ := obj["choices"].([]any)
	for _, c := range choices {
		cm, _ := c.(map[string]any)
		if numKey(cm["index"]) != "0" && cm["index"] != nil {
			continue
		}
		d, _ := cm["delta"].(map[string]any)
		o.text(d["content"])
		calls, _ := d["tool_calls"].([]any)
		for _, tc := range calls {
			tm, _ := tc.(map[string]any)
			fn, _ := tm["function"].(map[string]any)
			k := "c" + numKey(tm["index"])
			t := o.byIndex[k]
			if t == nil {
				t = o.tool(k, fn["name"])
			}
			s, _ := fn["arguments"].(string)
			appendCapped(&t.args, s)
		}
	}
}

// tool starts a tool call under a stream-local key.
func (o *observer) tool(key string, name any) *toolCall {
	n, _ := name.(string)
	t := &toolCall{name: n}
	o.tools = append(o.tools, t)
	o.byIndex[key] = t
	return t
}

func (o *observer) text(v any) {
	if s, ok := v.(string); ok {
		appendCapped(&o.reply, s)
	}
}

// appendCapped grows b up to maxObserved and then stops: the trace
// store truncates far below that anyway.
func appendCapped(b *strings.Builder, s string) {
	if b.Len()+len(s) <= maxObserved {
		b.WriteString(s)
	}
}

// usage reads token counts in either naming; later counts win, since
// streams report running totals.
func (o *observer) usage(v any) {
	u, _ := v.(map[string]any)
	if u == nil {
		return
	}
	for _, k := range []string{"prompt_tokens", "input_tokens"} {
		if n, ok := toInt(u[k]); ok && n > 0 {
			o.in = n
		}
	}
	for _, k := range []string{"completion_tokens", "output_tokens"} {
		if n, ok := toInt(u[k]); ok && n > 0 {
			o.out = n
		}
	}
}

func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), true
	}
	return 0, false
}

// errorText keeps a provider error's message (never its request).
func (o *observer) errorText(obj map[string]any) {
	e, _ := obj["error"].(map[string]any)
	if msg, _ := e["message"].(string); msg != "" {
		o.errText = msg
	}
}

// finishPlain parses a non-streamed reply kept by Write.
func (o *observer) finishPlain() {
	if o.overflow || len(o.body) == 0 {
		return
	}
	v, err := decodeJSON(o.body)
	o.body = nil
	if err != nil {
		return
	}
	obj, _ := v.(map[string]any)
	if obj == nil {
		return
	}
	if obj["error"] != nil {
		o.errorText(obj)
		return
	}
	o.usage(obj["usage"])
	// Chat Completions.
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		c, _ := choices[0].(map[string]any)
		msg, _ := c["message"].(map[string]any)
		o.text(msg["content"])
		calls, _ := msg["tool_calls"].([]any)
		for _, tc := range calls {
			tm, _ := tc.(map[string]any)
			fn, _ := tm["function"].(map[string]any)
			t := o.tool("", fn["name"])
			s, _ := fn["arguments"].(string)
			appendCapped(&t.args, s)
		}
		return
	}
	// Anthropic Messages.
	if blocks, ok := obj["content"].([]any); ok {
		for _, b := range blocks {
			bm, _ := b.(map[string]any)
			switch bm["type"] {
			case "text":
				o.text(bm["text"])
			case "tool_use":
				t := o.tool("", bm["name"])
				if raw, err := encodeJSON(bm["input"]); err == nil {
					appendCapped(&t.args, string(raw))
				}
			}
		}
		return
	}
	// Responses.
	items, _ := obj["output"].([]any)
	for _, it := range items {
		im, _ := it.(map[string]any)
		switch im["type"] {
		case "message":
			o.text(partsText(im["content"]))
		case "function_call":
			t := o.tool("", im["name"])
			s, _ := im["arguments"].(string)
			appendCapped(&t.args, s)
		}
	}
}

// record fills the content fields of c.
func (o *observer) record(c *store.TraceCall) {
	c.Model = o.model
	c.Question = o.question
	c.Reply = o.reply.String()
	if c.Reply == "" && o.errText != "" {
		c.Reply = "error: " + o.errText
	}
	c.InputTokens, c.OutputTokens = o.in, o.out
	for _, t := range o.tools {
		c.ToolCalls = append(c.ToolCalls, store.TraceToolCall{Name: t.name, Args: t.args.String()})
	}
}
