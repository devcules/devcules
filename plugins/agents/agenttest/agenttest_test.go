package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/devcules/devcules/plugins/agents"
)

// toy is the smallest adapter that keeps the contract: it proves the
// suite passes a good adapter, so an adapter failing it is at fault.
type toy struct{}

func (toy) ID() string                            { return "toy" }
func (toy) Name() string                          { return "Toy" }
func (toy) Provider() string                      { return "meta" }
func (toy) Detect(context.Context) (string, bool) { return "", false }
func (toy) InstallHint() string                   { return "install toy" }

func (toy) LaunchSpec(l agents.Launch) (agents.Spec, error) {
	if l.ServeURL == "" || l.Token == "" || l.Hook == "" {
		return agents.Spec{}, errors.New("incomplete launch")
	}
	cfg, _ := json.Marshal(map[string]string{
		"base": l.ServeURL + "/llm/meta/v1", "mcp": l.ServeURL + "/mcp", "model": l.Model, "hook": l.Hook,
	})
	return agents.Spec{
		Cmd:   []string{"toy"},
		Env:   map[string]string{"TOY_KEY": l.Token},
		Files: map[string][]byte{"toy.json": cfg},
	}, nil
}

func (toy) HookCall(stdin []byte) (agents.Call, error) {
	var in struct{ Tool, Cmd string }
	if json.Unmarshal(stdin, &in) != nil || in.Tool == "" {
		return agents.Call{}, errors.New("bad call")
	}
	if in.Cmd == "" {
		return agents.Call{Tool: in.Tool, Command: in.Tool}, nil
	}
	return agents.Call{Tool: in.Tool, Command: in.Cmd}, nil
}

func (toy) HookAnswer(a agents.Answer) agents.Reply {
	if a.Allow {
		return agents.Reply{}
	}
	return agents.Reply{Stderr: a.Reason, Code: 2}
}

func TestContractPassesAGoodAdapter(t *testing.T) {
	Run(t, toy{}, Fixture{
		ShellCall:     []byte(`{"tool":"shell","cmd":"ls -la"}`),
		WantTool:      "shell",
		WantCommand:   "ls -la",
		OtherCall:     []byte(`{"tool":"edit"}`),
		WantOtherTool: "edit",
	})
}
