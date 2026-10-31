package assistants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/mcp"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
)

// MaxTurns is the loop guard: an assistant may chain at most this many
// model calls in one Chat before the loop ends the turn, so a model
// that keeps asking for tools cannot spin forever.
const MaxTurns = 25

// maxMessages caps the history held per conversation; older turns
// drop off the front (the loop resends them on every model call).
const maxMessages = 200

// maxInput caps one user message, like the settings test box.
const maxInput = 64 << 10

// Trim caps a snippet (an upstream error, a quoted turn) at 400
// bytes on a rune boundary, so a chat surface never swallows a flood.
func Trim(s string) string { return trunc(s, 400) }

// errBlocked marks a redaction block on the user message: it is
// rejected instead of sent, and Blocked recognises it.
type errBlocked struct{ err error }

func (e *errBlocked) Error() string { return e.err.Error() }

// Blocked reports that err was a redaction block on the user message:
// the caller shows the text as-is (it names the blocked kinds), rather
// than a generic failure.
func Blocked(err error) (string, bool) {
	var b *errBlocked
	if errors.As(err, &b) {
		return b.Error(), true
	}
	return "", false
}

// ModelCaller sends one turn to the model and returns its reply. The
// conversation (system prompt plus history) goes with every call; the
// loop owns tool execution, so the caller only translates. req is
// redacted and the caller must restore placeholders in the reply's
// text and arguments (Devcules's redaction boundary, PLAN.md §3).
type ModelCaller interface {
	Chat(ctx context.Context, req ChatRequest) (ModelReply, error)
}

// ChatMessage is one message as the model sees it. ToolCalls ride on
// assistant messages; a tool message carries one call's result.
type ChatMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []ToolCallAsk `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// ToolCallAsk is a model's request to run one tool.
type ToolCallAsk struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ChatRequest is one model call in a conversation.
type ChatRequest struct {
	System   string
	Messages []ChatMessage
	Tools    []mcp.Tool
}

// ModelReply is one model answer: text to say, tools to run, or both.
type ModelReply struct {
	Text      string
	ToolCalls []ToolCallAsk
}

// Broadcaster receives loop events as they happen: serve hooks it to
// the audit log. It must not block.
type Broadcaster func(Event)

// Options wires a Loop. Model, Tools and Gate are required.
type Options struct {
	// Prompt is the system prompt; DefaultPrompt when empty.
	Prompt string
	// Model talks to the helper model.
	Model ModelCaller
	// Tools returns the tools the assistant may call this turn: the
	// serve MCP registry (read-only built-ins plus registered manage
	// tools), translated to the model's format by the caller.
	Tools func() []mcp.Tool
	// Gate gates and runs one tool call (see ApprovalsGate).
	Gate func(ctx context.Context, ask ToolCallAsk) GateResult
	Now  func() time.Time
	// OnEvent receives every event after it is buffered.
	OnEvent Broadcaster
	// Convos keeps conversations; an empty in-memory store by default,
	// which suits one serve process.
	Convos SessionStore
}

// GateResult is what a Gate answers for one tool call.
type GateResult struct {
	// Decision is the gate's verdict: allow, ask or deny.
	Decision string
	// ApprovedBy names who answered an ask that was allowed.
	ApprovedBy string
	// Args are the arguments exactly as gated and run: the chat shows
	// these, which are what the model wrote this turn (compaction
	// never rewrites them out from under a call).
	Args json.RawMessage
	// Result is the tool's answer as the model reads it; a veto comes
	// back as an error result so the model can explain it.
	Result mcp.Result
}

// Loop runs conversations for one assistant: it keeps the history,
// applies the user-message redaction round trip, calls the model,
// gates and runs the tools the model asks for, and buffers events.
type Loop struct {
	id  string
	o   Options
	red RedactIO

	// turns gives each session its own lock, so one Chat per session
	// runs at a time (the per-session conversation record is under
	// it); sessions do not wait on each other. fresh records the id
	// minted for the most recent "" chat so its caller can keep the
	// conversation; it drops on the next "".
	turns map[string]*sync.Mutex
	fresh string
	mu    sync.Mutex
}

// RedactIO is the redaction round trip the loop needs for user
// messages: mask on the way in (the model and the stored history only
// ever see placeholders). Tool arguments are strict: a placeholder in
// one is an error, because restoring there would hide a redaction gap
// the review tooling is meant to catch.
type RedactIO interface {
	RedactIn(text string) (string, error)
}

// NewLoop starts a loop. red may be nil (messages pass through).
func NewLoop(id string, o Options, red RedactIO) (*Loop, error) {
	if o.Model == nil || o.Tools == nil || o.Gate == nil {
		return nil, errors.New("assistants: model, tools and gate are required")
	}
	if o.Prompt == "" {
		o.Prompt = DefaultPrompt
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Convos == nil {
		o.Convos = NewMemorySessions()
	}
	return &Loop{id: id, o: o, red: red, turns: map[string]*sync.Mutex{}}, nil
}

// Chat implements Assistant.Chat: append in to sess ("" starts a new
// session) and play the turn out, returning what happened.
func (l *Loop) Chat(ctx context.Context, sess, in string) ([]Event, error) {
	if in = trunc(in, maxInput); in == "" {
		return nil, errors.New("assistants: a message is required")
	}
	masked := in
	if l.red != nil {
		var err error
		masked, err = l.red.RedactIn(in)
		if err != nil {
			return nil, &errBlocked{err}
		}
	}
	c, turn, err := l.session(sess)
	if err != nil {
		return nil, err
	}
	turn.Lock()
	defer turn.Unlock()

	var held []Event
	ev := func(e Event) {
		e.At = l.o.Now().UTC()
		held = append(held, e)
		if l.o.OnEvent != nil {
			l.o.OnEvent(e)
		}
	}

	c.Msgs = append(c.Msgs, ChatMessage{Role: "user", Content: masked})
	c.Shown = append(c.Shown, Message{Role: "user", Content: masked, At: l.o.Now().UTC()})
	for step := 0; ; step++ {
		if step >= MaxTurns {
			text := fmt.Sprintf("I stopped after %d tool steps without finishing, so this does not run forever. Ask again with a narrower task.", MaxTurns)
			c.Shown = append(c.Shown, Message{Role: "assistant", Content: text, At: l.o.Now().UTC()})
			ev(Event{Type: EventMessage, Text: text})
			break
		}
		reply, err := l.o.Model.Chat(ctx, ChatRequest{
			System:   l.o.Prompt,
			Messages: append([]ChatMessage(nil), c.Msgs...),
			Tools:    l.o.Tools(),
		})
		if err != nil {
			return nil, fmt.Errorf("assistants: the helper model could not be reached: %w", err)
		}
		if len(c.Msgs) > maxMessages/2 {
			compact(c, l.o.Now().UTC(), ev)
		}
		c.Msgs = append(c.Msgs, ChatMessage{Role: "assistant", Content: reply.Text, ToolCalls: reply.ToolCalls})
		if reply.Text != "" {
			c.Shown = append(c.Shown, Message{Role: "assistant", Content: reply.Text, At: l.o.Now().UTC()})
			ev(Event{Type: EventMessage, Text: reply.Text})
		}
		if len(reply.ToolCalls) == 0 {
			break
		}
		// The loop runs the calls exactly as the model wrote them in
		// this reply; Pending only remembers a call left running when
		// the server died mid-turn (a re-issued id after that is
		// refused in gated). A duplicate id in one reply is the model
		// confusing itself: the first occurrence runs with its own
		// arguments, the second is refused, so no call ever runs with
		// another's arguments.
		seen := map[string]bool{}
		for _, ask := range reply.ToolCalls {
			var res GateResult
			if seen[ask.ID] {
				res = GateResult{Decision: "deny", Args: ask.Args, Result: errorResult(
					"this reply named two tool calls " + strconv.Quote(ask.ID) + "; the second was refused")}
			} else {
				seen[ask.ID] = true
				res = l.gated(ctx, ask)
			}
			c.Msgs = append(c.Msgs, ChatMessage{Role: "tool", ToolCallID: ask.ID, Content: res.Result.Text})
			ev(Event{Type: EventTool, Tool: ToolCall{
				ID: ask.ID, Name: ask.Name, Args: res.Args,
				Decision: res.Decision, ApprovedBy: res.ApprovedBy,
				Result: res.Result.Text, IsError: res.Result.IsError,
			}})
		}
	}
	for len(c.Msgs) > maxMessages {
		c.Msgs = append([]ChatMessage(nil), c.Msgs[2:]...)
	}
	for len(c.Shown) > maxMessages {
		c.Shown = append([]Message(nil), c.Shown[len(c.Shown)-maxMessages/2:]...)
	}
	return held, nil
}

// compact folds the front of the history into one recalled-exchange
// message, preserving every tool call's name for the trace. It runs
// only between model calls, so an approval's arguments are always the
// ones the model wrote this turn and never come from a rewritten
// summary. Assistant text is quoted, user text and results trimmed.
func compact(c *Conversation, at time.Time, ev func(Event)) {
	if len(c.Msgs) < 3 {
		return
	}
	keep := 2 // the latest exchange must stay exact for tool pairing
	drop := c.Msgs[:len(c.Msgs)-keep]
	var b strings.Builder
	for _, m := range drop {
		var s string
		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			var names []string
			for _, t := range m.ToolCalls {
				names = append(names, t.Name)
			}
			s = "assistant asked to call " + strings.Join(names, ", ")
			if m.Content != "" {
				s += " saying " + quote(m.Content)
			}
		case m.Role == "tool":
			s = "the tool answered " + quote(trunc(m.Content, 400))
		default: // user, or assistant without calls
			s = m.Role + " said " + quote(trunc(m.Content, 400))
		}
		b.WriteString("- " + s + "\n")
	}
	summary := "Earlier in this conversation, compacted for length (arguments exactly as run are on record):\n" + b.String()
	c.Msgs = append([]ChatMessage{{Role: "assistant", Content: summary}}, c.Msgs[len(c.Msgs)-keep:]...)
	c.Shown = append(c.Shown, Message{Role: "assistant",
		Content: "(for length, earlier turns are held as a summary; approvals and results above are exact)", At: at})
}

func quote(s string) string { return strconv.Quote(s) }

// session finds (or, for "", mints) the conversation and its turn
// lock; a bad or unknown id is store.ErrNotFound, never a new session.
func (l *Loop) session(sess string) (*Conversation, *sync.Mutex, error) {
	if sess != "" && !ValidSessionID(sess) {
		return nil, nil, store.ErrNotFound
	}
	mint := sess == ""
	if mint {
		sess = newSessionID()
		l.mu.Lock()
		l.fresh = sess
		l.mu.Unlock()
	}
	c, err := l.o.Convos.Get(sess)
	if err != nil {
		// Only a "" call mints; an error or unknown id is surfaced, so
		// a typo never smuggles history into a fresh conversation and a
		// caller never mistakes another turn's text for theirs.
		if !mint || !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
		c = &Conversation{ID: sess, Assistant: l.id, Created: l.o.Now().UTC()}
		l.o.Convos.Put(c)
	}
	l.mu.Lock()
	turn, ok := l.turns[sess]
	if !ok {
		turn = &sync.Mutex{}
		l.turns[sess] = turn
	}
	l.mu.Unlock()
	return c, turn, nil
}

// FreshChat returns the id the latest "" Chat minted, or "", and
// clears it: the web tier resolves a fresh conversation's id once. It
// never returns an id that predates the caller's chat — Chat sets it
// right before the turn starts, and another fresh Chat after this one
// returns would only move it forward.
func (l *Loop) FreshChat() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.fresh
	l.fresh = ""
	return id
}

// Session implements Assistant.Session.
func (l *Loop) Session(sess string) (Session, error) {
	if !ValidSessionID(sess) {
		return Session{}, store.ErrNotFound
	}
	c, err := l.o.Convos.Get(sess)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: c.ID, Assistant: c.Assistant,
		Messages: append([]Message(nil), c.Shown...), Created: c.Created}, nil
}

// gated runs one asked-for tool through the gate on exactly the
// arguments the model wrote; a placeholder in them becomes an error
// result for the model: the turn continues and the reply says what
// went wrong.
func (l *Loop) gated(ctx context.Context, ask ToolCallAsk) GateResult {
	if redact.HasPlaceholder(ask.Args) {
		return GateResult{Decision: "deny", Args: ask.Args, Result: errorResult(
			"the model passed a redaction placeholder as an argument; ask the user for the real value or describe the change without it")}
	}
	res := l.o.Gate(ctx, ask)
	res.Args = ask.Args
	return res
}

// errorResult is mcp.Errorf without the format parsing of a message
// that already has punctuation.
func errorResult(msg string) mcp.Result { return mcp.Result{Text: msg, IsError: true} }

// trunc cuts text at the limit on a rune boundary.
func trunc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && (s[max]&0xC0) == 0x80 {
		max--
	}
	return s[:max]
}
