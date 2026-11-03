package hermes_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistanttest"
	"github.com/devcules/devcules/internal/assistants/hermes"
)

// TestContract is the M5.2 gate for Hermes: the contract suite plays
// against the adapter wired to the scripted upstream.
func TestContract(t *testing.T) {
	assistanttest.RunUpstream(t,
		func(t *testing.T, ups *httptest.Server, gate func(context.Context, assistants.ToolCallAsk) assistants.GateResult) assistants.Assistant {
			t.Helper()
			caller, err := hermes.Caller(hermes.DefaultModel, ups.URL, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			loop, err := assistants.NewLoop("hermes", assistants.Options{
				Model: caller, Tools: assistanttest.Tools, Gate: gate,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			a, err := hermes.New(loop, "")
			if err != nil {
				t.Fatal(err)
			}
			return a
		},
		assistanttest.GateOf,
	)
}

// TestAdapter covers what the contract leaves out: a missing loop is
// refused, and the picker reads the id and the trimmed readiness note.
func TestAdapter(t *testing.T) {
	if _, err := hermes.New(nil, ""); err == nil || err.Error() == "" {
		t.Fatalf("New(nil) = %v, want a named error", err)
	}
	caller, err := hermes.Caller(hermes.DefaultModel, "http://127.0.0.1:1", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	gate, _ := assistanttest.GateOf(t, nil)
	loop, err := assistants.NewLoop("hermes", assistants.Options{Model: caller, Tools: assistanttest.Tools, Gate: gate}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := hermes.New(loop, "  set its base URL in Settings  ")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != "hermes" || a.State() != "set its base URL in Settings" || a.Loop() != loop {
		t.Fatalf("ID %q, State %q, Loop %v", a.ID(), a.State(), a.Loop() == loop)
	}
	if _, err := a.Session("no-such-session"); err == nil {
		t.Fatal("an unknown session was found")
	}
}
