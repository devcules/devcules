package router

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"strings"

	"github.com/devcules/devcules/internal/redact"
)

// sseEvent is one server-sent event. meta keeps every non-data line
// (event:, id:, retry:, comments) in order, so they pass through
// unchanged; data is the joined data payload.
type sseEvent struct {
	meta    []string
	data    string
	hasData bool
}

// readEvent reads one event; io.EOF with no lines read means the
// stream ended.
func readEvent(br *bufio.Reader) (sseEvent, error) {
	var ev sseEvent
	var data []string
	read := false
	for {
		line, err := br.ReadString('\n')
		if line == "" && err != nil {
			if read {
				ev.data = strings.Join(data, "\n")
				return ev, nil
			}
			return ev, err
		}
		read = true
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			ev.data = strings.Join(data, "\n")
			return ev, nil
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
			ev.hasData = true
			continue
		}
		ev.meta = append(ev.meta, line)
	}
}

func (ev sseEvent) write(w io.Writer) error {
	var b strings.Builder
	for _, m := range ev.meta {
		b.WriteString(m + "\n")
	}
	if ev.hasData {
		for _, d := range strings.Split(ev.data, "\n") {
			b.WriteString("data: " + d + "\n")
		}
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// streamField is one text field that arrives in pieces across events
// (a choice's content, a tool call's arguments, a content block's
// text). synth builds the event that carries a held tail when the
// field closes.
type streamField struct {
	r     *redact.StreamRestorer
	synth func(text string) sseEvent
}

// streamRestore restores placeholders in an SSE reply. Delta fields go
// through per-field streaming restorers; every other string in an
// event is whole, so it is restored in place. When a field closes, a
// held tail is sent as one extra delta event just before the closing
// event.
type streamRestore struct {
	s      *redact.Session
	fields map[string]*streamField
	order  []string
}

func newStreamRestore(s *redact.Session) *streamRestore {
	return &streamRestore{s: s, fields: map[string]*streamField{}}
}

// field returns the restorer for key, creating it with synth.
func (sr *streamRestore) field(key string, synth func(string) sseEvent) *streamField {
	f, ok := sr.fields[key]
	if !ok {
		f = &streamField{r: sr.s.Restorer(), synth: synth}
		sr.fields[key] = f
		sr.order = append(sr.order, key)
	}
	return f
}

// flush closes every field whose key starts with prefix ("" for all)
// and returns the events carrying their held tails.
func (sr *streamRestore) flush(prefix string) []sseEvent {
	var out []sseEvent
	kept := sr.order[:0]
	for _, k := range sr.order {
		if !strings.HasPrefix(k, prefix) {
			kept = append(kept, k)
			continue
		}
		f := sr.fields[k]
		delete(sr.fields, k)
		if tail := f.r.Flush(); tail != "" {
			out = append(out, f.synth(tail))
		}
	}
	sr.order = kept
	return out
}

// process turns one upstream event into the events to send on.
func (sr *streamRestore) process(ev sseEvent) []sseEvent {
	if !ev.hasData {
		return []sseEvent{ev}
	}
	if strings.TrimSpace(ev.data) == "[DONE]" {
		return append(sr.flush(""), ev)
	}
	v, err := decodeJSON([]byte(ev.data))
	obj, isObj := v.(map[string]any)
	if err != nil || !isObj {
		ev.data = sr.s.Restore(ev.data)
		return []sseEvent{ev}
	}
	var pre []sseEvent
	typ, _ := obj["type"].(string)
	switch {
	case obj["object"] == "chat.completion.chunk":
		pre = sr.chatChunk(obj)
	case strings.HasPrefix(typ, "response."):
		pre = sr.responsesEvent(ev, obj, typ)
	case anthropicEvents[typ]:
		pre = sr.anthropicEvent(obj, typ)
	default:
		restoreValue(obj, sr.s)
	}
	out, err := encodeJSON(obj)
	if err == nil {
		ev.data = string(out)
	}
	return append(pre, ev)
}

func dataEvent(meta []string, v any) sseEvent {
	out, _ := encodeJSON(v)
	return sseEvent{meta: meta, data: string(out), hasData: true}
}

func numKey(v any) string {
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return "0"
}

// chatChunk handles an OpenAI-style chat.completion.chunk: content,
// reasoning_content and tool call arguments stream per choice; a
// choice with a finish_reason closes its fields.
func (sr *streamRestore) chatChunk(obj map[string]any) []sseEvent {
	head := map[string]any{}
	for _, k := range []string{"id", "object", "created", "model", "system_fingerprint"} {
		if v, ok := obj[k]; ok {
			head[k] = v
		}
	}
	synth := func(index any, delta map[string]any) func(string) sseEvent {
		return func(text string) sseEvent {
			chunk := maps.Clone(head)
			d := fillTail(delta, text)
			chunk["choices"] = []any{map[string]any{"index": index, "delta": d, "finish_reason": nil}}
			return dataEvent(nil, chunk)
		}
	}
	var pre []sseEvent
	choices, _ := obj["choices"].([]any)
	for _, c := range choices {
		ch, ok := c.(map[string]any)
		if !ok {
			continue
		}
		idx := ch["index"]
		prefix := "c/" + numKey(idx) + "/"
		if delta, ok := ch["delta"].(map[string]any); ok {
			for _, name := range []string{"content", "reasoning_content"} {
				if s, ok := delta[name].(string); ok {
					f := sr.field(prefix+name, synth(idx, map[string]any{name: tailMarker}))
					delta[name] = f.r.Write(s)
				}
			}
			if s, ok := delta["refusal"].(string); ok {
				delta["refusal"] = sr.s.Restore(s)
			}
			calls, _ := delta["tool_calls"].([]any)
			for _, tc := range calls {
				call, ok := tc.(map[string]any)
				if !ok {
					continue
				}
				fn, ok := call["function"].(map[string]any)
				if !ok {
					continue
				}
				if s, ok := fn["arguments"].(string); ok {
					tidx := call["index"]
					f := sr.field(prefix+"tool/"+numKey(tidx), synth(idx, map[string]any{
						"tool_calls": []any{map[string]any{"index": tidx, "function": map[string]any{"arguments": tailMarker}}},
					}))
					fn["arguments"] = f.r.Write(s)
				}
			}
		}
		if msg, ok := ch["message"]; ok {
			restoreValue(msg, sr.s)
		}
		if fr, ok := ch["finish_reason"]; ok && fr != nil {
			pre = append(pre, sr.flush(prefix)...)
		}
	}
	return pre
}

// tailMarker stands where a synthetic event's held text goes.
const tailMarker = "\x00tail\x00"

// fillTail replaces tailMarker anywhere in v with text.
func fillTail(v any, text string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, c := range x {
			out[k] = fillTail(c, text)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, c := range x {
			out[i] = fillTail(c, text)
		}
		return out
	case string:
		if x == tailMarker {
			return text
		}
		return x
	default:
		return v
	}
}

var anthropicEvents = map[string]bool{
	"message_start": true, "content_block_start": true, "content_block_delta": true,
	"content_block_stop": true, "message_delta": true, "message_stop": true, "ping": true, "error": true,
}

// anthropicDeltaFields maps a Messages delta type to its text field.
var anthropicDeltaFields = map[string]string{
	"text_delta":       "text",
	"thinking_delta":   "thinking",
	"input_json_delta": "partial_json",
}

// anthropicEvent handles Anthropic-style Messages events: text,
// thinking and tool input stream per content block; content_block_stop
// closes the block, message_stop closes everything.
func (sr *streamRestore) anthropicEvent(obj map[string]any, typ string) []sseEvent {
	switch typ {
	case "content_block_delta":
		idx := obj["index"]
		delta, _ := obj["delta"].(map[string]any)
		dtype, _ := delta["type"].(string)
		name, streamed := anthropicDeltaFields[dtype]
		s, isString := delta[name].(string)
		if !streamed || !isString {
			return nil
		}
		f := sr.field("a/"+numKey(idx)+"/", func(text string) sseEvent {
			return dataEvent([]string{"event: content_block_delta"}, map[string]any{
				"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": dtype, name: text},
			})
		})
		delta[name] = f.r.Write(s)
		return nil
	case "content_block_stop":
		return sr.flush("a/" + numKey(obj["index"]) + "/")
	case "message_stop":
		return sr.flush("")
	default:
		restoreValue(obj, sr.s)
		return nil
	}
}

// responsesEvent handles OpenAI-style Responses events: any
// "<x>.delta" with a string delta streams per item and part, the
// matching "<x>.done" closes it (and carries the whole text, restored
// in place), and the terminal response events close everything.
func (sr *streamRestore) responsesEvent(ev sseEvent, obj map[string]any, typ string) []sseEvent {
	key := func(base string) string {
		return "r/" + base + "/" + fmtAny(obj["item_id"]) + "/" + numKey(obj["output_index"]) + "/" + numKey(obj["content_index"]) + "/"
	}
	if base, ok := strings.CutSuffix(typ, ".delta"); ok {
		if s, isString := obj["delta"].(string); isString {
			tmpl := maps.Clone(obj)
			meta := ev.meta
			f := sr.field(key(base), func(text string) sseEvent {
				out := maps.Clone(tmpl)
				out["delta"] = text
				return dataEvent(meta, out)
			})
			obj["delta"] = f.r.Write(s)
			return nil
		}
	}
	var pre []sseEvent
	if base, ok := strings.CutSuffix(typ, ".done"); ok {
		pre = sr.flush(key(base))
	}
	switch typ {
	case "response.completed", "response.failed", "response.incomplete":
		pre = append(pre, sr.flush("")...)
	}
	restoreValue(obj, sr.s)
	return pre
}

func fmtAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return numKey(v)
}

// pipeSSE copies an SSE body to w, restoring placeholders, flushing
// after every event so the client sees tokens as they arrive.
func pipeSSE(w http.ResponseWriter, body io.Reader, s *redact.Session) error {
	flusher, _ := w.(http.Flusher)
	sr := newStreamRestore(s)
	br := bufio.NewReaderSize(body, 64<<10)
	for {
		ev, err := readEvent(br)
		if errors.Is(err, io.EOF) {
			for _, tail := range sr.flush("") {
				if err := tail.write(w); err != nil {
					return err
				}
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		}
		if err != nil {
			return err
		}
		for _, out := range sr.process(ev) {
			if err := out.write(w); err != nil {
				return err
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
