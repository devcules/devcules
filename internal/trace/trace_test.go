package trace

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

func newStore(t *testing.T) (*Store, *bytes.Buffer) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var logs bytes.Buffer
	return New(s, slog.New(slog.NewTextHandler(&logs, nil))), &logs
}

func TestTraceCapsAndReads(t *testing.T) {
	ts, _ := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC)
	var tools []store.TraceToolCall
	for range MaxToolCalls + 5 {
		tools = append(tools, store.TraceToolCall{Name: "bash", Args: strings.Repeat("a", MaxArgs+10)})
	}
	ts.Trace(ctx, store.TraceCall{LaunchID: "l1", At: at, Provider: "meta", Path: "v1/chat/completions", Model: "m",
		Status: 200, InputTokens: 3, OutputTokens: 4, Redacted: map[string]int{"SECRET": 1},
		Question: strings.Repeat("é", MaxText), Reply: "ok", ToolCalls: tools})
	ts.Trace(ctx, store.TraceCall{LaunchID: "l2", At: at.Add(time.Minute), Provider: "meta", Path: "v1/models", Status: 200})

	sessions, err := ts.Summaries(ctx, 0)
	if err != nil || len(sessions) != 2 || sessions[0].LaunchID != "l2" || sessions[1].Redacted != 1 || sessions[1].InputTokens != 3 {
		t.Fatalf("Sessions = %+v, %v", sessions, err)
	}
	calls, err := ts.Calls(ctx, "l1")
	if err != nil || len(calls) != 1 {
		t.Fatalf("Calls = %+v, %v", calls, err)
	}
	c := calls[0]
	if len(c.Question) > MaxText+len(" …[truncated]") || !strings.HasSuffix(c.Question, "[truncated]") || !strings.HasPrefix(c.Question, "é") {
		t.Fatalf("question not capped on a rune: %d bytes", len(c.Question))
	}
	if len(c.ToolCalls) != MaxToolCalls || len(c.ToolCalls[0].Args) > MaxArgs+len(" …[truncated]") {
		t.Fatalf("tool calls not capped: %d", len(c.ToolCalls))
	}
	if c.Redacted["SECRET"] != 1 || c.Reply != "ok" || c.Model != "m" {
		t.Fatalf("call = %+v", c)
	}
	empty, err := ts.Calls(ctx, "l2")
	if err != nil || empty[0].ToolCalls == nil {
		t.Fatal("an empty tool list must read as [], not null")
	}
}

func TestPrune(t *testing.T) {
	ts, _ := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC)
	ts.now = func() time.Time { return now }
	ts.Trace(ctx, store.TraceCall{LaunchID: "old", At: now.Add(-31 * 24 * time.Hour)})
	ts.Trace(ctx, store.TraceCall{LaunchID: "new", At: now.Add(-29 * 24 * time.Hour)})
	if n, err := ts.Prune(ctx, 0); n != 0 || err != nil {
		t.Fatalf("Prune(0) = %d, %v; want keep everything", n, err)
	}
	if n, err := ts.Prune(ctx, DefaultDays); n != 1 || err != nil {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	s, _ := ts.Summaries(ctx, 10)
	if len(s) != 1 || s[0].LaunchID != "new" {
		t.Fatalf("after Prune: %+v", s)
	}
}

func TestWriteFailureIsLoggedNotFatal(t *testing.T) {
	ts, logs := newStore(t)
	ts.Trace(context.Background(), store.TraceCall{Question: "secret-free"})
	if !strings.Contains(logs.String(), "trace write failed") || strings.Contains(logs.String(), "secret-free") {
		t.Fatalf("log = %q", logs.String())
	}
}

func TestReadErrors(t *testing.T) {
	ts, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ts.Summaries(ctx, 5); err == nil {
		t.Fatal("Sessions hid an error")
	}
	if _, err := ts.Calls(ctx, "x"); err == nil {
		t.Fatal("Calls hid an error")
	}
}
