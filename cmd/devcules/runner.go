package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/terminals/pty"
)

// runRunner links to serve and stays linked until a signal: the dialer
// reconnects across serve restarts on its own. A vault key refuses
// first — runners get per-launch tokens, never real keys — and a
// missing token fails with next steps, since the runner never mints
// (only serve creates the file).
func runRunner(cfg config.Config, stdout, stderr io.Writer, sigs <-chan os.Signal) int {
	if _, err := cfg.ForRole("runner"); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	if cfg.RunnerToken == "" {
		fmt.Fprintf(stderr, "runner token missing: set DEVCULES_RUNNER_TOKEN or DEVCULES_RUNNER_TOKEN_FILE (nothing at %s yet: serve creates it on first start)\n", cfg.RunnerTokenFile)
		return exitError
	}
	wsURL, err := linkURL(cfg.ServeURL)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	name := runnerName()
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	// The serve address and link name are operational, not secret; the
	// token never appears in a log line.
	log.Info("runner connecting", "serve", cfg.ServeURL, "name", name)

	c := dial.New(wsURL, name, version, cfg.RunnerToken, dial.Options{Logger: log})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	// Terminals ride the same link: tmux is the source of truth, so a
	// missing tmux only loses terminals — the runner still links. Serve
	// registers its handlers before Run starts, and drops every attach
	// client on link changes and when ctx ends.
	if m, err := terminalManager(log); err != nil {
		log.Warn("terminals unavailable, linking without them", "err", err.Error())
	} else {
		pty.Serve(ctx, c, m, newAgentLauncher(cfg.ServeURL))
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	select {
	case sig := <-sigs:
		log.Info("shutting down", "signal", sig.String())
		stop()
		if err := <-done; err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		return exitOK
	case err := <-done:
		// Run ends only on cancellation, so reaching here without a
		// signal means a bug: say so instead of exiting silently.
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		fmt.Fprintln(stderr, "runner stopped without a signal")
		return exitError
	}
}

// linkURL turns the serve HTTP(S) address into the /link WebSocket
// address. The config loader already validates the shape; this guards
// the hand-built configs tests pass in.
func linkURL(serveURL string) (string, error) {
	u, err := url.Parse(serveURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("DEVCULES_SERVE_URL invalid: %q (want an http(s) URL with a host)", serveURL)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("DEVCULES_SERVE_URL invalid: %q (want an http(s) URL with a host)", serveURL)
	}
	u.Path = path.Join(u.Path, "/link")
	return u.String(), nil
}

// terminalManager builds the tmux driver for this runner. The socket
// lives under TMUX_TMPDIR when set (a tmpfs in compose), else a private
// dir in the system temp area; shells start in /workspace, falling back
// to the current directory outside containers.
func terminalManager(log *slog.Logger) (*pty.Manager, error) {
	socketDir := os.Getenv("TMUX_TMPDIR")
	if socketDir == "" {
		socketDir = filepath.Join(os.TempDir(), "devcules-tmux")
	}
	workspace := "/workspace"
	if st, err := os.Stat(workspace); err != nil || !st.IsDir() {
		workspace, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	return pty.New(socketDir, workspace, log)
}

// runnerName identifies this runner on the link. Hostnames are unique
// per machine and per container, so a restarted runner reclaims its
// name (replacing its ghost) while two live runners never collide.
func runnerName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "runner"
}
