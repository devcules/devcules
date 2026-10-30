package pty_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/terminals/pty"
)

// TestMain lets the test binary stand in for `devcules reap` (agent
// launch scripts run the reaper with the launch dir) and for a
// long-running child: macOS hides a platform binary's environment
// (/bin/sleep's), so the children the reaper must find are this
// binary.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "sleep" {
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == "reap" {
		if _, err := pty.Reap(os.Args[2]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// waitPid reads the pid the agent wrote to mark.
func waitPid(t *testing.T, mark string) int {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(mark); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
				return pid
			}
		}
	}
	t.Fatal("the agent never wrote its child's pid")
	return 0
}

func waitGone(t *testing.T, pid int, what string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("%s (pid %d) outlived its launch", what, pid)
}

// TestCloseStopsWhatTheAgentDetached: closing an agent terminal stops
// a child in its own process group (the pane's tree) and an orphan
// whose environment points into the launch dir (the sweep).
func TestCloseStopsWhatTheAgentDetached(t *testing.T) {
	m, ctx := tmuxManager(t)
	id := "00000000000000c1"
	dir := m.LaunchDir(id)
	tmp := t.TempDir()
	group, orphan := filepath.Join(tmp, "group.pid"), filepath.Join(tmp, "orphan.pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Both ignore SIGHUP, as a daemon does: only the reaper stops them.
	l := pty.Launch{
		Cmd: []string{"bash", "-c", `set -m; nohup "$3" sleep >/dev/null 2>&1 & echo $! > "$1"; set +m; ( nohup "$3" sleep >/dev/null 2>&1 & echo $! > "$2" ); sleep 302`, "agent", group, orphan, exe},
		Env: map[string]string{"AGENT_HOME": filepath.Join(dir, "home")},
	}
	if _, err := m.OpenAgent(ctx, id, "testagent", l); err != nil {
		t.Fatalf("OpenAgent: %s", err)
	}
	gpid, opid := waitPid(t, group), waitPid(t, orphan)
	if err := m.Close(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitGone(t, gpid, "the child in its own process group")
	waitGone(t, opid, "the orphan pointing into the launch dir")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("launch dir still there: %v", err)
	}
}

// TestAgentExitReapsLeftovers: when the agent itself exits, the
// launch script's reaper stops its orphan, and the terminal stays a
// shell.
func TestAgentExitReapsLeftovers(t *testing.T) {
	m, ctx := tmuxManager(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m.SetReaper(exe)
	id := "00000000000000c2"
	dir := m.LaunchDir(id)
	mark := filepath.Join(t.TempDir(), "orphan.pid")
	l := pty.Launch{
		Cmd: []string{"bash", "-c", `( nohup "$2" sleep >/dev/null 2>&1 & echo $! > "$1" ); exit 3`, "agent", mark, exe},
		Env: map[string]string{"AGENT_HOME": filepath.Join(dir, "home")},
	}
	if _, err := m.OpenAgent(ctx, id, "testagent", l); err != nil {
		t.Fatalf("OpenAgent: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })
	waitGone(t, waitPid(t, mark), "the agent's orphan")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the launch dir was not removed")
		}
	}
	terms, err := m.List(ctx)
	if err != nil || len(terms) != 1 {
		t.Fatalf("List = %+v %v: the terminal must stay, as a shell", terms, err)
	}
}
