package main

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/config"
)

// TestRunRunnerIdle proves the runner logs its wait and exits on signal.
func TestRunRunnerIdle(t *testing.T) {
	cfg := config.Config{Listen: "127.0.0.1:1"}
	sigs := make(chan os.Signal, 1)
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runRunner(cfg, &stdout, &stderr, sigs) }()
	sigs <- syscall.SIGTERM
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit = %d, stderr = %q, want 0", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not exit after SIGTERM")
	}
	jsonLines(t, stdout.String())
	if !strings.Contains(stdout.String(), "M1.3") {
		t.Fatalf("stdout = %q, want the link wait note", stdout.String())
	}
}

// TestRunRunnerRefusesVaultKey proves the trust split at the runner entry.
func TestRunRunnerRefusesVaultKey(t *testing.T) {
	cfg := config.Config{VaultKey: "k"}
	sigs := make(chan os.Signal, 1)
	var stdout, stderr bytes.Buffer
	if code := runRunner(cfg, &stdout, &stderr, sigs); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "must not be set for runner") {
		t.Fatalf("stderr = %q, want the vault key refusal", stderr.String())
	}
}
