package main

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/trace"
)

func TestRunAudit(t *testing.T) {
	dir := t.TempDir()
	cfg := serveConfig(dir)
	var out, errOut bytes.Buffer
	reset := func() { out.Reset(); errOut.Reset() }
	audits := func(args ...string) int {
		reset()
		return runAudit(cfg, args, strings.NewReader(""), &out, &errOut, openSQLite)
	}

	for _, args := range [][]string{nil, {"spin"}, {"export", "extra"}, {"export", "-after", "x"}, {"verify", "a", "b"}} {
		if code := audits(args...); code != exitUsage {
			t.Fatalf("%v: exit = %d, want usage", args, code)
		}
	}

	// An empty log verifies, and the export records itself.
	if code := audits("verify"); code != exitOK || !strings.Contains(out.String(), "0 entries") {
		t.Fatalf("verify empty: %d %q %q", code, out.String(), errOut.String())
	}
	if code := audits("export"); code != exitOK || !strings.Contains(out.String(), `"action":"audit.export"`) || !strings.Contains(errOut.String(), "exported 1 entries") {
		t.Fatalf("export: %d %q %q", code, out.String(), errOut.String())
	}
	exported := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(exported, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := audits("export", "-after", "1"); code != exitOK || !strings.Contains(out.String(), `"seq":2`) || strings.Contains(out.String(), `"seq":1,`) {
		t.Fatalf("export -after: %d %q", code, out.String())
	}
	if code := audits("verify", exported); code != exitOK || !strings.Contains(out.String(), "1 entries") {
		t.Fatalf("verify file: %d %q %q", code, out.String(), errOut.String())
	}
	reset()
	if code := runAudit(cfg, []string{"verify", "-"}, strings.NewReader(`{"seq":2}`), &out, &errOut, openSQLite); code != exitError || !strings.Contains(errOut.String(), "missing") {
		t.Fatalf("verify stdin: %d %q", code, errOut.String())
	}
	if code := audits("verify", filepath.Join(dir, "nope.jsonl")); code != exitError {
		t.Fatalf("verify missing file = %d", code)
	}

	// Tampering with the database shows in verify, with the entry.
	db, err := sql.Open("sqlite", filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TRIGGER audit_log_no_update`, `UPDATE audit_log SET actor = 'mallory' WHERE seq = 1`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if code := audits("verify"); code != exitError || !strings.Contains(errOut.String(), "broken at entry 1") {
		t.Fatalf("verify tampered: %d %q", code, errOut.String())
	}
	var logs bytes.Buffer
	s, err := openSQLite(filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	setupAudit(context.Background(), cfg, s, slog.New(slog.NewJSONHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "audit chain broken") {
		t.Fatalf("serve's check = %q, want the broken chain logged", logs.String())
	}

	pg := cfg
	pg.DatabaseURL = "postgres://db/x"
	reset()
	if code := runAudit(pg, []string{"verify"}, nil, &out, &errOut, openSQLite); code != exitError {
		t.Fatalf("postgres = %d", code)
	}
	reset()
	broken := func(string) (store.Store, error) { return nil, os.ErrPermission }
	if code := runAudit(cfg, []string{"export"}, nil, &out, &errOut, broken); code != exitError {
		t.Fatalf("unopenable store = %d", code)
	}
}

func TestVaultRotationIsAudited(t *testing.T) {
	dir := t.TempDir()
	cfg := serveConfig(dir)
	cfg.VaultKey = ""
	if code, _, stderr, _, _ := serveFixture(t, cfg, nil); code != exitOK {
		t.Fatalf("serve: %s", stderr)
	}
	var out, errOut bytes.Buffer
	if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitOK {
		t.Fatalf("rotate: %s", errOut.String())
	}
	out.Reset()
	if code := runAudit(cfg, []string{"export"}, nil, &out, &errOut, openSQLite); code != exitOK || !strings.Contains(out.String(), `"action":"vault.rotate","detail":{"from"`) {
		t.Fatalf("export = %q", out.String())
	}
}

func TestPruneTraces(t *testing.T) {
	s, err := openSQLite(filepath.Join(t.TempDir(), "devcules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	traces := trace.New(s, nil)
	traces.Trace(context.Background(), store.TraceCall{LaunchID: "old", At: time.Now().Add(-40 * 24 * time.Hour)})
	logs := &lockedBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { pruneTracesDaily(ctx, time.Millisecond, traces, 30, log); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "old traces pruned") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(logs.String(), `"count":1`) {
		t.Fatalf("logs = %q", logs.String())
	}
}

// lockedBuffer is a bytes.Buffer safe to read while a logger writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
