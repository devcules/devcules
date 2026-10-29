package agents

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/terminals"
)

type fake struct {
	id        string
	installed bool
	err       error
}

func (f fake) ID() string                            { return f.id }
func (f fake) Name() string                          { return strings.ToUpper(f.id) }
func (f fake) Provider() string                      { return "meta" }
func (f fake) InstallHint() string                   { return "install " + f.id }
func (f fake) HookCall([]byte) (Call, error)         { return Call{}, nil }
func (f fake) HookAnswer(Answer) Reply               { return Reply{} }
func (f fake) Detect(context.Context) (string, bool) { return "1.0", f.installed }
func (f fake) LaunchSpec(l Launch) (Spec, error) {
	if f.err != nil {
		return Spec{}, f.err
	}
	return Spec{Cmd: []string{f.id, l.Model}, Env: map[string]string{"HOOK": l.Hook, "URL": l.ServeURL}}, nil
}

func TestRegistry(t *testing.T) {
	r := New("http://serve:7659/", "/bin/devcules", fake{id: "one", installed: true}, fake{id: "two"}, fake{id: "bad", err: errors.New("no")})
	list := r.Agents(context.Background())
	if len(list) != 3 || !list[0].Installed || list[1].Installed || list[1].Install != "install two" || list[0].Name != "ONE" {
		t.Fatalf("Agents = %+v", list)
	}
	spec, err := r.Spec(terminals.AgentLaunch{ID: "one", Token: "tok", Model: "m"}, "/run/launch-x")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.Cmd, []string{"one", "m"}) || string(spec.Files[TokenFile]) != "tok" || spec.Env["URL"] != "http://serve:7659" {
		t.Fatalf("spec = %+v", spec)
	}
	home, _ := os.UserHomeDir()
	want := `env -i HOME=` + ShQuote(home) + ` DEVCULES_SERVE_URL='http://serve:7659' DEVCULES_LAUNCH_TOKEN_FILE='/run/launch-x/launch.token' '/bin/devcules' hook --agent one`
	if spec.Env["HOOK"] != want {
		t.Fatalf("hook = %q\nwant %q", spec.Env["HOOK"], want)
	}
	if _, err := r.Spec(terminals.AgentLaunch{ID: "nope"}, "/d"); err == nil {
		t.Fatal("unknown agent accepted")
	}
	if _, err := r.Spec(terminals.AgentLaunch{ID: "bad"}, "/d"); err == nil {
		t.Fatal("adapter error swallowed")
	}
}

func TestShQuote(t *testing.T) {
	if got := ShQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("ShQuote = %s", got)
	}
}

func TestVersion(t *testing.T) {
	t.Setenv("DEVCULES_RUNNER_TOKEN", "must-not-leak")
	out, ok := Version(context.Background(), []string{"PROBE=1"}, "bash", "-c", `echo "v$PROBE ${DEVCULES_RUNNER_TOKEN:-clean}"; echo second`)
	if !ok || out != "v1 clean" {
		t.Fatalf("Version = %q %v", out, ok)
	}
	if _, ok := Version(context.Background(), nil, "definitely-not-installed-cli"); ok {
		t.Fatal("a missing CLI was detected")
	}
}
