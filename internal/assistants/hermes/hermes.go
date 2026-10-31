// Package hermes is the assistant adapter for a Hermes agent runtime
// (assistant id "hermes"): the conversational helper served by a
// Hermes deployment the operator points Devcules at. Hermes talks an
// OpenAI-style chat completions endpoint with tool calling; this
// adapter is the shared assistantsopenai.Caller pointed at Hermes's
// base URL with its token, plus the ID the picker shows.
//
// It bundles nothing: PLAN.md §2's "detect or install on the user's
// machine at runtime" holds for assistants as for agent CLIs. A desk
// that has not been pointed at a Hermes runtime simply does not list
// this assistant.
package hermes

import (
	"context"
	"strings"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistantsopenai"
)

// DefaultModel is the Hermes planner model.
const DefaultModel = "hermes-4"

// Assistant is the Hermes adapter: the shared loop over Hermes's
// OpenAI-style endpoint.
type Assistant struct {
	loop  *assistants.Loop
	state string
}

// New wires the adapter over an existing loop (serve builds it from
// config). state names why the adapter is not answering when it is
// not; "" means it is.
func New(loop *assistants.Loop, state string) (*Assistant, error) {
	if loop == nil {
		return nil, errNilLoop
	}
	return &Assistant{loop: loop, state: strings.TrimSpace(state)}, nil
}

var errNilLoop = errNilLoopT("hermes: a loop is required")

type errNilLoopT string

func (e errNilLoopT) Error() string { return string(e) }

// Caller builds the upstream caller for a Hermes runtime: model,
// base URL and its bearer, on the shared no-redirect client.
func Caller(model, baseURL, token string) (*assistantsopenai.Caller, error) {
	return assistantsopenai.NewCaller(model, baseURL, token, assistantsopenai.NoRedirect(nil))
}

// ID is "hermes", as the API lists it.
func (a *Assistant) ID() string { return "hermes" }

// State implements the picker's readiness note.
func (a *Assistant) State() string { return a.state }

// Chat implements assistants.Assistant.
func (a *Assistant) Chat(ctx context.Context, sess, in string) ([]assistants.Event, error) {
	return a.loop.Chat(ctx, sess, in)
}

// Session implements assistants.Assistant.
func (a *Assistant) Session(sess string) (assistants.Session, error) {
	return a.loop.Session(sess)
}

// Loop exposes the shared loop for the API's session listing.
func (a *Assistant) Loop() *assistants.Loop { return a.loop }
