package assistants

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/mcp"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// scriptedModel is a tiny inline Script: each call pops one reply.
type scriptedModel struct {
	mu    sync.Mutex
	reply []ModelReply
	seen  []ChatRequest
}

func (m *scriptedModel) Chat(_ context.Context, req ChatRequest) (ModelReply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen = append(m.seen, req)
	if len(m.reply) == 0 {
		return ModelReply{Text: "(no script left)"}, nil
	}
	r := m.reply[0]
	m.reply = m.reply[1:]
	return r, nil
}

// testLoop wires a Loop over the in-memory model and gate.
func testLoop(t *testing.T, model *scriptedModel, gate func(context.Context, ToolCallAsk) GateResult) *Loop {
	t.Helper()
	l, err := NewLoop("builtin", Options{
		Model: model,
		Tools: func() []mcp.Tool {
			return []mcp.Tool{{Name: "status", InputSchema: json.RawMessage(`{"type":"object"}`)}}
		},
		Gate: gate,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestScriptedApprovalFlow is the M5.1 issue's gate: a scripted
// conversation runs against a mock model while every tool call passes
// through a real approvals.Service. The critical rule must pause, a
// phone-side Allow must be what runs the call, and a Deny must reach
// the model as a refusal it explains.
func TestScriptedApprovalFlow(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc, err := approvals.New(ctx, approvals.Options{Store: st, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	// "Ask for everything": the assistant's runner target asks each time.
	if err := svc.SetMode(ctx, approvals.Target("builtin"), approvals.ModeAll, "test", ""); err != nil {
		t.Fatal(err)
	}

	model := &scriptedModel{reply: []ModelReply{
		{ToolCalls: []ToolCallAsk{{ID: "c1", Name: "status", Args: json.RawMessage(`{}`)}}},
		{Text: "Status is up; that call was approved by the owner."},
	}}
	var ran []string
	run := func(_ context.Context, ask ToolCallAsk) (mcp.Result, error) {
		ran = append(ran, ask.Name)
		return mcp.Result{Text: "serve up"}, nil
	}
	loop := testLoop(t, model, ApprovalsGate(PolicyOptions{
		Decider: svc, Runner: "builtin", Agent: "builtin", LaunchID: "assist",
	}, run))

	// Answer any approval allow-once from a "phone" goroutine.
	// One controlled answerer drains pending continuously against a
	// single decision variable the phases flip; there is never a
	// second goroutine able to answer the same approval, so the allow
	// and deny phases cannot collide.
	var decide atomic.Bool
	decide.Store(true)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				pending, err := svc.List(ctx, "pending", 10)
				if err != nil {
					continue
				}
				for _, p := range pending {
					_, _ = svc.Answer(ctx, p.ID, decide.Load(), "owner", "")
				}
			}
		}
	}()

	_, evs, err := loop.Chat(ctx, "", "what is running?")
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "status" {
		t.Fatalf("ran = %v", ran)
	}
	var sawAsk bool
	for _, e := range evs {
		if e.Type == EventTool {
			if e.Tool.Decision != string(approvals.Ask) || e.Tool.ApprovedBy != "owner" {
				t.Fatalf("tool event = %+v", e.Tool)
			}
			sawAsk = true
		}
	}
	if !sawAsk {
		t.Fatalf("no tool event in %+v", evs)
	}

	// Deny the next one: the model must hear the refusal and explain.
	model.reply = []ModelReply{
		{ToolCalls: []ToolCallAsk{{ID: "c2", Name: "status", Args: json.RawMessage(`{}`)}}},
		{Text: "The owner denied that, so I did not run it."},
	}
	decide.Store(false)
	_, evs, err = loop.Chat(ctx, loopTurnSession(t, loop), "try it again")
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Fatalf("the denied call still ran: %v", ran)
	}
	last := evs[len(evs)-1]
	if last.Type != EventMessage || !strings.Contains(last.Text, "denied") {
		t.Fatalf("last = %+v", last)
	}
}

// loopTurnSession returns the one session the loop currently holds, in
// tests where only one conversation exists.
func loopTurnSession(t *testing.T, l *Loop) string {
	t.Helper()
	ms, ok := l.o.Convos.(*MemorySessions)
	if !ok {
		t.Fatal("loop not on MemorySessions")
	}
	all := ms.ListAll()
	if len(all) != 1 {
		t.Fatalf("sessions = %d", len(all))
	}
	return all[0].ID
}

// TestMaxTurnsGuard: a model that only asks for tools ends with the
// guard message and runs exactly MaxTurns calls.
func TestMaxTurnsGuard(t *testing.T) {
	model := &scriptedModel{}
	for i := 0; i < MaxTurns+3; i++ {
		model.reply = append(model.reply, ModelReply{
			ToolCalls: []ToolCallAsk{{ID: "x", Name: "status", Args: json.RawMessage(`{}`)}}})
	}
	var n int
	loop := testLoop(t, model, func(context.Context, ToolCallAsk) GateResult {
		n++
		return GateResult{Decision: "allow", Result: mcp.Result{Text: "up"}}
	})
	_, evs, err := loop.Chat(context.Background(), "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if n != MaxTurns {
		t.Fatalf("calls = %d, want MaxTurns=%d", n, MaxTurns)
	}
	last := evs[len(evs)-1]
	if last.Type != EventMessage || !strings.Contains(last.Text, "stopped") {
		t.Fatalf("guard message = %+v", last)
	}
}

// TestPlaceholderArgumentRefused: a redaction placeholder in the
// arguments never reaches the gate.
func TestPlaceholderArgumentRefused(t *testing.T) {
	model := &scriptedModel{reply: []ModelReply{
		{ToolCalls: []ToolCallAsk{{ID: "c1", Name: "status", Args: json.RawMessage(`{"note":"REDACTED_SECRET_0123ABCDEF"}`)}}},
		{Text: "I cannot use a masked value."},
	}}
	var gated int
	loop := testLoop(t, model, func(context.Context, ToolCallAsk) GateResult {
		gated++
		return GateResult{Decision: "allow", Result: mcp.Result{Text: "up"}}
	})
	_, evs, err := loop.Chat(context.Background(), "", "pass REDACTED_SECRET_0123ABCDEF along")
	if err != nil {
		t.Fatal(err)
	}
	if gated != 0 {
		t.Fatalf("the gate saw a placeholder call")
	}
	if evs[0].Tool.Decision != "deny" || !strings.Contains(evs[0].Tool.Result, "placeholder") {
		t.Fatalf("event = %+v", evs[0].Tool)
	}
}

// TestBlockedMessage: a block-mode finding in the user message rejects
// the turn before the model is called.
func TestBlockedMessage(t *testing.T) {
	r, err := redact.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	policy := redact.DefaultPolicy()
	policy[redact.Secret] = redact.Block
	model := &scriptedModel{reply: []ModelReply{{Text: "unreached"}}}
	loop := testLoop(t, model, func(context.Context, ToolCallAsk) GateResult {
		return GateResult{Decision: "deny", Result: mcp.Result{Text: "x", IsError: true}}
	})
	loop.red = &SessionRedact{s: r.NewSession(policy)}
	// A bare AWS key trips aws-access-token; keys ending "EXAMPLE" are
	// allowlisted by the gitleaks rules, which is why this one differs.
	// Built from parts, so no file holds a whole key shape.
	_, _, err = loop.Chat(context.Background(), "", "my key is "+"AKIA"+"2ZVWXTYPLMNBVCXZ")
	if err == nil {
		t.Fatal("a block-mode secret reached the model")
	}
	if msg, ok := Blocked(err); !ok || msg == "" {
		t.Fatalf("err = %v", err)
	}
	if len(model.seen) != 0 {
		t.Fatal("the model was called on a blocked message")
	}
}

// TestCompactionKeepsApprovalArgs builds a conversation long enough to
// compact, then proves a fresh turn's tool call is gated with the
// exact arguments the model just asked (compaction runs only between
// turns with Pending empty, so the asked arguments are those).
func TestCompactionKeepsApprovalArgs(t *testing.T) {
	model := &scriptedModel{}
	var got []string
	loop := testLoop(t, model, func(_ context.Context, ask ToolCallAsk) GateResult {
		got = append(got, string(ask.Args))
		return GateResult{Decision: "allow", Result: mcp.Result{Text: "ok"}}
	})
	// Drive text-only turns until Msgs passes the compaction trigger:
	// each turn appends user+assistant, so maxMessages/2 needs this
	// many Chats.
	firstID := ""
	for i := 0; i < maxMessages/4+4; i++ {
		model.reply = append(model.reply, ModelReply{Text: "ok"})
		_, evs, err := loop.Chat(context.Background(), firstID, "say ok")
		if err != nil {
			t.Fatal(err)
		}
		if firstID == "" {
			firstID = loopTurnSession(t, loop)
		}
		_ = evs
	}
	ms := loop.o.Convos.(*MemorySessions)
	c, err := ms.Get(firstID)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Msgs) >= maxMessages/2 {
		t.Fatalf("msgs=%d: compaction never ran", len(c.Msgs))
	}
	model.reply = append(model.reply, ModelReply{
		ToolCalls: []ToolCallAsk{{ID: "keep-me", Name: "status", Args: json.RawMessage(`{"target":"barnacle-01"}`)}}})
	if _, _, err := loop.Chat(context.Background(), firstID, "check barnacle"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `{"target":"barnacle-01"}` {
		t.Fatalf("gate saw %v", got)
	}
}

// TestSessionResume mints a session, then continues it by id.
func TestSessionResume(t *testing.T) {
	model := &scriptedModel{reply: []ModelReply{{Text: "one"}, {Text: "two"}}}
	loop := testLoop(t, model, func(context.Context, ToolCallAsk) GateResult {
		return GateResult{Decision: "deny", Result: mcp.Result{Text: "x", IsError: true}}
	})
	_, evs, err := loop.Chat(context.Background(), "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Text != "one" {
		t.Fatalf("evs = %+v", evs)
	}
	ms := loop.o.Convos.(*MemorySessions)
	id := ms.ListAll()[0].ID
	_, evs, err = loop.Chat(context.Background(), id, "again")
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Text != "two" {
		t.Fatalf("evs = %+v", evs)
	}
	s, err := loop.Session(id)
	if err != nil || len(s.Messages) != 4 {
		t.Fatalf("session = %+v err=%v", s, err)
	}
}
