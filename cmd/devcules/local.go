package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/bus/memory"
	"github.com/devcules/devcules/internal/config"
)

// runLocal is the zero-argument host desk: one `devcules` on a computer
// runs serve and its host runner in one process, so the project picker
// sees the developer's own folders and no env var is needed. The runner
// dials serve's own listen address over loopback; Docker stays the
// sandbox, an explicit extra machine (`devcules serve`+`devcules
// runner`, the compose stack).
//
// SIGINT/SIGTERM stop both halves because the one channel runs in main;
// either half ending on its own ends the desk and prints its reason.
func runLocal(cfg config.Config, stdout, stderr io.Writer, sigs chan os.Signal, open storeOpener) int {
	// Home is the workspace anchor: the browse shortcuts ~ and the
	// picker's first listing show the computer's real folders. An
	// operator who set DEVCULES_WORKSPACE explicitly keeps that.
	if _, set := os.LookupEnv("DEVCULES_WORKSPACE"); !set {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cfg.Workspace = home
		}
	}
	// The in-process runner talks to serve on its own listen address,
	// where the split runner defaults to the compose name "serve". Only
	// an explicit DEVCULES_SERVE_URL overrides; otherwise the host desk
	// always links against the address it listens on itself.
	if envEmpty("DEVCULES_SERVE_URL") {
		cfg.ServeURL = "http://" + listenAddress(cfg.Listen)
	}
	// Local cookies and passkeys take the same loopback name.
	if envEmpty("DEVCULES_PUBLIC_URL") {
		cfg.PublicURL = cfg.ServeURL
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "cannot listen on %s: %s\n", cfg.Listen, err)
		return exitError
	}

	done := make(chan int, 2)
	var once sync.Once
	finish := func(code int) { once.Do(func() { done <- code }) }

	// The runner half serves to nobody: like the split runner it rejects
	// serve-only secrets (ForRole), so it gets a cleaned copy — the
	// vault key and provider keys stay in the serve half.
	runnerCfg := cfg
	runnerCfg.VaultKey = ""
	runnerCfg.VaultNewKey = ""
	runnerCfg.MetaAPIKey = ""
	if runnerCfg.RunnerTokenFile == "" {
		runnerCfg.RunnerTokenFile = filepath.Join(cfg.DataDir, "runner.token")
	}

	// Serve first: it mints the runner token file and the runner reads
	// the value (not the file path) before its first dial, which is the
	// same hand-off the compose stack does with the shared link volume.
	go func() { finish(runServe(cfg, stdout, stderr, ln, sigs, open, memory.New())) }()
	if runnerCfg.RunnerToken == "" {
		if err := readRunnerToken(&runnerCfg, 20*time.Second); err != nil {
			fmt.Fprintln(stderr, "devcules: serve minted no runner token:", err)
			return exitError
		}
	}
	go func() { finish(runRunner(runnerCfg, hostRunnerName(), stdout, stderr, sigs)) }()

	code := <-done
	// One half stopped: the other unwinds through the signal channel.
	sigs <- os.Interrupt
	return code
}

// envEmpty reports an unset or blank environment variable: a value that
// is all whitespace means the same as unset.
func envEmpty(name string) bool {
	v, set := os.LookupEnv(name)
	return !set || strings.TrimSpace(v) == ""
}

// readRunnerToken waits for serve to mint the runner token (same file
// the compose mount shares) and fills it into the runner's copy, so the
// runner half dials with the value the way the split runner would.
func readRunnerToken(runnerCfg *config.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if raw, err := os.ReadFile(runnerCfg.RunnerTokenFile); err == nil {
			if token := strings.TrimSpace(string(raw)); token != "" {
				runnerCfg.RunnerToken = token
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no token at %s after %s", runnerCfg.RunnerTokenFile, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// listenAddress turns the listen flag into a dialable host:port for the
// in-process runner: a wild-card host becomes the loopback.
func listenAddress(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// hostRunnerName labels the host link for the pickers: the computer the
// developer sits at, told apart from a container. It reclaims its own
// name on restart like any runner.
func hostRunnerName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return "host (" + h + ")"
	}
	return "host (this computer)"
}
