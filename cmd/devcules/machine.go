package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/machines"
	"github.com/devcules/devcules/internal/machines/elements"
	"github.com/devcules/devcules/internal/machines/helper"
	"github.com/devcules/devcules/internal/machines/input"
	"github.com/devcules/devcules/internal/machines/ocr"
	"github.com/devcules/devcules/internal/machines/screen"
	"github.com/devcules/devcules/internal/terminals/pty"
)

// machineIdentity is what enrolment leaves on the computer: the name
// it enrolled under and the serve it enrolled with. The private key
// sits next to it in machine.key; neither ever leaves the machine.
type machineIdentity struct {
	Name  string `json:"name"`
	Serve string `json:"serve"`
}

func machineKeyPath(cfg config.Config) string   { return filepath.Join(cfg.DataDir, "machine.key") }
func machineIdentPath(cfg config.Config) string { return filepath.Join(cfg.DataDir, "machine.json") }

// runMachine is `devcules machine`: with "enrol <code> [--name N]"
// it enrols this computer, and with no arguments it links to serve
// and stays linked until a signal.
func runMachine(cfg config.Config, args []string, stdout, stderr io.Writer, sigs <-chan os.Signal, client *http.Client) int {
	switch {
	case len(args) == 0:
		return runMachineLink(cfg, stdout, stderr, sigs)
	case args[0] == "enrol" || args[0] == "enroll":
		code, name, err := enrolArgs(args[1:])
		if err != nil {
			return usage(stderr, "%s", err)
		}
		return runMachineEnrol(cfg, code, name, stdout, stderr, client)
	default:
		return usage(stderr, "machine takes no arguments, or: enrol <code> [--name <name>]")
	}
}

func enrolArgs(args []string) (code, name string, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "-"):
			return "", "", fmt.Errorf("machine enrol: unknown flag %q", a)
		case code == "":
			code = a
		default:
			return "", "", fmt.Errorf("machine enrol: one code, got %q too", a)
		}
	}
	if code == "" {
		return "", "", errors.New("machine enrol: want the code from the Machines tab: devcules machine enrol <code> [--name <name>]")
	}
	return code, name, nil
}

// runMachineEnrol makes (or reuses) this computer's key, proves it to
// serve with the one-time code, and records the name and serve.
func runMachineEnrol(cfg config.Config, code, name string, stdout, stderr io.Writer, client *http.Client) int {
	if name == "" {
		name = runnerName()
	}
	if !machines.ValidName(name) {
		fmt.Fprintf(stderr, "machine name %q invalid: letters, digits, '.', '_' and '-', up to 64 (pass --name)\n", name)
		return exitError
	}
	priv, err := loadOrCreateMachineKey(machineKeyPath(cfg))
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	body, _ := json.Marshal(map[string]string{
		"code":   code,
		"name":   name,
		"pubkey": base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)),
		"sig":    base64.StdEncoding.EncodeToString(ed25519.Sign(priv, machines.EnrolMessage(code))),
	})
	u, err := url.Parse(cfg.ServeURL)
	if err != nil {
		fmt.Fprintf(stderr, "DEVCULES_SERVE_URL invalid: %q\n", cfg.ServeURL)
		return exitError
	}
	u.Path = path.Join(u.Path, "/api/machines/enrol")
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "cannot reach serve at %s: %s (set DEVCULES_SERVE_URL to the address you open Devcules at)\n", cfg.ServeURL, err)
		return exitError
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		fmt.Fprintf(stderr, "enrolment refused: %s\n", e.Error)
		return exitError
	}
	ident, _ := json.MarshalIndent(machineIdentity{Name: name, Serve: cfg.ServeURL}, "", "  ")
	if err := os.WriteFile(machineIdentPath(cfg), append(ident, '\n'), 0o600); err != nil {
		fmt.Fprintf(stderr, "enrolled, but cannot save %s: %s\n", machineIdentPath(cfg), err)
		return exitError
	}
	fmt.Fprintf(stdout, "enrolled as %q with %s. Start it with: devcules machine\n", name, cfg.ServeURL)
	return exitOK
}

// loadOrCreateMachineKey reads the ed25519 seed, or makes one. The
// file holds the base64 seed, mode 0600 in a 0700 directory; a key
// readable by others is refused, as ssh does.
func loadOrCreateMachineKey(p string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(p); err == nil {
		if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s is readable by others: chmod 600 it", p)
		}
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s is not a machine key (delete it and enrol again)", p)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot read %s: %w", p, err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", filepath.Dir(p), err)
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", p, err)
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(seed) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// runMachineLink links this computer to serve: the machframes helper
// for the screen and input, and tmux terminals for the shell, both
// over one link signed with the enrolled key. A local
// DEVCULES_MACHINE_SHELL=off withholds the shell whatever serve asks.
func runMachineLink(cfg config.Config, stdout, stderr io.Writer, sigs <-chan os.Signal) int {
	raw, err := os.ReadFile(machineIdentPath(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "this machine is not enrolled (no %s): mint a code in the Machines tab, then run devcules machine enrol <code>\n", machineIdentPath(cfg))
		return exitError
	}
	var ident machineIdentity
	if err := json.Unmarshal(raw, &ident); err != nil || !machines.ValidName(ident.Name) {
		fmt.Fprintf(stderr, "%s is damaged: enrol again\n", machineIdentPath(cfg))
		return exitError
	}
	priv, err := loadOrCreateMachineKey(machineKeyPath(cfg))
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	serveURL := cfg.ServeURL
	if _, set := os.LookupEnv("DEVCULES_SERVE_URL"); !set && ident.Serve != "" {
		serveURL = ident.Serve
	}
	wsURL, err := linkURL(serveURL)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	log.Info("machine connecting", "serve", serveURL, "name", ident.Name)
	c := dial.New(wsURL, ident.Name, version, "", dial.Options{
		Logger: log,
		Role:   "machine",
		Sign:   func(nonce []byte) []byte { return ed25519.Sign(priv, nonce) },
	})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	helper.New(machineDrivers(cfg, serveURL, log)).Serve(c)
	if os.Getenv("DEVCULES_MACHINE_SHELL") == "off" {
		log.Info("shell withheld by DEVCULES_MACHINE_SHELL=off")
	} else if m, err := machineTerminals(cfg, log); err != nil {
		log.Warn("shell unavailable, linking without terminals", "err", err.Error())
	} else {
		pty.Serve(ctx, c, m, nil)
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
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		fmt.Fprintln(stderr, "machine stopped without a signal")
		return exitError
	}
}

// machineDrivers opens each sense; one that fails to open is left nil
// and answers "unavailable on this machine" instead of stopping the
// helper.
func machineDrivers(cfg config.Config, serveURL string, log *slog.Logger) helper.Drivers {
	d := helper.Drivers{Router: serveURL, LaunchTok: cfg.LaunchToken}
	if v, err := screen.New(); err != nil {
		log.Warn("screen unavailable", "err", err.Error())
	} else {
		d.Screen = v
	}
	if v, err := input.New(); err != nil {
		log.Warn("input unavailable", "err", err.Error())
	} else {
		d.Input = v
	}
	if v, err := elements.New(); err != nil {
		log.Warn("accessibility tree unavailable", "err", err.Error())
	} else {
		d.Elements = v
	}
	if v, err := ocr.New(); err != nil {
		log.Warn("OCR unavailable", "err", err.Error())
	} else {
		d.Words = v
	}
	return d
}

// machineTerminals builds the tmux driver for this computer's shell.
// Shells start in DEVCULES_WORKSPACE when it exists, else the home
// directory: a machine is a person's computer, not a container.
func machineTerminals(cfg config.Config, log *slog.Logger) (*pty.Manager, error) {
	if !pty.HasTmux() {
		return nil, errors.New("tmux not installed (brew install tmux, or apt install tmux)")
	}
	socketDir := os.Getenv("TMUX_TMPDIR")
	if socketDir == "" {
		socketDir = filepath.Join(os.TempDir(), "devcules-machine-tmux")
	}
	workspace := cfg.Workspace
	if st, err := os.Stat(workspace); err != nil || !st.IsDir() {
		if workspace, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	return pty.New(socketDir, workspace, log)
}
