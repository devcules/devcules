//go:build unix

package registry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// b2Grandchild starts a stdio "server" that is a grandchild of the
// bash -ic wrapper (an inner shell running sleep, like npx running
// node) and returns the session plus the unique pattern to pgrep for.
func b2Grandchild(t *testing.T, timeout time.Duration) (*stdioSession, string) {
	t.Helper()
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not installed")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	// A per-run duration so parallel runs never see each other's sleep.
	secs := fmt.Sprintf("77.%d", time.Now().UnixNano()%1_000_000+int64(os.Getpid()%1000))
	s, err := dialStdio(context.Background(), "bash", []string{"-c", "sleep " + secs + "; true"}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	pattern := "sleep " + secs
	b2WaitFor(t, pattern, true)
	return s, pattern
}

// b2Running reports whether a process matching pattern is alive.
func b2Running(pattern string) bool {
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return strings.TrimSpace(string(out)) != ""
}

func b2WaitFor(t *testing.T, pattern string, alive bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b2Running(pattern) == alive {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if alive {
		t.Fatalf("%q never started", pattern)
	}
	_ = exec.Command("pkill", "-f", pattern).Run()
	t.Fatalf("%q still running: the inspected server outlived its session", pattern)
}

// TestStdioCloseKillsServerTree: Close must take the whole server down,
// not only the bash wrapper (GOSEC-16).
func TestStdioCloseKillsServerTree(t *testing.T) {
	s, pattern := b2Grandchild(t, time.Minute)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b2WaitFor(t, pattern, false)
}

// TestStdioTimeoutKillsServerTree: the dial timeout firing (no Close
// yet) must also take the whole server down.
func TestStdioTimeoutKillsServerTree(t *testing.T) {
	s, pattern := b2Grandchild(t, 500*time.Millisecond)
	defer func() { _ = s.Close() }()
	b2WaitFor(t, pattern, false)
}

// TestStdioCloseTwiceKillsOnce: a second Close (sequential or
// concurrent) is a no-op. After the first Close reaped bash its pid may
// be reused, so a second kill(-pid) could take down an unrelated group:
// the test stands a live decoy group in for the recycled pid.
func TestStdioCloseTwiceKillsOnce(t *testing.T) {
	s, pattern := b2Grandchild(t, time.Minute)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b2WaitFor(t, pattern, false)

	decoyPattern := fmt.Sprintf("sleep 78.%d", time.Now().UnixNano()%1_000_000)
	decoy := exec.Command("bash", "-c", decoyPattern+"; true")
	decoy.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := decoy.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-decoy.Process.Pid, syscall.SIGKILL)
		_ = decoy.Wait()
	}()
	b2WaitFor(t, decoyPattern, true)
	s.cmd.Process = decoy.Process // the recycled pid

	done := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			_ = s.Close()
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if !b2Running(decoyPattern) {
		t.Fatal("a second Close killed the process group that reused the pid")
	}
}
