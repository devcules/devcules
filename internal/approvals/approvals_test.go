package approvals

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/push"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

const fakeAWS = "AKIA" + "Z7QWERTY4UIOP3XZ"

type recPush struct {
	mu   sync.Mutex
	msgs []push.Message
	sent chan struct{}
	err  error
}

func (r *recPush) Send(_ context.Context, m push.Message) (push.Result, error) {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	r.sent <- struct{}{}
	return push.Result{Sent: 1}, r.err
}

type fixture struct {
	svc   *Service
	store store.Store
	log   *audit.Log
	push  *recPush
	clock *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	p := &recPush{sent: make(chan struct{}, 10)}
	log := audit.New(s, nil)
	svc, err := New(ctx, Options{Store: s, AuditLog: log, Push: p, Redact: r, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	return &fixture{svc: svc, store: s, log: log, push: p, clock: &now}
}

func (f *fixture) actions(t *testing.T) []string {
	t.Helper()
	entries, err := f.log.Recent(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		out = append(out, entries[i].Action+" "+entries[i].Target)
	}
	return out
}

func TestAskFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := Request{LaunchID: "l1", Runner: "laptop", Agent: "musecode", Tool: "Bash", Command: "git push --force origin main # key " + fakeAWS}

	// No mode yet: denied, and audited.
	out, err := f.svc.Ask(ctx, req)
	if err != nil || out.Verdict != Deny || !strings.Contains(out.Reason, "no access mode") || out.Approval != nil {
		t.Fatalf("unset mode = %+v %v", out, err)
	}
	if err := f.svc.SetMode(ctx, "runner:laptop", ModeCritical, "admin", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	plain := req
	plain.Command = "go test ./..."
	if out, _ := f.svc.Ask(ctx, plain); out.Verdict != Allow {
		t.Fatalf("plain call under critical = %+v", out)
	}

	out, err = f.svc.Ask(ctx, req)
	if err != nil || out.Verdict != Ask || out.Approval == nil || !strings.Contains(out.Reason, "Force push") {
		t.Fatalf("critical call = %+v %v", out, err)
	}
	a := out.Approval
	if a.Status != store.ApprovalPending || a.Rule != "force-push" || a.RuleName != "Force push" || a.Runner != "laptop" ||
		strings.Contains(a.Command, fakeAWS) || !strings.Contains(a.Command, "REDACTED_SECRET_") || !a.Expires.Equal(f.clock.Add(time.Minute)) {
		t.Fatalf("approval = %+v", a)
	}
	<-f.push.sent
	m := f.push.msgs[0]
	if m.Title != "Approval needed" || !strings.Contains(m.Body, "musecode on laptop wants to run Force push") ||
		strings.Contains(m.Body, "git push") || m.URL != "/?approval="+a.ID || !m.Urgent || m.TTL != time.Minute {
		t.Fatalf("push = %+v", m)
	}

	// Another launch cannot wait on it; the asker waits until answered.
	if _, err := f.svc.Wait(ctx, "l2", a.ID, time.Millisecond); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign wait = %v", err)
	}
	if out, err := f.svc.Wait(ctx, "l1", a.ID, 10*time.Millisecond); err != nil || out.Verdict != Ask {
		t.Fatalf("short wait = %+v %v", out, err)
	}
	done := make(chan Outcome)
	go func() {
		out, _ := f.svc.Wait(ctx, "l1", a.ID, time.Minute)
		done <- out
	}()
	time.Sleep(20 * time.Millisecond)
	answered, err := f.svc.Answer(ctx, a.ID, true, "admin", "192.0.2.1")
	if err != nil || answered.Status != store.ApprovalAllowed || answered.AnsweredBy != "admin" {
		t.Fatalf("Answer = %+v %v", answered, err)
	}
	if got := <-done; got.Verdict != Allow || !strings.Contains(got.Reason, "allowed once by admin") {
		t.Fatalf("waiter got %+v", got)
	}
	if _, err := f.svc.Answer(ctx, a.ID, false, "admin", ""); !errors.Is(err, ErrAnswered) {
		t.Fatalf("second answer = %v", err)
	}
	if _, err := f.svc.Answer(ctx, "nope", false, "admin", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown answer = %v", err)
	}

	// A deny reaches the agent too.
	out, _ = f.svc.Ask(ctx, Request{LaunchID: "l1", Runner: "laptop", Tool: "Write", Command: "notes.md", Irreversible: true})
	<-f.push.sent
	if !strings.Contains(f.push.msgs[1].Body, "An agent on laptop wants to run an irreversible Write call") {
		t.Fatalf("irreversible push = %+v", f.push.msgs[1])
	}
	if _, err := f.svc.Answer(ctx, out.Approval.ID, false, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Wait(ctx, "l1", out.Approval.ID, time.Second); got.Verdict != Deny || !strings.Contains(got.Reason, "denied by admin") {
		t.Fatalf("denied wait = %+v", got)
	}

	want := []string{"approval.blocked Bash", "approval.mode runner:laptop", "approval.ask " + a.ID, "approval.allow " + a.ID,
		"approval.ask " + out.Approval.ID, "approval.deny " + out.Approval.ID}
	if got := f.actions(t); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("audit =\n%v\nwant\n%v", got, want)
	}
	list, _ := f.svc.List(ctx, store.ApprovalAllowed, 0)
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("List allowed = %+v", list)
	}
	if _, err := f.svc.List(ctx, "bogus", 0); err == nil {
		t.Fatal("unknown status listed")
	}
}

func TestExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_ = f.svc.SetMode(ctx, Unbound, ModeAll, "admin", "")
	out, err := f.svc.Ask(ctx, Request{LaunchID: "l1", Tool: "Bash", Command: "ls"})
	if err != nil || out.Verdict != Ask || out.Approval.Runner != "" {
		t.Fatalf("unbound ask = %+v %v", out, err)
	}
	<-f.push.sent
	if !strings.Contains(f.push.msgs[0].Body, "a launch with no runner wants to run a Bash call") {
		t.Fatalf("push = %+v", f.push.msgs[0])
	}
	if n := f.svc.Expire(ctx); n != 0 {
		t.Fatalf("expired %d before the timeout", n)
	}
	*f.clock = f.clock.Add(2 * time.Minute)
	// Wait notices the timeout itself.
	got, err := f.svc.Wait(ctx, "l1", out.Approval.ID, time.Second)
	if err != nil || got.Verdict != Deny || !strings.Contains(got.Reason, "nobody answered") {
		t.Fatalf("expired wait = %+v %v", got, err)
	}
	if _, err := f.svc.Answer(ctx, out.Approval.ID, true, "admin", ""); !errors.Is(err, ErrAnswered) {
		t.Fatalf("answer after expiry = %v", err)
	}
	acts := f.actions(t)
	if acts[len(acts)-1] != "approval.expire "+out.Approval.ID {
		t.Fatalf("audit = %v", acts)
	}

	// Run expires on its ticker.
	out, _ = f.svc.Ask(ctx, Request{LaunchID: "l1", Tool: "Bash", Command: "ls"})
	<-f.push.sent
	*f.clock = f.clock.Add(2 * time.Minute)
	runCtx, stop := context.WithCancel(ctx)
	go f.svc.Run(runCtx, 5*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for {
		list, _ := f.svc.List(ctx, store.ApprovalExpired, 10)
		if len(list) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run never expired the approval")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
}

func TestModesAndRulesPersist(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, bad := range []struct {
		target string
		mode   Mode
	}{{"runner:x", "maybe"}, {"laptop", ModeAll}, {"runner:", ModeAll}, {"runner:a\nb", ModeAll}, {"runner:" + strings.Repeat("x", 80), ModeAll}} {
		if err := f.svc.SetMode(ctx, bad.target, bad.mode, "admin", ""); err == nil {
			t.Fatalf("SetMode(%q, %q) accepted", bad.target, bad.mode)
		}
	}
	if err := f.svc.SetMode(ctx, "runner:laptop", ModeFull, "admin", ""); err != nil {
		t.Fatal(err)
	}
	_ = f.svc.SetMode(ctx, "runner:laptop", ModeFull, "admin", "") // no change, no audit
	rules := f.svc.Rules()
	for i := range rules {
		if rules[i].ID == "drop-table" {
			rules[i].Enabled = false
		}
	}
	rules = append(rules, Rule{ID: "tf-destroy", Name: "Terraform destroy", Pattern: `\bterraform\s+destroy\b`, Enabled: true})
	if err := f.svc.SetRules(ctx, rules, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetRules(ctx, []Rule{{ID: "x", Name: "x", Pattern: "("}}, "admin", ""); err == nil {
		t.Fatal("a broken pattern saved")
	}
	if r, ok := f.svc.Check("terraform destroy -auto-approve"); !ok || r.ID != "tf-destroy" {
		t.Fatalf("Check = %+v %v", r, ok)
	}
	if _, ok := f.svc.Check("DROP TABLE x"); ok {
		t.Fatal("a switched-off rule still matched")
	}

	// A fresh service reads both back.
	again, err := New(ctx, Options{Store: f.store, AuditLog: f.log})
	if err != nil {
		t.Fatal(err)
	}
	if again.Modes()["runner:laptop"] != ModeFull || again.Timeout() != DefaultTimeout {
		t.Fatalf("modes = %v", again.Modes())
	}
	if _, ok := again.Check("terraform destroy"); !ok {
		t.Fatal("custom rule lost on reload")
	}
	// Removing the custom rule is audited as removed.
	_ = again.SetRules(ctx, DefaultRules(), "admin", "")
	entries, _ := f.log.Recent(ctx, 0, 1)
	if entries[0].Action != audit.ApprovalRules || entries[0].Detail["tf-destroy"] != "removed" || entries[0].Detail["drop-table"] != "on" {
		t.Fatalf("rules audit = %+v", entries[0])
	}
	modes := 0
	all, _ := f.log.Recent(ctx, 0, 100)
	for _, e := range all {
		if e.Action == audit.ApprovalMode {
			modes++
		}
	}
	if modes != 1 {
		t.Fatalf("mode changes audited %d times, want 1", modes)
	}

	// Broken saved settings stop New with a clear error.
	for key, raw := range map[string]string{modesKey: "{", rulesKey: "["} {
		_ = f.store.Meta().Set(ctx, key, raw)
		if _, err := New(ctx, Options{Store: f.store}); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s broken: %v", key, err)
		}
		_ = f.store.Meta().Delete(ctx, key)
	}
	_ = f.store.Meta().Set(ctx, modesKey, `{"unbound":"sometimes"}`)
	if _, err := New(ctx, Options{Store: f.store}); err == nil {
		t.Fatal("an unknown saved mode loaded")
	}
	if _, err := New(ctx, Options{}); err == nil {
		t.Fatal("New without a store")
	}
}

func TestAskValidates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for name, r := range map[string]Request{
		"no tool":      {Command: "x"},
		"long tool":    {Tool: strings.Repeat("t", maxTool+1)},
		"control tool": {Tool: "Ba\x1bsh"},
		"long agent":   {Tool: "Bash", Agent: strings.Repeat("a", maxAgent+1)},
		"long command": {Tool: "Bash", Command: strings.Repeat("c", MaxCommand+1)},
		"not utf8":     {Tool: "Bash", Command: "\xff"},
		"escape":       {Tool: "Bash", Command: "echo \x1b[2J"},
	} {
		if _, err := f.svc.Ask(ctx, r); err == nil || !strings.HasPrefix(err.Error(), "approvals: ") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A long command is stored cut, on a rune boundary.
	_ = f.svc.SetMode(ctx, Unbound, ModeAll, "admin", "")
	out, err := f.svc.Ask(ctx, Request{LaunchID: "l", Tool: "Bash", Command: "echo " + strings.Repeat("é", shownCommand) + "\nmore"})
	if err != nil || !strings.HasSuffix(out.Approval.Command, " …(cut)") || len(out.Approval.Command) > shownCommand+20 {
		t.Fatalf("cut = %d %v", len(out.Approval.Command), err)
	}
	<-f.push.sent
}

func TestPushFailureIsLogged(t *testing.T) {
	f := newFixture(t)
	var logs bytes.Buffer
	var mu sync.Mutex
	f.svc.o.Log = slogTo(&logs, &mu)
	f.push.err = errors.New("push service down")
	_ = f.svc.SetMode(context.Background(), Unbound, ModeAll, "admin", "")
	if _, err := f.svc.Ask(context.Background(), Request{LaunchID: "l", Tool: "Bash", Command: "ls"}); err != nil {
		t.Fatal(err)
	}
	<-f.push.sent
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		s := logs.String()
		mu.Unlock()
		if strings.Contains(s, "approval push failed") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs = %s", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
