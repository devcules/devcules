package pty_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/devcules/devcules/internal/terminals/pty"
)

// testManager builds a Manager on a temp socket dir: validation and
// bookkeeping run before any process spawns, so these tests run
// everywhere (the real-tmux suite is tmux_test.go, skip-gated for CI).
func testManager(t *testing.T) *pty.Manager {
	t.Helper()
	m, err := pty.NewForTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestValidationWithoutTmux(t *testing.T) {
	t.Parallel()
	m := testManager(t)
	ctx := context.Background()
	id := "0123456789abcdef"

	if _, err := m.Open(ctx, "bad"); err == nil {
		t.Fatal("Open(bad id) succeeded")
	}
	if _, err := m.Attach(ctx, "bad", id, 80, 24); err == nil {
		t.Fatal("Attach(bad term) succeeded")
	}
	if _, err := m.Attach(ctx, id, "bad", 80, 24); err == nil {
		t.Fatal("Attach(bad stream) succeeded")
	}
	if _, err := m.Attach(ctx, id, id, 0, 24); err == nil {
		t.Fatal("Attach(0 cols) succeeded")
	}
	if _, err := m.Attach(ctx, id, id, 501, 24); err == nil {
		t.Fatal("Attach(501 cols) succeeded")
	}
	if err := m.Write("bad", []byte("x")); err == nil {
		t.Fatal("Write(bad stream) succeeded")
	}
	if err := m.Write(id, []byte("x")); err == nil {
		t.Fatal("Write(unknown stream) succeeded")
	}
	if err := m.Resize(ctx, "bad", 80, 24); err == nil {
		t.Fatal("Resize(bad stream) succeeded")
	}
	if err := m.Resize(ctx, id, 0, 24); err == nil {
		t.Fatal("Resize(0 cols) succeeded")
	}
	if err := m.Close(ctx, "bad"); err == nil {
		t.Fatal("Close(bad id) succeeded")
	}
	// Detach of nothing is silent: deletes are idempotent.
	m.Detach(id)
	if err := m.Close(ctx, id); err == nil {
		// Without tmux this fails at list-sessions; with tmux the
		// session is missing and Close succeeds. Either way the id
		// shape was accepted — the branch is covered by tmux_test.
		t.Logf("Close without tmux: skipped (%v)", err)
	}
}

// TestNewRefusesWithoutTmux pins the fail-fast message when tmux is
// missing. It only runs where tmux is absent; where tmux exists the
// constructor succeeds and there is nothing to assert here.
func TestNewRefusesWithoutTmux(t *testing.T) {
	t.Parallel()
	if pty.HasTmux() {
		t.Skip("tmux present: constructor succeeds (covered by tmux_test)")
	}
	if _, err := pty.New(t.TempDir(), t.TempDir(), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New without tmux succeeded")
	}
}
