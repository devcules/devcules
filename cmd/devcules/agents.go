package main

import (
	"context"
	"os"
	"strings"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/pty"
	"github.com/devcules/devcules/plugins/agents"
	"github.com/devcules/devcules/plugins/agents/claudecode"
	"github.com/devcules/devcules/plugins/agents/cli"
	"github.com/devcules/devcules/plugins/agents/codex"
	"github.com/devcules/devcules/plugins/agents/musecode"
)

// agentList is every agent adapter this build offers: the coding
// agents on the router, then the personal assistant CLIs that run
// their own binary in the project's directory.
func agentList() []agents.Agent {
	return []agents.Agent{
		musecode.New(), claudecode.New(), codex.New(),
		cli.NewHermes(), cli.NewOpenClaw(),
	}
}

// wiredAgents is the hook's list: only the agents an approval hook
// can run. An off-router assistant installs none, so naming one to
// `devcules hook` is unknown.
func wiredAgents() []agents.Agent {
	var out []agents.Agent
	for _, a := range agentList() {
		if agents.Wired(a) {
			out = append(out, a)
		}
	}
	return out
}

// agentLauncher adapts the plugins/agents registry to the runner's
// terminal manager, which must not import plugins.
type agentLauncher struct{ r *agents.Registry }

func newAgentLauncher(serveURL string) agentLauncher {
	exe, err := os.Executable()
	if err != nil {
		exe = "devcules"
	}
	return agentLauncher{r: agents.New(serveURL, exe, agentList()...)}
}

func (l agentLauncher) Agents(ctx context.Context) []terminals.AgentInfo {
	return l.r.Agents(ctx)
}

func (l agentLauncher) Launch(_ context.Context, a terminals.AgentLaunch, dir string) (pty.Launch, error) {
	s, err := l.r.Spec(a, dir)
	if err != nil {
		return pty.Launch{}, err
	}
	return pty.Launch{Cmd: s.Cmd, Env: s.Env, Files: s.Files}, nil
}

// hookAgent finds the adapter `devcules hook --agent <id>` names.
func hookAgent(id string) (agents.Agent, bool) {
	return agents.New("", "", wiredAgents()...).Get(id)
}

// agentIDs lists the hook adapter ids for usage messages.
func agentIDs() string {
	var ids []string
	for _, a := range wiredAgents() {
		ids = append(ids, a.ID())
	}
	return strings.Join(ids, ", ")
}
