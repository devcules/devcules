package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/config"
)

// runnerConfig links at an address nothing answers: dial fails fast
// and the client backs off, so SIGTERM always lands cleanly.
func runnerConfig() config.Config {
	return config.Config{
		ServeURL:    "http://127.0.0.1:1",
		RunnerToken: "test-token",
		Workspace:   "/workspace",
	}
}

// TestRunRunnerConnects proves the runner starts its link loop and
// exits clean on signal, never logging the token.
func TestRunRunnerConnects(t *testing.T) {
	cfg := runnerConfig()
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
	if !strings.Contains(stdout.String(), "runner connecting") {
		t.Fatalf("stdout = %q, want the connecting line", stdout.String())
	}
	if strings.Contains(stdout.String(), "test-token") || strings.Contains(stderr.String(), "test-token") {
		t.Fatal("output leaks the runner token")
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

// TestRunRunnerMissingToken proves the runner fails with next steps
// instead of dialling anonymously: only serve mints the file.
func TestRunRunnerMissingToken(t *testing.T) {
	cfg := runnerConfig()
	cfg.RunnerToken = ""
	sigs := make(chan os.Signal, 1)
	var stdout, stderr bytes.Buffer
	if code := runRunner(cfg, &stdout, &stderr, sigs); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "runner token missing: set DEVCULES_RUNNER_TOKEN or DEVCULES_RUNNER_TOKEN_FILE") {
		t.Fatalf("stderr = %q, want next steps", stderr.String())
	}
}

// TestLinkURLTable proves the serve address maps to the link address,
// keeping any path prefix a reverse proxy adds.
func TestLinkURLTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr string
	}{
		{in: "http://serve:7659", want: "ws://serve:7659/link"},
		{in: "https://example.com", want: "wss://example.com/link"},
		{in: "http://h:1/prefix", want: "ws://h:1/prefix/link"},
		{in: "gopher://h", wantErr: `DEVCULES_SERVE_URL invalid: "gopher://h" (want an http(s) URL with a host)`},
		{in: "http://", wantErr: `DEVCULES_SERVE_URL invalid: "http://" (want an http(s) URL with a host)`},
		{in: "://bad", wantErr: `DEVCULES_SERVE_URL invalid: "://bad" (want an http(s) URL with a host)`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := linkURL(tc.in)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("linkURL(%q) = %q, %v; want error %q", tc.in, got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("linkURL(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("linkURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRunnerNameNonEmpty proves a link name always exists, even where
// the hostname is unavailable (the fallback keeps single-binary
// deploys working).
func TestRunnerNameNonEmpty(t *testing.T) {
	t.Parallel()
	if runnerName() == "" {
		t.Fatal("runnerName() is empty")
	}
}

// TestRunnerWorkspace: the runner starts shells in DEVCULES_WORKSPACE,
// not wherever it was launched from (the M1.4 code looked only at
// /workspace and then the current directory).
func TestRunnerWorkspace(t *testing.T) {
	ws := t.TempDir()
	if got := runnerWorkspace(ws); got != ws {
		t.Fatalf("runnerWorkspace(%q) = %q", ws, got)
	}
	wd, _ := os.Getwd()
	if got := runnerWorkspace(filepath.Join(ws, "missing")); got != wd {
		t.Fatalf("missing workspace = %q, want the current dir %q", got, wd)
	}
}
