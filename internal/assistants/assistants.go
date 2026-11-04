// Package assistants holds the Assistant interface, the conversation
// loop behind it, and the web (session-scoped) plumbing the built-in
// assistant and future style adapters share.
//
// An Assistant is a conversational helper that improves the setup
// (PLAN.md §8): it answers questions about Devcules and can change the
// setup through the same MCP tools a coding agent uses, with every
// change gated by the approvals policy exactly as an agent's call is.
// Adapters (builtin, later hermes and openclaw) implement running the
// conversation against a model; this package supplies the loop, the
// tool gateway and the user-message redaction round trip.
//
// It does not stream to the browser (events are buffered per turn and
// served as JSON), does not store conversation history across server
// restarts, and never holds a provider key: model calls go to an
// upstream URL the server wires at construction, where the provider
// keys live.
package assistants

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"
)

// Assistant is one conversational helper: one way of running a
// conversation against a setup helper model. builtin is the local
// implementation; hermes and openclaw are external services reached
// over OpenAI-style APIs and MCP (M5.2).
type Assistant interface {
	// ID names the assistant for the API and audit: "builtin",
	// "hermes", "openclaw".
	ID() string
	// Chat appends in to the conversation sess ("" starts one) and
	// returns the conversation's id (the newly minted one for "") and
	// the events the reply produced. It returns when the turn is fully
	// played out, including any tool calls and their approvals; the
	// caller shows the events in order. The id comes back to this
	// caller only, so concurrent fresh chats never see each other's.
	Chat(ctx context.Context, sess string, in string) (string, []Event, error)
	// Session returns the conversation, or store.ErrNotFound.
	Session(sess string) (Session, error)
}

// Message is one turn in a conversation. Tool hides tool calls and
// results from the chat view.
type Message struct {
	Role    string    `json:"role"` // user, assistant, tool
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// Session is a conversation as the API shows it.
type Session struct {
	ID        string    `json:"id"`
	Assistant string    `json:"assistant"`
	Messages  []Message `json:"messages"`
	Created   time.Time `json:"created"`
}

const (
	// EventMessage marks a complete assistant answer (text).
	EventMessage = "assistant.message"
	// EventTool marks one tool call, its result and how it was gated.
	EventTool = "assistant.tool"
)

// Event is one displayable step of a reply.
type Event struct {
	Type string    `json:"type"`
	Text string    `json:"text,omitempty"`
	Tool ToolCall  `json:"tool,omitempty"`
	At   time.Time `json:"at"`
}

// ToolCall is one tool step of a reply as the chat shows it.
type ToolCall struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Args       json.RawMessage `json:"args"`
	Decision   string          `json:"decision"` // gate verdict: allow, ask, deny
	ApprovedBy string          `json:"approvedBy,omitempty"`
	Result     string          `json:"result"`
	IsError    bool            `json:"isError"`
}

// sessionID is the id alphabet the UI accepts: 16 lowercase hex
// characters, simple enough that a curl user can spot a typo.
var sessionID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidSessionID reports a well-formed conversation id.
func ValidSessionID(id string) bool { return sessionID.MatchString(id) }

// assistantID is the id alphabet the API accepts: like sessionID,
// short enough to spot a typo.
var assistantID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidAssistant reports a well-formed assistant id.
func ValidAssistant(id string) bool { return assistantID.MatchString(id) }

// Registry is the wired assistants, as serve builds them: the API
// lists and dispatches by id.
type Registry struct {
	all map[string]Assistant
}

// NewRegistry registers assistants; a duplicate or invalid id is an
// error at startup, never a swap later.
func NewRegistry(list ...Assistant) (*Registry, error) {
	r := &Registry{all: map[string]Assistant{}}
	for _, a := range list {
		if !ValidAssistant(a.ID()) {
			return nil, errors.New("assistants: bad id " + a.ID())
		}
		if _, dup := r.all[a.ID()]; dup {
			return nil, errors.New("assistants: duplicate id " + a.ID())
		}
		r.all[a.ID()] = a
	}
	return r, nil
}

// Get returns one assistant.
func (r *Registry) Get(id string) (Assistant, bool) {
	a, ok := r.all[id]
	return a, ok
}

// List returns every assistant in id order.
func (r *Registry) List() []Assistant {
	out := make([]Assistant, 0, len(r.all))
	for _, a := range r.all {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
