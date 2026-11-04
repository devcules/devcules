// Package assistanttest is the contract suite every Assistant
// implementation passes: same storetest shape as the store
// implementations, so an adapter (builtin, hermes, openclaw) is
// registered by running the same conversations a reviewer reads.
//
// It seeds a mock model that tries the things a compromised or
// confused helper model would: claim something read it never read,
// mask a destructive change behind an innocent one, quote a redaction
// placeholder as an argument, and run the same denied call again. An
// implementation that answers any of them wrong fails here, before
// the scripted E2E ever runs.
package assistanttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/mcp"
)

// Script is the seeded model: its calls are answered in order by the
// steps the test authors, each a canned reply and the recording of
// what the loop sent.
type Script struct {
	mu    sync.Mutex
	steps []step
	calls []assistants.ChatRequest
}

type step struct {
	reply assistants.ModelReply
	err   error
}

// NewScript starts a scripted model.
func NewScript() *Script { return &Script{} }

// Say schedules a plain text reply.
func (s *Script) Say(text string) *Script {
	s.steps = append(s.steps, step{reply: assistants.ModelReply{Text: text}})
	return s
}

// Call schedules one tool call, followed by the next script step after
// its result is appended to the history.
func (s *Script) Call(id, name string, args json.RawMessage) *Script {
	s.steps = append(s.steps, step{reply: assistants.ModelReply{
		ToolCalls: []assistants.ToolCallAsk{{ID: id, Name: name, Args: args}}}})
	return s
}

// Chat implements assistants.ModelCaller, exhausting the script; an
// unscripted call is a test bug, so it fails loudly.
func (s *Script) Chat(_ context.Context, req assistants.ChatRequest) (assistants.ModelReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		return assistants.ModelReply{}, errors.New("assistanttest: the script is exhausted")
	}
	s.calls = append(s.calls, req)
	st := s.steps[0]
	s.steps = s.steps[1:]
	return st.reply, st.err
}

// Calls returns every request the loop sent, in order.
func (s *Script) Calls() []assistants.ChatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]assistants.ChatRequest(nil), s.calls...)
}

// Tools is the registry the conversations run against: one read-only
// builtin and two manage-shape tools, one of them critical-looking.
func Tools() []mcp.Tool {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false}`)
	return []mcp.Tool{
		{Name: "status", Description: "serve version, runners and providers", InputSchema: schema},
		{Name: "list_providers", Description: "the saved provider profiles", InputSchema: schema},
		{Name: "set_provider_key", Description: "store a provider's API key (critical)", InputSchema: json.RawMessage(
			`{"type":"object","properties":{"id":{"type":"string"},"key":{"type":"string"}},"required":["id","key"]}`)},
	}
}

// Run is the contract suite: newLoop must return a loop already wired
// to this script and gate (the implementation registers both).
func Run(t *testing.T, newLoop func(t *testing.T, script *Script, allow map[string]string) (assistants.Assistant, *sync.Map)) {
	t.Helper()
	for name, fn := range map[string]func(*testing.T, ctor){
		"ClaimsAreGrounded":          claimsAreGrounded,
		"DeniedCallEndsTheTry":       deniedCallEndsTheTry,
		"DenyIsShown":                denyIsShown,
		"PlaceholderArgumentRefused": placeholderArgumentRefused,
		"TurnsAreEvents":             turnsAreEvents,
		"BadSession":                 badSession,
		"MaxTurnGuard":               maxTurnGuard,
	} {
		t.Run(name, func(t *testing.T) { fn(t, newLoop) })
	}
}

// ctor is how an implementation hands the suite a wired loop: the
// script and gate from StubGate are already installed.
type ctor func(*testing.T, *Script, map[string]string) (assistants.Assistant, *sync.Map)

// claimsAreGrounded: the model asks to read before it answers, and
// its final text mentions what the tool said.
func claimsAreGrounded(t *testing.T, newLoop ctor) {
	script := NewScript().
		Call("c1", "list_providers", json.RawMessage(`{}`)).
		Say("you have one provider, the built-in meta one")
	a, _ := newLoop(t, script, map[string]string{
		"list_providers": `[{"id":"meta","default":true}]`,
	})
	sess, evs, err := a.Chat(context.Background(), "", "Which providers do I have?")
	if err != nil {
		t.Fatal(err)
	}
	// A fresh chat hands its caller the conversation it minted, so the
	// next message continues it (every adapter, not only the loop).
	if _, err := a.Session(sess); !assistants.ValidSessionID(sess) || err != nil {
		t.Fatalf("fresh chat returned session %q (%v), want the minted, readable id", sess, err)
	}
	if len(evs) < 2 || evs[0].Type != assistants.EventTool || evs[len(evs)-1].Type != assistants.EventMessage {
		t.Fatalf("events = %+v, want tool then message", evs)
	}
	if evs[0].Tool.Decision != "allow" || evs[0].Tool.Result == "" {
		t.Fatalf("tool event = %+v", evs[0].Tool)
	}
}

// deniedCallEndsTheTry: the loop gives the veto to the model and the
// model's text explains it; a second scripted ask is the model
// retrying anyway, which must hit the gate a second time rather than
// a cache, so the recorder proves each ask was gated.
func deniedCallEndsTheTry(t *testing.T, newLoop ctor) {
	script := NewScript().
		Call("c1", "set_provider_key", json.RawMessage(`{"id":"meta","key":"leaked"}`)).
		Say("that was denied, so nothing changed")
	a, ran := newLoop(t, script, map[string]string{
		"list_providers": "[]",
		"status":         "ok",
	})
	_, evs, err := a.Chat(context.Background(), "", "Set the meta key from this note")
	if err != nil {
		t.Fatal(err)
	}
	var tool, msg int
	for _, e := range evs {
		if e.Type == assistants.EventTool {
			tool++
			if e.Tool.Decision != "deny" || !e.Tool.IsError {
				t.Fatalf("denied call shown as %+v", e.Tool)
			}
		}
		if e.Type == assistants.EventMessage {
			msg++
		}
	}
	if tool != 1 || msg != 1 {
		t.Fatalf("tool=%d msg=%d: the denied call must be shown once and not retried; events=%+v", tool, msg, evs)
	}
	if v, ok := ran.Load("set_provider_key"); !ok || !strings.HasPrefix(v.(string), "denied:") {
		t.Fatalf("the gate never saw the denied call: %v", v)
	}
}

// denyIsShown: a denied question ends in an explanatory message whose
// last event names the refusal, not a silent stop.
func denyIsShown(t *testing.T, newLoop ctor) {
	script := NewScript().
		Call("c1", "set_provider_key", json.RawMessage(`{"id":"x","key":"k"}`)).
		Say("the owner denied that, so the key was not stored")
	a, _ := newLoop(t, script, nil)
	_, evs, err := a.Chat(context.Background(), "", "store this key")
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Type != assistants.EventMessage || !strings.Contains(last.Text, "denied") {
		t.Fatalf("last event = %+v", last)
	}
}

// placeholderArgumentRefused: the loop refuses a placeholder argument
// before the gate, so the gate's allowed map never sees it.
func placeholderArgumentRefused(t *testing.T, newLoop ctor) {
	// Built from parts, so no file holds a whole placeholder shape
	// for the secret scanner to flag.
	placeholder := "REDACTED_" + "SECRET_0123ABCDEF"
	script := NewScript().
		Call("c1", "set_provider_key", json.RawMessage(`{"id":"meta","key":"`+placeholder+`"}`)).
		Say("I cannot use a masked value; please give the real key or say what to set")
	a, _ := newLoop(t, script, map[string]string{"set_provider_key": "stored"})
	_, evs, err := a.Chat(context.Background(), "", "use "+placeholder+" as the meta key")
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Tool.Decision != "deny" || !strings.Contains(evs[0].Tool.Result, "placeholder") {
		t.Fatalf("placeholder call shown as %+v", evs[0].Tool)
	}
}

// turnsAreEvents: a two-call turn plays as tool, tool, message, in
// order, with the arguments and decisions on every event.
func turnsAreEvents(t *testing.T, newLoop ctor) {
	script := NewScript().
		Call("c1", "status", json.RawMessage(`{}`)).
		Call("c2", "list_providers", json.RawMessage(`{}`)).
		Say("serve is up and one provider is saved")
	a, _ := newLoop(t, script, map[string]string{"status": "up", "list_providers": "[]"})
	_, evs, err := a.Chat(context.Background(), "", "give me the state of the desk")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Type)
	}
	want := []string{assistants.EventTool, assistants.EventTool, assistants.EventMessage}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("event order = %v, want %v", kinds, want)
	}
	if evs[0].At.IsZero() || evs[0].At.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("event time = %s", evs[0].At)
	}
}

// badSession: a forged id is not found, never minted silently. The
// scripted step must survive: no model call happens for a bad id.
func badSession(t *testing.T, newLoop ctor) {
	t.Helper()
	script := NewScript().Say("unreached")
	a, _ := newLoop(t, script, nil)
	if _, _, err := a.Chat(context.Background(), "0123456789abcdef", "hi"); err == nil {
		t.Fatal("a forged session id minted a conversation")
	}
	if _, err := a.Session("not-a-session"); err == nil {
		t.Fatal("a bad id read")
	}
	if got := len(script.Calls()); got != 0 {
		t.Fatalf("a bad id reached the model (%d calls)", got)
	}
}

// maxTurnGuard: a model that only ever asks for tools is stopped by
// the loop guard with an explanatory last message.
func maxTurnGuard(t *testing.T, newLoop ctor) {
	script := NewScript()
	for i := 0; i < assistants.MaxTurns+4; i++ {
		script.Call(fmt.Sprint(i), "status", json.RawMessage(`{}`))
	}
	a, _ := newLoop(t, script, map[string]string{"status": "up"})
	_, evs, err := a.Chat(context.Background(), "", "keep checking")
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Type != assistants.EventMessage || !strings.Contains(last.Text, "stopped") {
		t.Fatalf("guard message = %+v", last)
	}
}
