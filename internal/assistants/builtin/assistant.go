package builtin

import (
	"context"
	"errors"

	"github.com/devcules/devcules/internal/assistants"
)

// Assistant is the built-in Devcules assistant: the shared
// conversation loop over Caller's chat-completions endpoint, with the
// desk's MCP tools behind the approvals gate. Wired along serve with
// the default provider's key it is the PLAN's "built-in assistant on
// Muse Spark"; with any other OpenAI-compatible base URL it is the
// same helper on that endpoint.
//
// Chat is synchronous: events come back when the turn has fully
// played out, in order, for the JSON shape the web tier serves. The
// underlying loop serialises one session's turns.
type Assistant struct {
	loop *assistants.Loop
}

// New wires the assistant over an existing loop: serve builds the
// loop (tools, gate, redactor) and this holds it.
func New(loop *assistants.Loop) (*Assistant, error) {
	if loop == nil {
		return nil, errNilLoop
	}
	return &Assistant{loop: loop}, nil
}

var errNilLoop = errors.New("builtin: a loop is required")

// ID is "builtin", as the API lists it.
func (a *Assistant) ID() string { return "builtin" }

// Chat implements assistants.Assistant.
func (a *Assistant) Chat(ctx context.Context, sess, in string) ([]assistants.Event, error) {
	return a.loop.Chat(ctx, sess, in)
}

// Session implements assistants.Assistant.
func (a *Assistant) Session(sess string) (assistants.Session, error) {
	return a.loop.Session(sess)
}

// Loop exposes the shared loop for the web tier's session listing.
func (a *Assistant) Loop() *assistants.Loop { return a.loop }
