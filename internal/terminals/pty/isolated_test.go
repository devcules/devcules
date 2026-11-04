package pty_test

import (
	"context"
	"testing"
)

// A distinctive temp directory alone didn't isolate -L devcules: its
// socket stayed under /tmp/tmux-<uid>/devcules, and one test killed
// another's server. With an explicit private -S socket, managers are
// independent even when their jobs run in parallel packages.
func TestManagersIsolateTmuxServers(t *testing.T) {
	t.Parallel()
	a, _ := tmuxManager(t)
	b, _ := tmuxManager(t)
	ctx := context.Background()
	if _, err := a.Open(ctx, "1111111111111111"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(ctx, "2222222222222222"); err != nil {
		t.Fatal(err)
	}
	b.Shutdown(ctx)
	readUntil(t, a, ctx, "1111111111111111", "aaaaaaaaaaaaaaaa", "printf 'isolated-ok\\n'\n", "isolated-ok")
	if list, err := a.List(ctx); err != nil || len(list) != 1 || list[0].Term != "1111111111111111" {
		t.Fatalf("first manager after other shutdown: %+v, %v", list, err)
	}
}
