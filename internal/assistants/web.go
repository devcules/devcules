package assistants

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/mcp"
	"github.com/devcules/devcules/internal/store"
)

// SessionStore keeps conversations. The in-memory default below is
// what serve uses until a store-suite implementation lands; the
// interface matches the store package's repo style so the swap is
// one wiring change.
type SessionStore interface {
	// Get returns the conversation or store.ErrNotFound.
	Get(id string) (*Conversation, error)
	// Put creates or replaces the conversation.
	Put(c *Conversation)
}

// Conversation is the loop's record of one session. Pending holds the
// redacted arguments of each tool call awaiting its result, because a
// compaction pass may have rewritten them out of the resent history:
// the run uses the stored copy, never a post-compaction argument.
type Conversation struct {
	ID        string
	Assistant string
	Msgs      []ChatMessage
	Shown     []Message
	Pending   map[string]json.RawMessage
	Created   time.Time
}

// MemorySessions is the in-memory SessionStore.
type MemorySessions struct {
	mu  sync.Mutex
	all map[string]*Conversation
	ids []string
}

// NewMemorySessions starts an empty store.
func NewMemorySessions() *MemorySessions {
	return &MemorySessions{all: map[string]*Conversation{}}
}

// Get implements SessionStore.
func (m *MemorySessions) Get(id string) (*Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.all[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return c, nil
}

// Put implements SessionStore.
func (m *MemorySessions) Put(c *Conversation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.all[c.ID]; !ok {
		m.ids = append(m.ids, c.ID)
	}
	m.all[c.ID] = c
}

// ListAll is for tests and the future list-sessions API: every stored
// conversation, oldest first.
func (m *MemorySessions) ListAll() []*Conversation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Conversation, 0, len(m.ids))
	for _, id := range m.ids {
		out = append(out, m.all[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// newSessionID returns a random 16-hex id (64 bits; crypto/rand, like
// the rest of Devcules's ids).
func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// rand.Read cannot fail on supported platforms; fall back to a
		// time-seeded id rather than crash a chat.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// PolicyOptions wires ApprovalsGate.
type PolicyOptions struct {
	// Decider decides and waits; required.
	Decider *approvals.Service
	// Runner is the approvals target the assistant's calls count as
	// (its own mode, separate from any terminal's runner).
	Runner string
	// Agent names the assistant in approval records.
	Agent string
	// LaunchID ties approvals to the assistant's pseudo-launch.
	LaunchID string
	// Wait caps how long one ask blocks the turn; the approvals
	// service's own timeout by default.
	Wait time.Duration
}

// ApprovalsGate is the web Gate: every tool call the model asks for
// goes through the approvals service (the runner's mode decides) and,
// on allow, runs against the MCP caller.
func ApprovalsGate(o PolicyOptions, run func(ctx context.Context, ask ToolCallAsk) (mcp.Result, error)) func(ctx context.Context, ask ToolCallAsk) GateResult {
	wait := o.Wait
	if wait <= 0 {
		wait = o.Decider.Timeout()
	}
	return func(ctx context.Context, ask ToolCallAsk) GateResult {
		out, err := o.Decider.Ask(ctx, approvals.Request{
			LaunchID: o.LaunchID,
			Runner:   o.Runner,
			Agent:    o.Agent,
			Tool:     ask.Name,
			Command:  string(ask.Args),
		})
		if err != nil {
			return GateResult{Decision: "deny", Result: mcp.Errorf("the approval ask failed: %s", err)}
		}
		gr := GateResult{Decision: string(out.Verdict)}
		switch out.Verdict {
		case approvals.Deny:
			gr.Result = mcp.Errorf("the assistant is in a mode that does not allow tools; change the mode for %s in Access or ask for a read-only answer", o.Runner)
			return gr
		case approvals.Ask:
			out, err = o.Decider.Wait(ctx, o.LaunchID, out.Approval.ID, wait)
			if err != nil {
				gr.Result = mcp.Errorf("the approval could not be read: %s", err)
				return gr
			}
			gr.ApprovedBy = out.Approval.AnsweredBy
			if out.Approval.Status != store.ApprovalAllowed {
				gr.Result = mcp.Errorf("the tool call was refused%s", refusedHow(out.Approval))
				return gr
			}
		}
		res, err := run(ctx, ask)
		if err != nil {
			res = mcp.Errorf("the tool failed internally: %s", err)
		}
		gr.Result = res
		return gr
	}
}

func refusedHow(a *approvals.Approval) string {
	switch a.Status {
	case store.ApprovalDenied:
		return ": the owner denied it"
	case store.ApprovalExpired:
		return ": it timed out waiting for an answer"
	default:
		return ": its status is " + a.Status
	}
}

// AuditEvents returns a Broadcaster that audits one record per event
// for an assistant's turns: assistant.message and assistant.tool.
// for an assistant's turns: assistant.message and assistant.tool.
func AuditEvents(log *audit.Log, sess *string) Broadcaster {
	return func(e Event) {
		if log == nil {
			return
		}
		target := ""
		if sess != nil {
			target = *sess
		}
		switch e.Type {
		case EventMessage:
			_, _ = log.Record(context.Background(), audit.Event{
				Actor: "assistant", Action: "assistant.message", Target: target})
		case EventTool:
			_, _ = log.Record(context.Background(), audit.Event{
				Actor: "assistant", Action: "assistant.tool." + e.Tool.Decision, Target: target,
				Detail: map[string]string{"tool": e.Tool.Name, "by": e.Tool.ApprovedBy}})
		}
	}
}
