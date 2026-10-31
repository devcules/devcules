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
