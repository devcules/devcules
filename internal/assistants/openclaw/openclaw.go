// Package openclaw is the assistant adapter for a OpenClaw agent runtime
// (assistant id "openclaw"). It shares the Hermes adapter's shape: the
// shared assistantsopenai.Caller pointed at the service's OpenAI-style
// chat endpoint, the ID the picker shows, and the readiness note. It
// bundles nothing: a desk that has not been pointed at the runtime
// does not list this assistant.
package openclaw

import (
	"context"
	"strings"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistantsopenai"
)

// DefaultModel is the OpenClaw planner model.
const DefaultModel = "openclaw-1"

// Assistant is the OpenClaw adapter: the shared loop over the service's
// OpenAI-style endpoint.
type Assistant struct {
	loop  *assistants.Loop
	state string
}

// New wires the adapter over an existing loop.
func New(loop *assistants.Loop, state string) (*Assistant, error) {
	if loop == nil {
		return nil, errNilLoop
	}
	return &Assistant{loop: loop, state: strings.TrimSpace(state)}, nil
}

var errNilLoop = errNilLoopT("openclaw: a loop is required")

type errNilLoopT string

func (e errNilLoopT) Error() string { return string(e) }

// Caller builds the upstream caller: model, base URL and its bearer,
// on the shared no-redirect client.
func Caller(model, baseURL, token string) (*assistantsopenai.Caller, error) {
	return assistantsopenai.NewCaller(model, baseURL, token, assistantsopenai.NoRedirect(nil))
}

// ID is "openclaw", as the API lists it.
func (a *Assistant) ID() string { return "openclaw" }

// State implements the picker's readiness note.
func (a *Assistant) State() string { return a.state }

// Chat implements assistants.Assistant.
func (a *Assistant) Chat(ctx context.Context, sess, in string) (string, []assistants.Event, error) {
	return a.loop.Chat(ctx, sess, in)
}

// Session implements assistants.Assistant.
func (a *Assistant) Session(sess string) (assistants.Session, error) {
	return a.loop.Session(sess)
}

// Loop exposes the shared loop for the API's session listing.
func (a *Assistant) Loop() *assistants.Loop { return a.loop }
