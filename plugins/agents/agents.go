package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/terminals"
)

// TokenFile is the launch dir file holding the launch token, which the
// hook reads (DEVCULES_LAUNCH_TOKEN_FILE): agents often run hooks in a
// cleared environment.
const TokenFile = "launch.token"

// Launch is one agent launch as an adapter sees it.
type Launch struct {
	// ServeURL is how this runner reaches serve; the router is
	// ServeURL + "/llm/<provider>" and the MCP server ServeURL + "/mcp".
	ServeURL string
	// Token is the per-launch token: the agent's model API key, its MCP
	// bearer token and the hook's credential.
	Token string
	// Model is the model to start with; "" is the adapter's default.
	Model string
	// Provider is the provider profile to call; "" is its own. Serve
	// checked that the profile serves the agent's Wire. The router
	// path is ServeURL + "/llm/" + provider.
	Provider string
	// Dir is the launch's private dir (0700, removed with the
	// terminal). Spec.Files paths are relative to it.
	Dir string
	// Hook is the shell command that runs the approval hook for this
	// launch; adapters put it in the agent's hook config as is.
	Hook string
}

// Spec is what the runner runs: the command, extra environment and
// files. The token may sit in Env and Files (both 0600 on the
// runner), never in Cmd, where other processes could read it.
type Spec struct {
	Cmd   []string
	Env   map[string]string
	Files map[string][]byte
}

// Call is one tool call from the agent's hook, in Devcules terms.
type Call struct {
	Tool         string
	Command      string
	Irreversible bool
}

// Answer is the approval outcome the hook hands back to the agent.
// Failed means Devcules could not decide (serve unreachable, a bad
// token): adapters must fail closed and block the call.
type Answer struct {
	Allow  bool
	Failed bool
	Reason string
}

// Reply is how the hook process ends for the agent's hook protocol.
type Reply struct {
	Stdout []byte
	Stderr string
	Code   int
}

// Agent is one coding agent CLI.
type Agent interface {
	ID() string       // "musecode"
	Name() string     // "Muse Code"
	Provider() string // its own provider profile: "meta"
	// Wire is the model API the CLI speaks (terminals.WireMessages or
	// terminals.WireResponses): serve offers every provider profile
	// that serves it.
	Wire() string
	// Only, when not empty, limits the agent to these providers
	// whatever they serve: Muse Code only talks to Meta.
	Only() []string
	// Detect reports the installed version, if any.
	Detect(ctx context.Context) (version string, ok bool)
	// InstallHint is the documented install command, shown to the
	// owner and never run.
	InstallHint() string
	LaunchSpec(l Launch) (Spec, error)
	// HookCall parses what the agent writes to its hook's stdin.
	HookCall(stdin []byte) (Call, error)
	// HookAnswer turns the outcome into the agent's hook reply.
	HookAnswer(a Answer) Reply
}

// ProviderFor resolves the provider a launch calls: p, or the agent's
// own when p is empty. A malformed id, or one outside the agent's
// Only list, is an error. Whether the profile serves the agent's Wire
// is serve's check: only serve holds the profiles.
func ProviderFor(a Agent, p string) (string, error) {
	if p == "" {
		return a.Provider(), nil
	}
	if !terminals.ValidAgentID(p) {
		return "", fmt.Errorf("%s: %q is not a provider id", a.ID(), p)
	}
	if only := a.Only(); len(only) > 0 && !slices.Contains(only, p) {
		return "", fmt.Errorf("%s: cannot use provider %q (it only speaks to %s)", a.ID(), p, strings.Join(only, ", "))
	}
	return p, nil
}

// Registry holds the adapters a runner offers.
type Registry struct {
	agents   []Agent
	serveURL string
	exe      string
	home     string
}

// New returns a registry for a runner that reaches serve at serveURL
// and runs hooks with the devcules binary at exe.
func New(serveURL, exe string, list ...Agent) *Registry {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return &Registry{agents: list, serveURL: strings.TrimRight(serveURL, "/"), exe: exe, home: home}
}

// Get returns the adapter with id.
func (r *Registry) Get(id string) (Agent, bool) {
	for _, a := range r.agents {
		if a.ID() == id {
			return a, true
		}
	}
	return nil, false
}

// Agents lists every adapter with what Detect finds, checking all of
// them at once.
func (r *Registry) Agents(ctx context.Context) []terminals.AgentInfo {
	out := make([]terminals.AgentInfo, len(r.agents))
	done := make(chan struct{})
	for i, a := range r.agents {
		go func() {
			defer func() { done <- struct{}{} }()
			v, ok := a.Detect(ctx)
			out[i] = terminals.AgentInfo{ID: a.ID(), Name: a.Name(), Provider: a.Provider(),
				Wire: a.Wire(), Only: a.Only(), Installed: ok, Version: v, Install: a.InstallHint()}
		}()
	}
	for range r.agents {
		<-done
	}
	return out
}

// Spec builds the launch for one agent: the adapter's spec plus the
// token file the hook reads.
func (r *Registry) Spec(a terminals.AgentLaunch, dir string) (Spec, error) {
	ag, ok := r.Get(a.ID)
	if !ok {
		return Spec{}, fmt.Errorf("unknown agent %q", a.ID)
	}
	l := Launch{ServeURL: r.serveURL, Token: a.Token, Model: a.Model, Provider: a.Provider, Dir: dir,
		Hook: HookCommand(r.serveURL, filepath.Join(dir, TokenFile), r.exe, r.home, a.ID)}
	spec, err := ag.LaunchSpec(l)
	if err != nil {
		return Spec{}, err
	}
	if spec.Files == nil {
		spec.Files = map[string][]byte{}
	}
	spec.Files[TokenFile] = []byte(a.Token)
	return spec, nil
}

// HookCommand is the shell command an agent's hook config runs. It
// starts from an empty environment (env -i) with only the runner's
// HOME, the serve address and the token file: hooks often get a
// cleared environment anyway, and the agent's own (its model key
// holds the launch token) must not reach the hook, whose config
// refuses provider keys.
func HookCommand(serveURL, tokenFile, exe, home, agent string) string {
	return "env -i HOME=" + ShQuote(home) + " DEVCULES_SERVE_URL=" + ShQuote(serveURL) +
		" DEVCULES_LAUNCH_TOKEN_FILE=" + ShQuote(tokenFile) + " " + ShQuote(exe) + " hook --agent " + agent
}

// ShQuote single-quotes s for a POSIX shell.
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// detectTimeout bounds one version check.
const detectTimeout = 5 * time.Second

// Version runs `<bin> args...` through a login shell, the way the
// launch will find it (user-installed CLIs often live on a PATH the
// profile sets), with a scrubbed environment: the runner's own token
// must never reach an agent CLI. env adds variables (KEY=value) for
// the check. It returns the first line of output.
func Version(ctx context.Context, env []string, bin string, args ...string) (string, bool) {
	ctx, stop := context.WithTimeout(ctx, detectTimeout)
	defer stop()
	line := ShQuote(bin)
	for _, a := range args {
		line += " " + ShQuote(a)
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", "command -v "+ShQuote(bin)+" >/dev/null && "+line)
	cmd.Env = append(scrubbedEnv(), env...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(first), true
}

// scrubbedEnv is the minimal environment for version checks.
func scrubbedEnv() []string {
	env := []string{"LANG=C.UTF-8", "TERM=dumb"}
	for _, k := range []string{"HOME", "PATH", "USER"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}
