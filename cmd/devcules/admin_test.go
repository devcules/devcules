package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/store/sqlite"
)

var newPasswordRE = regexp.MustCompile(`New password for admin \(shown once\): (\S+)`)

// TestAdminResetPassword resets the admin password on a data folder
// that serve still holds open: the new password signs in, the reset is
// audited without it, and nothing about it reaches stdout.
func TestAdminResetPassword(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "devcules.db")
	served, err := sqlite.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	svc := auth.New(served)
	old, _, err := svc.EnsureAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := svc.SignIn(ctx, auth.AdminUsername, old, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := runAdmin(config.Config{DataDir: dir}, []string{"reset-password"}, &stderr, openSQLite); code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	m := newPasswordRE.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("stderr = %q, want the new password once", stderr.String())
	}
	fresh := m[1]
	if !strings.Contains(stderr.String(), "1 session(s) signed out") {
		t.Fatalf("stderr = %q, want the signed-out count", stderr.String())
	}
	// serve's own connection sees the change at once.
	if _, err := svc.Authenticate(ctx, cookie); err == nil {
		t.Fatal("the old session still works")
	}
	if _, err := svc.SignIn(ctx, auth.AdminUsername, fresh, "192.0.2.1"); err != nil {
		t.Fatalf("the new password: %s", err)
	}
	var found bool
	var buf bytes.Buffer
	if _, err := audit.New(served, nil).Export(ctx, &buf, 0); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, audit.PasswordReset) {
			found = true
		}
	}
	if !found || strings.Contains(buf.String(), fresh) {
		t.Fatalf("audit = %s, want auth.password_reset and never the password", buf.String())
	}
}

func TestAdminResetPasswordRefusals(t *testing.T) {
	var stderr bytes.Buffer
	empty := t.TempDir()
	if code := runAdmin(config.Config{DataDir: empty}, []string{"reset-password"}, &stderr, openSQLite); code != exitError || !strings.Contains(stderr.String(), "no Devcules database") {
		t.Fatalf("missing db: exit %d, %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(empty, "devcules.db")); !os.IsNotExist(err) {
		t.Fatal("a reset against an empty folder created a database")
	}

	dir := t.TempDir()
	s, err := sqlite.Open(context.Background(), filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	stderr.Reset()
	if code := runAdmin(config.Config{DataDir: dir}, []string{"reset-password", "--user", "nobody"}, &stderr, openSQLite); code != exitError || !strings.Contains(stderr.String(), `no user "nobody"`) {
		t.Fatalf("unknown user: exit %d, %q", code, stderr.String())
	}
	for _, args := range [][]string{{}, {"frobnicate"}, {"reset-password", "extra"}} {
		stderr.Reset()
		if code := runAdmin(config.Config{DataDir: dir}, args, &stderr, openSQLite); code != exitUsage {
			t.Errorf("args %q: exit %d, want usage", args, code)
		}
	}
	stderr.Reset()
	if code := runAdmin(config.Config{DataDir: dir, DatabaseURL: "postgres://x"}, []string{"reset-password"}, &stderr, openSQLite); code != exitError {
		t.Fatalf("postgres: exit %d", code)
	}
	if got := busyHint(errString("database is locked (5) (SQLITE_BUSY)")); !strings.Contains(got, "stop serve first") {
		t.Fatalf("busyHint = %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
