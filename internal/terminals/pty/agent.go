package pty

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/terminals"
)

// Launch is what an agent needs in its terminal: the command, extra
// environment, and config files written under its launch dir (paths
// relative to it). The plugins/agents adapters build it; the manager
// only writes and runs it.
type Launch struct {
	Cmd   []string
	Env   map[string]string
	Files map[string][]byte
}

// Launcher lists and builds agent launches on the runner. cmd/devcules
// wires it to the plugins/agents registry, which internal code never
// imports.
type Launcher interface {
	Agents(ctx context.Context) []terminals.AgentInfo
	Launch(ctx context.Context, a terminals.AgentLaunch, dir string) (Launch, error)
}

var envKeyRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// agentOption tags a session with its agent id for the list.
const agentOption = "@devcules_agent"

// LaunchDir is where an agent terminal's files live: inside the
// private socket dir (a tmpfs in compose), so per-launch tokens never
// touch the workspace or a disk that outlives the runner.
func (m *Manager) LaunchDir(id string) string {
	return filepath.Join(m.socket, "launch-"+id)
}

// OpenAgent creates the tmux session for id running l: its files are
// written 0600 under LaunchDir(id), its environment is sourced from a
// 0600 file (never a command line), and when the agent exits the
// terminal drops to a login shell with the launch dir removed.
func (m *Manager) OpenAgent(ctx context.Context, id, agent string, l Launch) (time.Time, error) {
	if !terminals.ValidID(id) {
		return time.Time{}, fmt.Errorf("term: want 16 hex chars")
	}
	if !terminals.ValidAgentID(agent) || len(l.Cmd) == 0 || l.Cmd[0] == "" {
		return time.Time{}, fmt.Errorf("agent: bad launch")
	}
	m.openMu.Lock()
	defer m.openMu.Unlock()
	terms, err := m.List(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if len(terms) >= m.max {
		return time.Time{}, fmt.Errorf("too many terminals: at most %d per runner", m.max)
	}
	dir := m.LaunchDir(id)
	if err := writeLaunch(dir, l); err != nil {
		_ = os.RemoveAll(dir)
		return time.Time{}, err
	}
	name := terminals.SessionName(id)
	if _, err := m.run(ctx, "new-session", "-d", "-s", name, "-c", m.workspace,
		"-x", strconv.Itoa(defaultCols), "-y", strconv.Itoa(defaultRows), "bash", "-lc", agentScript(dir, agent, l)); err != nil {
		_ = os.RemoveAll(dir)
		if strings.Contains(err.Error(), "duplicate session") {
			return time.Time{}, fmt.Errorf("terminal exists")
		}
		return time.Time{}, fmt.Errorf("tmux failed to open the terminal")
	}
	// The tag only labels the list; a failure leaves a working terminal.
	_, _ = m.run(ctx, "set-option", "-t", name, agentOption, agent)
	return time.Now(), nil
}

// writeLaunch creates dir (it must not exist yet) and writes the files
// and the env file.
func writeLaunch(dir string, l Launch) error {
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("agent: cannot create the launch dir")
	}
	for rel, data := range l.Files {
		if !filepath.IsLocal(rel) || rel == "env.sh" {
			return fmt.Errorf("agent: bad file path")
		}
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("agent: cannot write its files")
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("agent: cannot write its files")
		}
	}
	var env strings.Builder
	for _, k := range slices.Sorted(maps.Keys(l.Env)) {
		if !envKeyRE.MatchString(k) {
			return fmt.Errorf("agent: bad env name")
		}
		env.WriteString(k + "=" + shQuote(l.Env[k]) + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "env.sh"), []byte(env.String()), 0o600); err != nil {
		return fmt.Errorf("agent: cannot write its files")
	}
	return nil
}

// agentScript runs the agent after the login profile (so PATH finds
// user-installed CLIs), then forgets its environment and files and
// leaves a shell, so the owner can read what happened.
func agentScript(dir, agent string, l Launch) string {
	quoted := make([]string, len(l.Cmd))
	for i, a := range l.Cmd {
		quoted[i] = shQuote(a)
	}
	keys := slices.Sorted(maps.Keys(l.Env))
	var b strings.Builder
	b.WriteString("set -a; . " + shQuote(filepath.Join(dir, "env.sh")) + "; set +a\n")
	b.WriteString(strings.Join(quoted, " ") + "\n")
	b.WriteString("code=$?\n")
	if len(keys) > 0 {
		b.WriteString("unset " + strings.Join(keys, " ") + "\n")
	}
	b.WriteString("rm -rf " + shQuote(dir) + "\n")
	b.WriteString(`printf '\n[devcules] ` + agent + ` exited (%s). This terminal is a shell now.\n' "$code"` + "\n")
	b.WriteString("exec bash -l\n")
	return b.String()
}

// shQuote single-quotes s for bash.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
