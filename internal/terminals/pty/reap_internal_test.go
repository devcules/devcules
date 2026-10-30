package pty

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestOwnedByPathOnly(t *testing.T) {
	roots := []string{"/tmp/x/launch-0123456789abcdef", "/private/tmp/x/launch-0123456789abcdef"}
	for _, c := range []struct {
		exe, cwd string
		env      []string
		want     bool
	}{
		{exe: "/private/tmp/x/launch-0123456789abcdef/codex/bin/codex", want: true},
		{cwd: "/tmp/x/launch-0123456789abcdef", want: true},
		{env: []string{"HOME=/Users/k", "CODEX_HOME=/tmp/x/launch-0123456789abcdef/codex"}, want: true},
		{exe: "/usr/bin/codex", cwd: "/Users/k/shop", env: []string{"CODEX_HOME=/Users/k/.codex"}},
		// A sibling launch, and a prefix that is not a path boundary.
		{exe: "/tmp/x/launch-0123456789abcdee/codex", env: []string{"A=/tmp/x/launch-0123456789abcdef0"}},
		{env: []string{"NOEQUALS"}},
	} {
		if got := owned(roots, c.exe, c.cwd, c.env); got != c.want {
			t.Errorf("owned(%q, %q, %q) = %v, want %v", c.exe, c.cwd, c.env, got, c.want)
		}
	}
	for dir, want := range map[string]bool{
		"/run/devcules-tmux/launch-0123456789abcdef":    true,
		"/run/devcules-tmux/launch-0123456789ABCDEF":    false,
		"run/launch-0123456789abcdef":                   false,
		"/run/devcules-tmux/launch-0123456789abcdef/..": false,
		"/":           false,
		"/Users/k":    false,
		"/tmp/launch": false,
	} {
		if ValidLaunchDir(dir) != want {
			t.Errorf("ValidLaunchDir(%q) != %v", dir, want)
		}
	}
	if r := launchRoots("/var/gone/launch-0123456789abcdef"); !slices.Contains(r, "/private/var/gone/launch-0123456789abcdef") {
		t.Errorf("launchRoots = %v, want the macOS /private form", r)
	}
	if _, err := Reap("/etc"); err == nil {
		t.Fatal("Reap swept a dir that is not a launch dir")
	}
}

func TestTreeOfAndAncestors(t *testing.T) {
	self := os.Getpid()
	table := []proc{
		{pid: 1, ppid: 0, pgid: 1},
		{pid: 50, ppid: 1, pgid: 50},    // tmux server
		{pid: 100, ppid: 50, pgid: 100}, // the pane
		{pid: 101, ppid: 100, pgid: 100},
		{pid: 102, ppid: 101, pgid: 102}, // a daemon in its own group
		{pid: 103, ppid: 102, pgid: 50},  // joined someone else's group
		{pid: 200, ppid: 50, pgid: 200},  // another pane
		{pid: self, ppid: 100, pgid: 100},
	}
	tr := treeOf(table, []int{100, 1})
	if !slices.Equal(tr.pids, []int{100, 101, 102, 103}) || !slices.Equal(tr.groups, []int{100, 102}) {
		t.Fatalf("treeOf = %+v, want the pane's subtree and only groups it leads", tr)
	}
	if a := ancestors(table, 103); !a[103] || !a[102] || !a[100] || !a[50] || a[1] || a[200] {
		t.Fatalf("ancestors = %v", a)
	}
}

// start runs a sleeping child that the test owns: this test binary
// in its "sleep" mode (see TestMain).
func start(t *testing.T, path, dir string, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(path, "sleep")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func gone(cmd *exec.Cmd) bool {
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return syscall.Kill(cmd.Process.Pid, 0) != nil
	}
}

func TestReapStopsProcessesInTheLaunchDir(t *testing.T) {
	if _, err := listProcs(); err != nil {
		t.Skip(err.Error())
	}
	sleep, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "launch-00000000000000ee")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A copy of the sleeper inside the dir, like Codex's daemon binary.
	src, _ := os.Open(sleep)
	dst, _ := os.OpenFile(filepath.Join(dir, "bin", "sleeper"), os.O_CREATE|os.O_WRONLY, 0o700)
	_, _ = io.Copy(dst, src)
	src.Close()
	dst.Close()

	byEnv := start(t, sleep, "", "CODEX_HOME="+filepath.Join(dir, "codex"))
	byCwd := start(t, sleep, dir)
	byExe := start(t, filepath.Join(dir, "bin", "sleeper"), "")
	other := start(t, sleep, "", "CODEX_HOME="+dir+"x")

	hit, err := Reap(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, cmd := range map[string]*exec.Cmd{"env": byEnv, "cwd": byCwd, "exe": byExe} {
		if !slices.Contains(hit, cmd.Process.Pid) || !gone(cmd) {
			t.Errorf("the process matched by %s survived (hit %v)", name, hit)
		}
	}
	if slices.Contains(hit, other.Process.Pid) || syscall.Kill(other.Process.Pid, 0) != nil {
		t.Error("an unrelated process was stopped")
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	// A process that ignores SIGTERM gets SIGKILL after the grace.
	cmd := exec.Command("bash", "-c", "trap '' TERM; sleep 300 & wait")
	if err := cmd.Start(); err != nil {
		t.Skip(err.Error())
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	time.Sleep(200 * time.Millisecond)
	stop([]int{cmd.Process.Pid}, nil, func(int) bool { return true })
	if !gone(cmd) {
		t.Fatal("a process ignoring SIGTERM survived")
	}
	stop(nil, nil, nil)
}
