package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/trace"
)

// setupAudit opens the audit log and the trace store, and checks the
// audit chain. A broken chain is logged loudly but does not stop
// serve: refusing to start would lock the owner out of the very UI
// that shows them what happened.
func setupAudit(ctx context.Context, cfg config.Config, s store.Store, log *slog.Logger) (*audit.Log, *trace.Store) {
	l := audit.New(s, log)
	n, err := l.Verify(ctx)
	var be *audit.BrokenError
	switch {
	case errors.As(err, &be):
		log.Error("audit chain broken: entries were changed or removed outside Devcules", "seq", be.Seq, "reason", be.Reason)
	case err != nil:
		log.Error("audit chain check failed", "err", err.Error())
	default:
		log.Info("audit chain verified", "entries", n)
	}
	return l, trace.New(s, log)
}

// pruneTraces drops traces past the retention.
func pruneTraces(ctx context.Context, traces *trace.Store, days int, log *slog.Logger) {
	if n, err := traces.Prune(ctx, days); err != nil && ctx.Err() == nil {
		log.Error("trace prune failed", "err", err.Error())
	} else if n > 0 {
		log.Info("old traces pruned", "count", n, "days", days)
	}
}

// pruneTracesDaily prunes once a day until ctx ends; serve prunes once
// itself at start.
func pruneTracesDaily(ctx context.Context, every time.Duration, traces *trace.Store, days int, log *slog.Logger) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pruneTraces(ctx, traces, days, log)
		}
	}
}

// recordRotation audits a vault key rotation by its key ids (never the
// keys). A failed record is reported, not fatal: the rotation is done.
func recordRotation(ctx context.Context, s store.Store, from, to string) {
	_, _ = audit.New(s, slog.New(slog.NewTextHandler(os.Stderr, nil))).Record(ctx,
		audit.Event{Actor: "cli", Action: audit.VaultRotate, Detail: map[string]string{"from": from, "to": to}})
}

// runAudit serves `devcules audit export [-after N]` (JSON lines to
// stdout) and `devcules audit verify [FILE|-]` (the database's chain,
// or an export's on its own).
func runAudit(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer, open storeOpener) int {
	if len(args) == 0 {
		return usage(stderr, "usage: devcules audit export [-after N] | devcules audit verify [FILE|-]")
	}
	switch args[0] {
	case "export":
		fs := flag.NewFlagSet("audit export", flag.ContinueOnError)
		fs.SetOutput(stderr)
		after := fs.Int64("after", 0, "export entries after this sequence number")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
			return usage(stderr, "usage: devcules audit export [-after N]")
		}
		s, code := openForAudit(cfg, stderr, open)
		if s == nil {
			return code
		}
		defer s.Close()
		l := audit.New(s, nil)
		ctx := context.Background()
		if _, err := l.Record(ctx, audit.Event{Actor: "cli", Action: audit.AuditExport}); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		n, err := l.Export(ctx, stdout, *after)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		fmt.Fprintf(stderr, "exported %d entries\n", n)
		return exitOK
	case "verify":
		if len(args) > 2 {
			return usage(stderr, "usage: devcules audit verify [FILE|-]")
		}
		var n int64
		var err error
		if len(args) == 2 {
			in := stdin
			if args[1] != "-" {
				f, ferr := os.Open(args[1])
				if ferr != nil {
					fmt.Fprintln(stderr, ferr.Error())
					return exitError
				}
				defer f.Close()
				in = f
			}
			n, err = audit.VerifyLines(in)
		} else {
			s, code := openForAudit(cfg, stderr, open)
			if s == nil {
				return code
			}
			defer s.Close()
			n, err = audit.New(s, nil).Verify(context.Background())
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s (after %d good entries)\n", err, n)
			return exitError
		}
		fmt.Fprintf(stdout, "audit chain ok: %d entries\n", n)
		return exitOK
	}
	return usage(stderr, "unknown audit command %q", args[0])
}

func openForAudit(cfg config.Config, stderr io.Writer, open storeOpener) (store.Store, int) {
	if cfg.DatabaseURL != "" {
		fmt.Fprintln(stderr, "DEVCULES_DATABASE_URL is set: the Postgres store does not exist yet (stretch goal); unset it to use SQLite")
		return nil, exitError
	}
	s, err := open(filepath.Join(cfg.DataDir, "devcules.db"))
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return nil, exitError
	}
	return s, exitOK
}
