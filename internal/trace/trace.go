package trace

import (
	"context"
	"io"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/devcules/devcules/internal/store"
)

const (
	// MaxText caps the question and the reply.
	MaxText = 32 << 10
	// MaxArgs caps one tool call's arguments.
	MaxArgs = 8 << 10
	// MaxToolCalls caps the tool calls kept per call.
	MaxToolCalls = 64
	// DefaultDays is the default retention.
	DefaultDays = 30
)

// Store keeps traces. It implements the router's Tracer.
type Store struct {
	repo store.TracesRepo
	log  *slog.Logger
	now  func() time.Time
}

// New returns a Store over the store's trace repo. logger reports
// failed writes (a lost trace never fails the model call); nil
// discards.
func New(s store.Store, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Store{repo: s.Traces(), log: logger, now: time.Now}
}

// truncate cuts s to max bytes on a rune boundary and marks the cut.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + " …[truncated]"
}

// Trace stores one call, capped. Errors are logged, never returned:
// the reply has already gone to the agent.
func (t *Store) Trace(ctx context.Context, c store.TraceCall) {
	c.Question = truncate(c.Question, MaxText)
	c.Reply = truncate(c.Reply, MaxText)
	if len(c.ToolCalls) > MaxToolCalls {
		c.ToolCalls = c.ToolCalls[:MaxToolCalls]
	}
	for i := range c.ToolCalls {
		c.ToolCalls[i].Name = truncate(c.ToolCalls[i].Name, 200)
		c.ToolCalls[i].Args = truncate(c.ToolCalls[i].Args, MaxArgs)
	}
	if _, err := t.repo.Add(ctx, c); err != nil {
		t.log.Error("trace write failed", "launch", c.LaunchID, "err", err.Error())
	}
}

// Prune deletes calls older than days. days <= 0 keeps everything.
func (t *Store) Prune(ctx context.Context, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	return t.repo.DeleteBefore(ctx, t.now().Add(-time.Duration(days)*24*time.Hour))
}

// Session is one launch's summary, as the API shows it.
type Session struct {
	LaunchID     string    `json:"launchId"`
	Calls        int       `json:"calls"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
	Redacted     int       `json:"redacted"`
	First        time.Time `json:"first"`
	Last         time.Time `json:"last"`
}

// ToolCall is one tool call a model asked for.
type ToolCall struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

// Call is one model call, as the API shows it.
type Call struct {
	ID           int64          `json:"id"`
	At           time.Time      `json:"at"`
	Provider     string         `json:"provider"`
	Path         string         `json:"path"`
	Model        string         `json:"model"`
	Status       int            `json:"status"`
	Stream       bool           `json:"stream"`
	DurationMS   int64          `json:"durationMs"`
	InputTokens  int64          `json:"inputTokens"`
	OutputTokens int64          `json:"outputTokens"`
	Redacted     map[string]int `json:"redacted"`
	Question     string         `json:"question"`
	Reply        string         `json:"reply"`
	ToolCalls    []ToolCall     `json:"toolCalls"`
}

// Summaries lists up to limit launches with calls, latest first.
func (t *Store) Summaries(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := t.repo.Summaries(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session(r))
	}
	return out, nil
}

// Calls returns one launch's calls, oldest first.
func (t *Store) Calls(ctx context.Context, launchID string) ([]Call, error) {
	rows, err := t.repo.Calls(ctx, launchID)
	if err != nil {
		return nil, err
	}
	out := make([]Call, 0, len(rows))
	for _, r := range rows {
		c := Call{ID: r.ID, At: r.At, Provider: r.Provider, Path: r.Path, Model: r.Model, Status: r.Status,
			Stream: r.Stream, DurationMS: r.DurationMS, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			Redacted: r.Redacted, Question: r.Question, Reply: r.Reply, ToolCalls: []ToolCall{}}
		for _, tc := range r.ToolCalls {
			c.ToolCalls = append(c.ToolCalls, ToolCall(tc))
		}
		out = append(out, c)
	}
	return out, nil
}
