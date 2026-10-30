package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/store"
)

const adminUsage = "usage: devcules admin reset-password [--user <name>]"

// runAdmin serves `devcules admin reset-password [--user NAME]`: a new
// random password for the user (admin by default), printed once to
// stderr, every session of that user signed out, and the reset
// audited without the password. It works on serve's database in place
// and can run while serve is up: SQLite's WAL lets both write, and
// serve checks passwords and sessions on every request.
func runAdmin(cfg config.Config, args []string, stderr io.Writer, open storeOpener) int {
	if len(args) == 0 || args[0] != "reset-password" {
		return usage(stderr, "%s", adminUsage)
	}
	fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	user := fs.String("user", auth.AdminUsername, "the user whose password to reset")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return usage(stderr, "%s", adminUsage)
	}
	if cfg.DatabaseURL != "" {
		fmt.Fprintln(stderr, "DEVCULES_DATABASE_URL is set: the Postgres store does not exist yet (stretch goal); unset it to use SQLite")
		return exitError
	}
	// Never create a database here: a reset against the wrong folder
	// must fail, not make an empty install.
	db := filepath.Join(cfg.DataDir, "devcules.db")
	if _, err := os.Stat(db); err != nil {
		fmt.Fprintf(stderr, "no Devcules database at %s: set DEVCULES_DATA_DIR to serve's data folder\n", db)
		return exitError
	}
	s, err := open(db)
	if err != nil {
		fmt.Fprintln(stderr, busyHint(err))
		return exitError
	}
	defer s.Close()
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	password, signedOut, err := auth.New(s).ResetPassword(ctx, *user)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fmt.Fprintf(stderr, "no user %q in %s\n", *user, db)
		return exitError
	case err != nil:
		fmt.Fprintln(stderr, busyHint(err))
		return exitError
	}
	// The audit names the user and how many sessions ended, never the
	// password. A failed record is reported, not fatal: the reset is done.
	if _, aerr := audit.New(s, nil).Record(ctx, audit.Event{Actor: "cli", Action: audit.PasswordReset, Target: *user,
		Detail: map[string]string{"sessions_ended": fmt.Sprint(signedOut)}}); aerr != nil {
		fmt.Fprintln(stderr, "password reset, but the audit entry failed: "+aerr.Error())
	}
	fmt.Fprintf(stderr, "New password for %s (shown once): %s\n", *user, password)
	fmt.Fprintf(stderr, "%d session(s) signed out. Sign in with this password, then add a passkey in Settings.\n", signedOut)
	return exitOK
}

// busyHint names the fix when SQLite stayed locked past its timeout:
// some writer held the database the whole time.
func busyHint(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "database is locked") || strings.Contains(msg, "SQLITE_BUSY") {
		return "the database stayed locked: stop serve first, then run this again (" + msg + ")"
	}
	return msg
}
