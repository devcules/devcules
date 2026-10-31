// Package cli is the off-router agent: one developer assistant CLI
// (Hermes, OpenClaw, any favourite) opened in a terminal, in the
// project's directory, where it has full run of itself and the tree.
//
// A coding agent (claudecode, codex, musecode) routes its model calls
// through serve, with a launch token and an approval hook translating
// the CLI's PreToolUse payload into Devcules approvals. A personal
// assistant CLI answers to none of that: it speaks its own API under
// its own login and keeps its own tool policy, so its launch has no
// router, no token and no hook — Cmd is the binary, nothing else. The
// contract suite skips the hook checks on agents.Wired(a) == false,
// and the launch still fails closed when Dir is empty.
//
// Devcules never bundles or installs the CLI (PLAN.md §2): Detect
// lists it only where the developer already has it, and the API shows
// InstallHint otherwise.
package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/plugins/agents"
)

// Agent is a personal assistant CLI running its own binary.
type Agent struct {
	id     string
	name   string
	bin    string
	args   []string
	hint   string
	detect func(ctx context.Context) (string, bool)
}

// Option tweaks one assistant's shape; the defaults are lowercase id,
// the id as the binary, no version check.
type Option func(*Agent)

// WithName sets the picker label (default: the id).
func WithName(name string) Option {
	return func(a *Agent) { a.name = strings.TrimSpace(name) }
}

// WithBinary names the executable when it differs from the id.
func WithBinary(bin string) Option {
	return func(a *Agent) { a.bin = strings.TrimSpace(bin) }
}

// WithInstallHint is the documented install command, shown never run.
func WithInstallHint(hint string) Option {
	return func(a *Agent) { a.hint = strings.TrimSpace(hint) }
}

// WithArgs appends the CLI's own interactive-chat arguments after the
// binary (Hermes is `chat --cli`, OpenClaw the same shape). Without it
// the launch runs the bare binary, which for most assistants is a
// help page, not a session.
func WithArgs(args ...string) Option {
	return func(a *Agent) { a.args = args }
}

// WithDetect replaces the version check (default: none, so the CLI
// never auto-lists — an assistant without a detection stays manual
// until WithDetect says how it announces itself).
func WithDetect(detect func(ctx context.Context) (string, bool)) Option {
	return func(a *Agent) { a.detect = detect }
}

// New builds the adapter for one assistant CLI.
func New(id string, opts ...Option) (Agent, error) {
	if !terminals.ValidAgentID(id) {
		return Agent{}, fmt.Errorf("cli: bad assistant id %q", id)
	}
	a := Agent{id: id, name: id, bin: id, detect: func(context.Context) (string, bool) { return "", false }}
	for _, opt := range opts {
		opt(&a)
	}
	if a.name == "" || a.bin == "" {
		return Agent{}, fmt.Errorf("cli: %s needs a name and a binary", id)
	}
	return a, nil
}

// Must is New or panic: wiring mistakes are build-time, not runtime.
func Must(a Agent, err error) Agent {
	if err != nil {
		panic(err)
	}
	return a
}

// ID is the assistant's id, as the API lists it.
func (a Agent) ID() string { return a.id }

// Name is the picker's label.
func (a Agent) Name() string { return a.name }

// Provider is the empty family: the assistant's model is its own
// business, so the launch form asks for no provider or key.
func (a Agent) Provider() string { return "" }

// Wired reports there is no serve round trip.
func (a Agent) Wired() bool { return false }

// Wire is the empty wire: nothing routes through serve, so no wire
// is ever offered for it.
func (a Agent) Wire() string { return "" }

// Only lists no providers: the assistant calls its model itself.
func (a Agent) Only() []string { return nil }

// InstallHint is the documented installer.
func (a Agent) InstallHint() string { return a.hint }

// Detect runs the assistant's own version check.
func (a Agent) Detect(ctx context.Context) (string, bool) { return a.detect(ctx) }

// LaunchSpec runs the CLI itself, in the terminal's directory. ServeURL,
// Token and Hook stay unused: the launch is off-router on purpose, so
// the assistant speaks its own API with its own login.
func (a Agent) LaunchSpec(l agents.Launch) (agents.Spec, error) {
	if strings.TrimSpace(l.Dir) == "" {
		return agents.Spec{}, fmt.Errorf("cli: %s has nowhere to run", a.id)
	}
	return agents.Spec{Cmd: append([]string{a.bin}, a.args...)}, nil
}

// HookCall always fails: an off-router assistant installs no hook, so
// a payload addressed to one is a wiring bug, not a call to judge.
func (a Agent) HookCall([]byte) (agents.Call, error) {
	return agents.Call{}, fmt.Errorf("cli: %s has no approval hook", a.id)
}

// HookAnswer always blocks: no hook may ever succeed through an
// assistant that installs none.
func (a Agent) HookAnswer(agents.Answer) agents.Reply {
	return agents.Blocked(agents.Answer{Reason: a.id + " has no approval hook"})
}
