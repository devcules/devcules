package pty_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/pty"
)

// tmuxManager skips without tmux locally but fails in CI, where the
// unit job installs it: silent green locally must never mean green in
// CI. Each test gets a private server on a temp socket dir.
func tmuxManager(t *testing.T) (*pty.Manager, context.Context) {
	t.Helper()
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed locally")
	}
	dir := t.TempDir()
	m, err := pty.New(dir, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return m, context.Background()
}

// viewer collects one stream's output on a background goroutine, so
// a quiet PTY fails the test at its deadline instead of blocking a
// Read forever. The goroutine ends when the stream is detached.
type viewer struct {
	mu  sync.Mutex
	out strings.Builder
}

func watch(m *pty.Manager, stream string) *viewer {
	v := &viewer{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := m.Read(stream, buf)
			v.mu.Lock()
			v.out.Write(buf[:n])
			v.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return v
}

// waitFor polls until the output contains want. Output carries shell
// echo and ANSI, so matching is Contains, never exact.
func (v *viewer) waitFor(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		v.mu.Lock()
		got := v.out.String()
		v.mu.Unlock()
		if strings.Contains(got, want) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	t.Fatalf("output never contained %q (got %q)", want, v.out.String())
	return ""
}

// readUntil attaches, writes cmd, and waits until want appears.
func readUntil(t *testing.T, m *pty.Manager, ctx context.Context, id, stream, cmd, want string) string {
	t.Helper()
	if _, err := m.Attach(ctx, id, stream, 80, 24); err != nil {
		t.Fatalf("Attach: %s", err)
	}
	t.Cleanup(func() { m.Detach(stream) })
	v := watch(m, stream)
	if err := m.Write(stream, []byte(cmd)); err != nil {
		t.Fatalf("Write: %s", err)
	}
	return v.waitFor(t, want)
}

// TestTmuxRoundTrip proves a shell runs and answers: echo shows up.
func TestTmuxRoundTrip(t *testing.T) {
	m, ctx := tmuxManager(t)
	id := "0123456789abcdef"
	if _, err := m.Open(ctx, id); err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })
	readUntil(t, m, ctx, id, "9999999999999999", "echo hi-$((40+2))\n", "hi-42")
}

// TestTmuxDisconnectResume proves resume needs no buffer of ours:
// detach, reattach, and the earlier output is redrawn by tmux.
func TestTmuxDisconnectResume(t *testing.T) {
	m, ctx := tmuxManager(t)
	id := "abcdef0123456789"
	if _, err := m.Open(ctx, id); err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })

	s1 := "1111111111111111"
	readUntil(t, m, ctx, id, s1, "echo marker-$((123*2))\n", "marker-246")
	m.Detach(s1)

	s2 := "2222222222222222"
	if _, err := m.Attach(ctx, id, s2, 80, 24); err != nil {
		t.Fatalf("reattach: %s", err)
	}
	t.Cleanup(func() { m.Detach(s2) })
	// The redraw must show the command's output, not just its echo.
	watch(m, s2).waitFor(t, "marker-246")
}

// TestTmuxTwoViewers proves phone and desktop share one terminal: two
// attach clients both see the same output.
func TestTmuxTwoViewers(t *testing.T) {
	m, ctx := tmuxManager(t)
	id := "aaaaaaaaaaaaaaaa"
	if _, err := m.Open(ctx, id); err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })

	s1, s2 := "bbbbbbbbbbbbbbbb", "cccccccccccccccc"
	var viewers []*viewer
	for _, s := range []string{s1, s2} {
		if _, err := m.Attach(ctx, id, s, 80, 24); err != nil {
			t.Fatalf("Attach: %s", err)
		}
		t.Cleanup(func() { m.Detach(s) })
		viewers = append(viewers, watch(m, s))
	}
	if err := m.Write(s1, []byte("echo shared-$((33*3))\n")); err != nil {
		t.Fatalf("Write: %s", err)
	}
	for _, v := range viewers {
		v.waitFor(t, "shared-99")
	}
}

// TestTmuxCloseKillsSession proves close ends the tmux session: the
// list empties and reattach fails unknown.
func TestTmuxCloseKillsSession(t *testing.T) {
	m, ctx := tmuxManager(t)
	id := "dddddddddddddddd"
	if _, err := m.Open(ctx, id); err != nil {
		t.Fatalf("Open: %s", err)
	}
	terms, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	if len(terms) != 1 || terms[0].Term != id {
		t.Fatalf("List = %+v, want the one session", terms)
	}
	if err := m.Close(ctx, id); err != nil {
		t.Fatalf("Close: %s", err)
	}
	terms, err = m.List(ctx)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	if len(terms) != 0 {
		t.Fatalf("List = %+v, want empty", terms)
	}
	if _, err := m.Attach(ctx, id, "eeeeeeeeeeeeeeee", 80, 24); err == nil {
		t.Fatal("Attach to a closed session succeeded")
	}
	// Closing again succeeds: deletes are idempotent.
	if err := m.Close(ctx, id); err != nil {
		t.Fatalf("second Close: %s", err)
	}
}

// TestTmuxEnvScrubbed proves no DEVCULES_* variable reaches a shell:
// the runner exports its token file path, and shells must never see
// it. TERM is pinned for tmux clients.
func TestTmuxEnvScrubbed(t *testing.T) {
	t.Setenv("DEVCULES_TEST_SENTINEL", "must-not-leak")
	m, ctx := tmuxManager(t)
	id := "ffffffffffffffff"
	if _, err := m.Open(ctx, id); err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })
	// Count DEVCULES_* variables inside the shell itself: a screenful
	// of printenv can scroll past the redraw. The arithmetic keeps the
	// typed command's echo from matching.
	out := readUntil(t, m, ctx, id, "ffffffff00000000", "echo dv=$(printenv | grep -c ^DEVCULES_)-$((1+1))\n", "-2\r")
	if !strings.Contains(out, "dv=0-2") {
		t.Fatalf("shell environment leaks DEVCULES_*: %q", out)
	}
}

// TestTmuxMaxTerminals proves the per-runner cap: the 17th open fails
// while the first 16 succeed.
func TestTmuxMaxTerminals(t *testing.T) {
	m, ctx := tmuxManager(t)
	var ids []string
	for range 17 {
		ids = append(ids, terminals.NewID())
	}
	for _, id := range ids[:16] {
		if _, err := m.Open(ctx, id); err != nil {
			t.Fatalf("Open: %s", err)
		}
		t.Cleanup(func() { _ = m.Close(ctx, id) })
	}
	if _, err := m.Open(ctx, ids[16]); err == nil {
		t.Fatal("17th Open succeeded past the cap")
	} else if !strings.Contains(err.Error(), "at most") {
		t.Fatalf("17th Open = %q, want the cap with next steps", err)
	}
}

// TestTmuxOpenInProject starts a shell in a directory and finds the
// project tag and path in the list.
func TestTmuxOpenInProject(t *testing.T) {
	m, ctx := tmuxManager(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("in-project-dir\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "00000000000000aa"
	if _, err := m.OpenIn(ctx, id, pty.Place{Dir: dir, Project: "p_shop1", Name: "api server"}); err != nil {
		t.Fatalf("OpenIn: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })
	readUntil(t, m, ctx, id, "00000000000000bb", "cat marker.txt\n", "in-project-dir")
	terms, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %s", err)
	}
	found := false
	for _, ti := range terms {
		if ti.Term == id {
			found = true
			if ti.Project != "p_shop1" || ti.Dir == "" || ti.Name != "api server" {
				t.Fatalf("listed = %+v, want project p_shop1, its dir and its name", ti)
			}
		}
	}
	if !found {
		t.Fatalf("terminal %s not listed in %+v", id, terms)
	}
	if _, err := m.OpenIn(ctx, "00000000000000cc", pty.Place{Dir: filepath.Join(dir, "nope")}); err == nil || err.Error() != "directory not found" {
		t.Fatalf("missing dir err = %v", err)
	}
}
