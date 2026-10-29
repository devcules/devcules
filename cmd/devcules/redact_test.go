package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

func TestSetupRedactKeepsKey(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	k, _ := vault.NewKey()
	v, err := vault.Open(ctx, s, k)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	r1, _, err := setupRedact(ctx, v, s, log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "redaction key created") {
		t.Fatalf("first start: %s", logs.String())
	}
	logs.Reset()
	r2, settings, err := setupRedact(ctx, v, s, log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "redaction key created") {
		t.Fatal("second start minted a new key")
	}
	// Same key, same placeholders across restarts.
	p := redact.Policy{redact.Secret: redact.Off, redact.Email: redact.Mask, redact.Phone: redact.Off, redact.Card: redact.Off, redact.IPv4: redact.Off}
	a, _, _ := r1.NewTestSession(p).Redact("ada@example.org")
	b, _, _ := r2.NewTestSession(p).Redact("ada@example.org")
	if a != b {
		t.Fatalf("placeholders changed across restarts: %q vs %q", a, b)
	}
	if settings.Policy()[redact.Secret] != redact.Mask {
		t.Fatalf("policy = %v", settings.Policy())
	}
}

func TestSetupRedactBadSavedPolicy(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	k, _ := vault.NewKey()
	v, _ := vault.Open(ctx, s, k)
	if err := s.Meta().Set(ctx, "redact.policy", "{broken"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := setupRedact(ctx, v, s, slog.New(slog.NewJSONHandler(io.Discard, nil))); err == nil || !strings.Contains(err.Error(), "redact.policy") {
		t.Fatalf("err = %v, want the saved policy named", err)
	}
}

func TestRunServeRefusesBadRedactPolicy(t *testing.T) {
	dir := t.TempDir()
	s, err := sqlite.Open(context.Background(), filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Meta().Set(context.Background(), "redact.policy", `{"SECRET":"loud"}`)
	s.Close()
	code, _, stderr, _, _ := serveFixture(t, serveConfig(dir), nil)
	if code != exitError || !strings.Contains(stderr, "unknown mode") {
		t.Fatalf("exit = %d, stderr = %q, want a refusal naming the bad mode", code, stderr)
	}
}
