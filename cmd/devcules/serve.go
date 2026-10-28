package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/devcules/devcules/internal/api"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/bus"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// drainTimeout bounds graceful shutdown: serve stops accepting, drains
// in-flight requests up to this long, then closes the bus and the store
// (PLAN.md §6).
const drainTimeout = 20 * time.Second

// storeOpener connects a store at path. SQLite is the production opener;
// tests substitute fakes and spies.
type storeOpener func(path string) (store.Store, error)

func openSQLite(path string) (store.Store, error) {
	return sqlite.Open(context.Background(), path)
}

// runServe runs serve to completion and returns its exit code. The store
// opens at <DataDir>/devcules.db; a set DEVCULES_DATABASE_URL names the
// Postgres store, which does not exist yet, so it fails with next steps
// instead of silently falling back to SQLite. First start creates the
// admin user and prints its one-time password to stderr as a plain
// line, never through the JSON logger; later starts print nothing.
// The listener, signals, store opener and bus are parameters so tests
// drive shutdown without real ports or process signals.
func runServe(cfg config.Config, stdout, stderr io.Writer, ln net.Listener, sigs <-chan os.Signal, open storeOpener, b bus.Bus) int {
	log := slog.New(slog.NewJSONHandler(stdout, nil))

	if cfg.DatabaseURL != "" {
		fmt.Fprintln(stderr, "DEVCULES_DATABASE_URL is set: the Postgres store does not exist yet (stretch goal); unset it to use SQLite")
		return exitError
	}
	s, err := open(filepath.Join(cfg.DataDir, "devcules.db"))
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	authSvc := auth.New(s)
	password, created, err := authSvc.EnsureAdmin(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		b.Close()
		s.Close()
		return exitError
	}
	if created {
		fmt.Fprintln(stderr, "Admin password (shown once): "+password)
	}
	srv := &http.Server{
		Handler:           api.Handler(api.Options{Store: s, Auth: authSvc, PublicURL: cfg.PublicURL}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       time.Minute,
	}
	// The address is operational, not secret; values and keys never log.
	log.Info("serve listening", "addr", cfg.Listen)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case sig := <-sigs:
		log.Info("shutting down", "signal", sig.String())
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve failed", "err", err.Error())
			b.Close()
			s.Close()
			return exitError
		}
		b.Close()
		s.Close()
		return exitOK
	}

	ctx, stop := context.WithTimeout(context.Background(), drainTimeout)
	defer stop()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("drain failed", "err", err.Error())
		b.Close()
		s.Close()
		return exitError
	}
	b.Close()
	s.Close()
	log.Info("serve stopped")
	return exitOK
}
