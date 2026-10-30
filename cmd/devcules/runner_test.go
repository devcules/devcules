package main

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/config"
)

// syncBuffer is an io.Writer that lets a test read the log while a
// server goroutine writes to it (bytes.Buffer races).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunLocalEndToEnd boots the one-command host desk: serve and the
// host runner against a loopback listener, in its own data dir, no env
// var set. The picker sees a link named for the computer, not a
// container, and a SIGTERM unwinds both halves cleanly.
func TestRunLocalEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("boots serve, tmux and a link")
	}
	// A never-used port avoids colliding with a dev desk run by hand.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := ln.Addr().String()
	_ = ln.Close() // runLocal binds it itself

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMUX_TMPDIR", filepath.Join(home, "tmux"))

	cfg := serveConfig(home)
	cfg.Listen = listen
	cfg.ServeURL = ""    // runLocal fills loopback from listen
	cfg.PublicURL = ""   // and the host's own name
	cfg.RunnerToken = "" // minted by serve, read by the runner half
	cfg.RunnerTokenFile = ""
	cfg.Workspace = "" // runLocal lands it in home

	sigs := make(chan os.Signal, 2)
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- runLocal(cfg, &stdout, &stderr, sigs, openSQLite) }()

	deadline := time.Now().Add(20 * time.Second)
	// Wait for the link itself: the runner logs "runner connecting" with
	// the host label only after the token hand-off finished.
	for {
		if err := healthz(listen); err == nil && strings.Contains(stdout.String(), "host (") {
			break
		}
		if time.Now().After(deadline) {
			sigs <- syscall.SIGTERM
			<-done
			t.Fatalf("desk never linked the host\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	sigs <- syscall.SIGTERM
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit = %d, want %d; stderr: %s", code, exitOK, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the desk did not drain after SIGTERM")
	}

	// The link carried the host label, and serve minted the token file
	// it shares with the runner half (the compose link volume in one
	// process — nothing left in the test's env).
	out := stdout.String()
	if !strings.Contains(out, `name":"host (`) || strings.Contains(out, "container") {
		t.Errorf("stdout lacks the host link label: %s", out)
	}
	if raw, err := os.ReadFile(filepath.Join(home, "runner.token")); err != nil || strings.TrimSpace(string(raw)) == "" {
		t.Errorf("runner token never minted under %s: %v", home, err)
	}
}

// healthz is the loopback readiness check the desk offers.
func healthz(listen string) error {
	port := listen[strings.LastIndex(listen, ":")+1:]
	resp, err := http.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}

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
	go func() { done <- runRunner(cfg, "", &stdout, &stderr, sigs) }()
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
	if code := runRunner(cfg, "", &stdout, &stderr, sigs); code != exitError {
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
	if code := runRunner(cfg, "", &stdout, &stderr, sigs); code != exitError {
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
