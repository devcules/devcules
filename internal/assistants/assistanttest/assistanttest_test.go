package assistanttest_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistantsopenai"
	"github.com/devcules/devcules/internal/assistants/assistanttest"
)

// toy is the smallest assistant: the shared loop plus an id.
type toy struct{ *assistants.Loop }

func (toy) ID() string { return "toy" }

// TestContractPassesTheSharedLoop proves the suite passes a good
// assistant: the shared loop with the script as its model, so an
// adapter failing the suite is at fault, not the suite.
func TestContractPassesTheSharedLoop(t *testing.T) {
	assistanttest.Run(t, func(t *testing.T, script *assistanttest.Script, allow map[string]string) (assistants.Assistant, *sync.Map) {
		gate, ran := assistanttest.GateOf(t, allow)
		loop, err := assistants.NewLoop("toy", assistants.Options{Model: script, Tools: assistanttest.Tools, Gate: gate}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return toy{loop}, ran
	})
}

// TestUpstreamContract runs the same suite through the mock
// OpenAI-style upstream and the shared caller, the way the hermes and
// openclaw adapters do.
func TestUpstreamContract(t *testing.T) {
	assistanttest.RunUpstream(t,
		func(t *testing.T, ups *httptest.Server, gate func(context.Context, assistants.ToolCallAsk) assistants.GateResult) assistants.Assistant {
			caller, err := assistantsopenai.NewCaller("toy-1", ups.URL, "test-key", nil)
			if err != nil {
				t.Fatal(err)
			}
			loop, err := assistants.NewLoop("toy", assistants.Options{Model: caller, Tools: assistanttest.Tools, Gate: gate}, nil)
			if err != nil {
				t.Fatal(err)
			}
			return toy{loop}
		},
		assistanttest.GateOf,
	)
}

// TestScriptRecordsCalls: the script hands back each step in order,
// records the requests, and errs once it runs out.
func TestScriptRecordsCalls(t *testing.T) {
	s := assistanttest.NewScript().Say("one")
	if r, err := s.Chat(context.Background(), assistants.ChatRequest{System: "sys"}); err != nil || r.Text != "one" {
		t.Fatalf("first = %+v %v", r, err)
	}
	if _, err := s.Chat(context.Background(), assistants.ChatRequest{}); err == nil {
		t.Fatal("an exhausted script answered")
	}
	if calls := s.Calls(); len(calls) == 0 || calls[0].System != "sys" {
		t.Fatalf("calls = %+v", calls)
	}
}
