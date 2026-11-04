package assistants

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/mcp"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

type named struct {
	id string
	Assistant
}

func (n named) ID() string { return n.id }

func TestRegistry(t *testing.T) {
	if _, err := NewRegistry(named{id: "Bad Id"}); err == nil {
		t.Fatal("a bad id was registered")
	}
	if _, err := NewRegistry(named{id: "hermes"}, named{id: "hermes"}); err == nil {
		t.Fatal("a duplicate id was registered")
	}
	r, err := NewRegistry(named{id: "openclaw"}, named{id: "builtin"}, named{id: "hermes"})
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := r.Get("hermes"); !ok || a.ID() != "hermes" {
		t.Fatal("Get missed a registered assistant")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get found an unknown id")
	}
	var ids []string
	for _, a := range r.List() {
		ids = append(ids, a.ID())
	}
	if strings.Join(ids, ",") != "builtin,hermes,openclaw" {
		t.Fatalf("List = %v, want id order", ids)
	}
	if !ValidAssistant("builtin") || ValidAssistant("") || ValidAssistant("9x") {
		t.Fatal("ValidAssistant")
	}
}

func TestTrimKeepsRunes(t *testing.T) {
	if Trim("short") != "short" {
		t.Fatal("a short string changed")
	}
	long := strings.Repeat("a", 399) + "é" + strings.Repeat("b", 50)
	got := Trim(long)
	if len(got) > 400 || !utf8.ValidString(got) || !strings.HasPrefix(got, strings.Repeat("a", 399)) {
		t.Fatalf("Trim cut mid-rune or too long: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestFreshChat(t *testing.T) {
	loop := testLoop(t, &scriptedModel{reply: []ModelReply{{Text: "hi"}, {Text: "again"}}}, func(context.Context, ToolCallAsk) GateResult {
		return GateResult{Decision: "deny", Result: mcp.Result{Text: "x", IsError: true}}
	})
	id, _, err := loop.Chat(context.Background(), "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSessionID(id) {
		t.Fatalf("fresh Chat returned id %q", id)
	}
	if _, err := loop.Session(id); err != nil {
		t.Fatalf("the fresh conversation is not readable: %v", err)
	}
	// Continuing a conversation hands back the same id.
	if again, _, err := loop.Chat(context.Background(), id, "more"); err != nil || again != id {
		t.Fatalf("continued Chat = %q, %v; want %q", again, err, id)
	}
}

func TestNewSessionRedact(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, _ := redact.New(make([]byte, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	red := NewSessionRedact(r, settings)
	fake := "AKIA" + "2ZVWXTYPLMNBVCXZ"
	out, err := red.RedactIn("my key is " + fake)
	if err != nil || strings.Contains(out, fake) {
		t.Fatalf("RedactIn = %q %v, want the key masked", out, err)
	}
}

func TestRefusedHow(t *testing.T) {
	for status, want := range map[string]string{
		store.ApprovalDenied:  "denied",
		store.ApprovalExpired: "timed out",
		"odd":                 "status is odd",
	} {
		if got := refusedHow(&approvals.Approval{Status: status}); !strings.Contains(got, want) {
			t.Errorf("refusedHow(%q) = %q", status, got)
		}
	}
}

func TestAuditEvents(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	AuditEvents(nil, nil)(Event{Type: EventMessage}) // no log: nothing, no panic
	log := audit.New(s, nil)
	sess := "s1"
	b := AuditEvents(log, &sess)
	b(Event{Type: EventMessage})
	b(Event{Type: EventTool, Tool: ToolCall{Name: "status", Decision: "allow", ApprovedBy: "admin"}})
	entries, err := log.Recent(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	all := strings.Join(actions, " ")
	if !strings.Contains(all, "assistant.message") || !strings.Contains(all, "assistant.tool.allow") {
		t.Fatalf("audit = %s", all)
	}
}
