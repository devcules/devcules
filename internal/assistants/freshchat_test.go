package assistants

import (
	"context"
	"testing"

	"github.com/devcules/devcules/internal/mcp"
)

// b2BlockingModel parks every call until release is closed.
type b2BlockingModel struct {
	entered chan struct{}
	release chan struct{}
}

func (m *b2BlockingModel) Chat(context.Context, ChatRequest) (ModelReply, error) {
	m.entered <- struct{}{}
	<-m.release
	return ModelReply{Text: "ok"}, nil
}

// TestFreshChatConcurrent runs two fresh chats in flight at once (two
// tabs, phone plus desktop): each caller must get its own conversation
// id back. Before, the minted id sat in one shared slot read after the
// turn, so A got B's id and B got "" (GOSEC-02).
func TestFreshChatConcurrent(t *testing.T) {
	m := &b2BlockingModel{entered: make(chan struct{}, 2), release: make(chan struct{})}
	l, err := NewLoop("builtin", Options{Model: m, Tools: func() []mcp.Tool { return nil }, Gate: func(context.Context, ToolCallAsk) GateResult { return GateResult{} }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := make(chan string), make(chan string)
	go func() { id, _, _ := l.Chat(context.Background(), "", "hi from A"); a <- id }()
	<-m.entered
	go func() { id, _, _ := l.Chat(context.Background(), "", "hi from B"); b <- id }()
	<-m.entered
	close(m.release)
	ida, idb := <-a, <-b
	if ida == "" || idb == "" || ida == idb {
		t.Fatalf("fresh ids mixed up: A=%q B=%q (each caller must get its own conversation id)", ida, idb)
	}
	for id, want := range map[string]string{ida: "hi from A", idb: "hi from B"} {
		s, err := l.Session(id)
		if err != nil || b2FirstUser(s) != want {
			t.Fatalf("session %q first user msg = %q (%v), want %q", id, b2FirstUser(s), err, want)
		}
	}
}

// TestFreshChatSharedSlotInterleave replays the interleaving where B's
// turn finishes before A's handler resolves its id (GORT-05): A must
// still be handed its own conversation and B its own.
func TestFreshChatSharedSlotInterleave(t *testing.T) {
	loop := testLoop(t, &scriptedModel{reply: []ModelReply{{Text: "to A"}, {Text: "to B"}}}, func(context.Context, ToolCallAsk) GateResult {
		return GateResult{Decision: "deny", Result: mcp.Result{Text: "x", IsError: true}}
	})
	ctx := context.Background()
	idForA, _, err := loop.Chat(ctx, "", "from A")
	if err != nil {
		t.Fatal(err)
	}
	idForB, _, err := loop.Chat(ctx, "", "from B")
	if err != nil {
		t.Fatal(err)
	}
	sa, _ := loop.Session(idForA)
	sb, _ := loop.Session(idForB)
	if idForA == "" || idForB == "" || b2FirstUser(sa) != "from A" || b2FirstUser(sb) != "from B" {
		t.Fatalf("A told %q (first msg %q), B told %q (first msg %q)", idForA, b2FirstUser(sa), idForB, b2FirstUser(sb))
	}
}

func b2FirstUser(s Session) string {
	for _, m := range s.Messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}
