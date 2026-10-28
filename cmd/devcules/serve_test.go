package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/bus/memory"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// fakeListener blocks in Accept until closed, so the server runs without
// a real port.
type fakeListener struct {
	closed chan struct{}
	once   sync.Once
}

func (f *fakeListener) Accept() (net.Conn, error) {
	<-f.closed
	return nil, net.ErrClosed
}

func (f *fakeListener) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}
}

// closeSpy records Close while delegating everything else.
type closeSpy struct {
	store.Store
	closed bool
}

func (s *closeSpy) Close() error {
	s.closed = true
	return s.Store.Close()
}

// serveFixture wires runServe with fakes and returns the exit code,
// output, the store spy and a subscribed channel that Close must close.
func serveFixture(t *testing.T, cfg config.Config, open storeOpener) (int, string, string, *closeSpy, <-chan []byte) {
	t.Helper()
	ln := &fakeListener{closed: make(chan struct{})}
	sigs := make(chan os.Signal, 1)
	b := memory.New()
	sub, err := b.Subscribe(context.Background(), "t")
	if err != nil {
		t.Fatalf("Subscribe: %s", err)
	}
	var spy *closeSpy
	opener := open
	if opener == nil {
		opener = func(path string) (store.Store, error) {
			s, err := sqlite.Open(context.Background(), path)
			if err != nil {
				return nil, err
			}
			spy = &closeSpy{Store: s}
			return spy, nil
		}
	}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runServe(cfg, &stdout, &stderr, ln, sigs, opener, b) }()
	sigs <- syscall.SIGTERM
	select {
	case code := <-done:
		return code, stdout.String(), stderr.String(), spy, sub
	case <-time.After(10 * time.Second):
		t.Fatal("runServe did not exit after SIGTERM")
		return 0, "", "", nil, nil
	}
}

// jsonLines parses stdout as one JSON object per line.
func jsonLines(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(strings.TrimSpace(stdout)) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("stdout line is not JSON: %q (%s)", line, err)
		}
		out = append(out, obj)
	}
	return out
}

func serveConfig(dir string) config.Config {
	return config.Config{
		Listen:    "127.0.0.1:1",
		DataDir:   dir,
		ServeURL:  "http://serve:7659",
		VaultKey:  "TOPSECRET-SHOULD-NEVER-LOG",
		Workspace: "/workspace",
	}
}

func TestRunServeShutdown(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr, spy, sub := serveFixture(t, serveConfig(dir), nil)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q, want clean drain", code, stderr)
	}
	if spy == nil || !spy.closed {
		t.Fatal("store was not closed on shutdown")
	}
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("bus subscriber got a message, want the channel closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bus subscriber channel never closed by shutdown")
	}
	lines := jsonLines(t, stdout)
	if len(lines) != 4 {
		t.Fatalf("got %d log lines, want token-created/listening/shutdown/stopped", len(lines))
	}
	if lines[0]["msg"] != "runner token file created" {
		t.Fatalf("first line = %v, want the token mint note", lines[0])
	}
	if lines[0]["path"] != dir+string(filepath.Separator)+"runner.token" {
		t.Fatalf("token path = %v, want the default file in the data dir", lines[0])
	}
	if lines[1]["addr"] != "127.0.0.1:1" {
		t.Fatalf("second line = %v, want the listen address and nothing else", lines[1])
	}
	for _, line := range lines {
		for _, v := range line {
			if s, ok := v.(string); ok && strings.Contains(s, "TOPSECRET") {
				t.Fatalf("log line leaks a secret: %v", line)
			}
		}
	}
	// The minted token value appears neither in the logs nor on stderr:
	// only its path is logged.
	raw, err := os.ReadFile(dir + string(filepath.Separator) + "runner.token")
	if err != nil {
		t.Fatalf("read minted token: %s", err)
	}
	minted := strings.TrimSpace(string(raw))
	if minted == "" {
		t.Fatal("minted token file is empty")
	}
	if strings.Contains(stdout, minted) || strings.Contains(stderr, minted) {
		t.Fatal("output leaks the minted runner token value")
	}
	// First start bootstraps the admin and prints the one-time
	// password as a single plain line on stderr.
	line := strings.TrimSpace(stderr)
	const prefix = "Admin password (shown once): "
	if !strings.HasPrefix(line, prefix) || strings.Contains(line[len(prefix):], " ") || line[len(prefix):] == "" {
		t.Fatalf("stderr = %q, want one %q line with the password", stderr, prefix)
	}
}

func TestRunServeSecondStartSilent(t *testing.T) {
	dir := t.TempDir()
	if code, _, _, _, _ := serveFixture(t, serveConfig(dir), nil); code != exitOK {
		t.Fatal("first start failed")
	}
	// The admin exists now: no password is printed again.
	code, _, stderr, _, _ := serveFixture(t, serveConfig(dir), nil)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q, want clean drain", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty once the admin exists", stderr)
	}
}

// brokenUsers fails the admin lookup so the bootstrap error path is
// reachable without a broken disk.
type brokenUsers struct {
	store.UsersRepo
}

func (b brokenUsers) PasswordHash(context.Context, string) (string, error) {
	return "", errors.New("users unavailable")
}

type brokenUsersStore struct {
	store.Store
}

func (b brokenUsersStore) Users() store.UsersRepo {
	return brokenUsers{UsersRepo: b.Store.Users()}
}

func TestRunServeBootstrapFailure(t *testing.T) {
	open := func(path string) (store.Store, error) {
		s, err := sqlite.Open(context.Background(), path)
		if err != nil {
			return nil, err
		}
		return brokenUsersStore{Store: s}, nil
	}
	code, _, stderr, _, _ := serveFixture(t, serveConfig(t.TempDir()), open)
	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "cannot check for admin user") {
		t.Fatalf("stderr = %q, want the bootstrap error", stderr)
	}
}

func TestRunServePostgresRefused(t *testing.T) {
	cfg := serveConfig(t.TempDir())
	cfg.DatabaseURL = "postgres://u@h/db"
	code, _, stderr, _, _ := serveFixture(t, cfg, nil)
	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	want := "DEVCULES_DATABASE_URL is set: the Postgres store does not exist yet (stretch goal); unset it to use SQLite\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestRunServeBadDataDir(t *testing.T) {
	blocker := t.TempDir() + "/blocker"
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := serveConfig(blocker)
	ln := &fakeListener{closed: make(chan struct{})}
	sigs := make(chan os.Signal, 1)
	var stdout, stderr bytes.Buffer
	code := runServe(cfg, &stdout, &stderr, ln, sigs, openSQLite, memory.New())
	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "cannot create data dir") || !strings.Contains(stderr.String(), "DEVCULES_DATA_DIR") {
		t.Fatalf("stderr = %q, want next steps", stderr.String())
	}
}

func TestRunBlockingRoleRunner(t *testing.T) {
	cfg := serveConfig(t.TempDir())
	cfg.VaultKey = ""
	cfg.RunnerToken = "test-token"
	sigs := make(chan os.Signal, 1)
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runBlockingRole(cfg, "runner", &stdout, &stderr, sigs) }()
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
}
