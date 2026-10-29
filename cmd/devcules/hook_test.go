package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/api"
	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/store/sqlite"
)

func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// hookServe runs the real API with approvals for one launch on runner
// "laptop" under mode.
func hookServe(t *testing.T, mode approvals.Mode) (*httptest.Server, string, *approvals.Service) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tokens := launch.New(s)
	token, _, _ := tokens.MintOn(ctx, "laptop", "musecode", "", time.Hour)
	a, _ := approvals.New(ctx, approvals.Options{Store: s, Timeout: time.Minute})
	if mode != "" {
		_ = a.SetMode(ctx, "runner:laptop", mode, "admin", "")
	}
	srv := httptest.NewServer(api.Handler(api.Options{Store: s, Auth: auth.New(s), Launches: tokens, ApprovalQueue: a}))
	t.Cleanup(srv.Close)
	return srv, token, a
}

func runHookWith(t *testing.T, cfg config.Config, stdin string, c hookClient) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runHook(cfg, nil, strings.NewReader(stdin), &out, &errOut, c)
	return code, out.String(), errOut.String()
}

func TestHookAllowDenyAndWait(t *testing.T) {
	srv, token, a := hookServe(t, approvals.ModeCritical)
	cfg := config.Config{ServeURL: srv.URL + "/", LaunchToken: token}
	c := hookClient{http: srv.Client(), sleep: noSleep}

	code, out, _ := runHookWith(t, cfg, `{"tool":"Bash","command":"go test ./..."}`, c)
	if code != hookAllow || !strings.Contains(out, `"decision":"allow"`) {
		t.Fatalf("plain = %d %q", code, out)
	}

	// A critical call waits until someone answers.
	go func() {
		for range 200 {
			list, _ := a.List(context.Background(), "pending", 10)
			if len(list) == 1 {
				_, _ = a.Answer(context.Background(), list[0].ID, false, "admin", "")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	code, out, errOut := runHookWith(t, cfg, `{"agent":"musecode","tool":"Bash","command":"git reset --hard"}`, c)
	var ans map[string]string
	_ = json.Unmarshal([]byte(out), &ans)
	if code != hookDeny || ans["decision"] != "deny" || ans["id"] == "" || !strings.Contains(ans["reason"], "denied by admin") {
		t.Fatalf("critical = %d %q", code, out)
	}
	if !strings.Contains(errOut, "git reset --hard needs approval") {
		t.Fatalf("stderr = %q, want the waiting note", errOut)
	}
}

func TestHookFailures(t *testing.T) {
	srv, token, _ := hookServe(t, "")
	c := hookClient{http: srv.Client(), sleep: noSleep}
	cfg := config.Config{ServeURL: srv.URL, LaunchToken: token}

	// No mode: a policy deny.
	if code, out, _ := runHookWith(t, cfg, `{"tool":"Bash","command":"ls"}`, c); code != hookDeny || !strings.Contains(out, "no access mode") {
		t.Fatalf("unset mode = %d %q", code, out)
	}
	for name, tc := range map[string]struct {
		cfg   config.Config
		stdin string
		want  string
	}{
		"no token":   {config.Config{ServeURL: srv.URL}, `{"tool":"Bash"}`, "DEVCULES_LAUNCH_TOKEN"},
		"bad stdin":  {cfg, `nope`, "stdin must be"},
		"no tool":    {cfg, `{"command":"ls"}`, "stdin must be"},
		"extra key":  {cfg, `{"tool":"Bash","runner":"x"}`, "stdin must be"},
		"bad token":  {config.Config{ServeURL: srv.URL, LaunchToken: "dvl_nope"}, `{"tool":"Bash"}`, "refused the hook (401)"},
		"bad answer": {config.Config{ServeURL: srv.URL + "/healthz#", LaunchToken: token}, `{"tool":"Bash"}`, ""},
	} {
		code, _, errOut := runHookWith(t, tc.cfg, tc.stdin, c)
		if code != exitError || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: exit %d stderr %q", name, code, errOut)
		}
	}
}

func TestHookRetriesUntilServeAnswers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			_, _ = w.Write([]byte(`{"decision":"maybe"}`))
		}
	}))
	defer srv.Close()
	slept := 0
	c := hookClient{http: srv.Client(), sleep: func(context.Context, time.Duration) error { slept++; return nil }}
	code, _, errOut := runHookWith(t, config.Config{ServeURL: srv.URL, LaunchToken: "dvl_x"}, `{"tool":"Bash"}`, c)
	if code != exitError || slept != 1 || !strings.Contains(errOut, `serve answered "maybe"`) {
		t.Fatalf("exit %d slept %d stderr %q", code, slept, errOut)
	}
	// An unreachable serve is retried until the deadline, then fails.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	tries := 0
	c = hookClient{http: http.DefaultClient, sleep: func(ctx context.Context, _ time.Duration) error {
		if tries++; tries > 3 {
			return context.DeadlineExceeded
		}
		return nil
	}}
	code, _, errOut = runHookWith(t, config.Config{ServeURL: dead.URL, LaunchToken: "dvl_x"}, `{"tool":"Bash"}`, c)
	if code != exitError || !strings.Contains(errOut, "cannot reach serve") || strings.Contains(errOut, dead.URL) {
		t.Fatalf("dead serve = %d %q", code, errOut)
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(canceled, time.Hour); err == nil {
		t.Fatal("sleepCtx ignored its context")
	}
}

func TestAgentHook(t *testing.T) {
	srv, token, _ := hookServe(t, approvals.ModeCritical)
	muse, _ := hookAgent("musecode")
	c := hookClient{http: srv.Client(), sleep: noSleep}
	cfg := config.Config{ServeURL: srv.URL, LaunchToken: token}
	run := func(cfg config.Config, stdin string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runHook(cfg, muse, strings.NewReader(stdin), &out, &errOut, c)
		return code, out.String(), errOut.String()
	}
	call := func(cmd string) string {
		return `{"hook_event_name":"PreToolUse","tool_name":"shell","tool_input":{"command":"` + cmd + `"}}`
	}

	if code, out, errOut := run(cfg, call("go test ./...")); code != 0 || out != "" || errOut != "" {
		t.Fatalf("allow = %d %q %q", code, out, errOut)
	}
	// Off blocks, and the reason reaches the agent on stderr.
	srvOff, tokenOff, _ := hookServe(t, approvals.ModeOff)
	c.http = srvOff.Client()
	if code, _, errOut := run(config.Config{ServeURL: srvOff.URL, LaunchToken: tokenOff}, call("ls")); code != 2 ||
		!strings.Contains(errOut, "Devcules blocked this call") {
		t.Fatalf("deny = %d %q", code, errOut)
	}
	c.http = srv.Client()
	// Every failure blocks the call.
	for name, tc := range map[string]struct {
		cfg   config.Config
		stdin string
		want  string
	}{
		"no token":   {config.Config{ServeURL: srv.URL}, call("ls"), "no launch token"},
		"bad stdin":  {cfg, `nope`, "could not check"},
		"bad event":  {cfg, `{"hook_event_name":"Stop"}`, "could not check"},
		"bad token":  {config.Config{ServeURL: srv.URL, LaunchToken: "dvl_nope"}, call("ls"), "refused the hook (401)"},
		"odd answer": {config.Config{ServeURL: srv.URL + "/healthz#", LaunchToken: token}, call("ls"), "could not check"},
	} {
		code, out, errOut := run(tc.cfg, tc.stdin)
		if code != 2 || out != "" || !strings.Contains(errOut, tc.want) || strings.Contains(errOut, token) {
			t.Errorf("%s: exit %d stdout %q stderr %q", name, code, out, errOut)
		}
	}
	odd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"decision":"maybe"}`))
	}))
	defer odd.Close()
	c.http = odd.Client()
	if code, _, errOut := run(config.Config{ServeURL: odd.URL, LaunchToken: "dvl_x"}, call("ls")); code != 2 || !strings.Contains(errOut, `serve answered "maybe"`) {
		t.Fatalf("odd decision = %d %q", code, errOut)
	}
}

func TestAgentHookReadFailure(t *testing.T) {
	muse, _ := hookAgent("musecode")
	var out, errOut bytes.Buffer
	code := runHook(config.Config{LaunchToken: "dvl_x"}, muse, failReader{}, &out, &errOut, hookClient{})
	if code != 2 || !strings.Contains(errOut.String(), "cannot read the tool call") {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
