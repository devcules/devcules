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
func runRunner(cfg config.Config, name string, stdout, stderr io.Writer, sigs <-chan os.Signal) int {
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
	if name == "" {
		name = runnerName()
	}
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
	if m, err := terminalManager(cfg.Workspace, log); err != nil {
		log.Warn("terminals unavailable, linking without them", "err", err.Error())
	} else {
		// An agent's launch script runs this binary's `reap` when the
		// agent exits, so what the agent detached stops with it.
		if exe, err := os.Executable(); err == nil {
			m.SetReaper(exe)
		}
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
// dir in the system temp area. Shells, project paths and clones start
// in DEVCULES_WORKSPACE (default /workspace), falling back to the
// current directory when it does not exist.
func terminalManager(workspace string, log *slog.Logger) (*pty.Manager, error) {
	socketDir := os.Getenv("TMUX_TMPDIR")
	if socketDir == "" {
		socketDir = filepath.Join(os.TempDir(), "devcules-tmux")
	}
	return pty.New(socketDir, runnerWorkspace(workspace), log)
}

// runnerWorkspace is ws when it is an existing directory, else the
// current directory (a runner started by hand outside a container).
func runnerWorkspace(ws string) string {
	if st, err := os.Stat(ws); err == nil && st.IsDir() {
		return ws
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return os.TempDir()
}

// runnerName identifies this runner on the link. Hostnames are unique
// per machine and per container, so a restarted runner reclaims its
// name (replacing its ghost) while two live runners never collide. In
// a container the link names itself for what it is, so the picker does
// not present the sandbox as the developer's own computer; a bare
// `devcules` passes the host label directly and skips this.
func runnerName() string {
	prefix := ""
	if config.DetectContainer() {
		prefix = "container "
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return prefix + "(" + h + ")"
	}
	return prefix + "runner"
}
