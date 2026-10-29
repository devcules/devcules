package approvals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/push"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
)

// Meta keys of the owner's settings.
const (
	modesKey = "approvals.modes"
	rulesKey = "approvals.rules"
)

const (
	// DefaultTimeout is how long a pending approval waits for a person.
	DefaultTimeout = 10 * time.Minute
	// MaxCommand caps the command text a hook may send.
	MaxCommand = 64 << 10
	// shownCommand caps the command as stored and shown.
	shownCommand = 4 << 10
	maxTool      = 100
	maxAgent     = 40
	maxTarget    = 80
)

// ErrNotFound reports an unknown approval, or one that belongs to
// another launch.
var ErrNotFound = errors.New("approvals: no such approval")

// ErrAnswered reports an approval that already has its answer.
var ErrAnswered = errors.New("approvals: already answered")

// Notifier sends the push notice; internal/push is the real one.
type Notifier interface {
	Send(ctx context.Context, m push.Message) (push.Result, error)
}

// Options wires a Service. Store is required.
type Options struct {
	Store store.Store
	// AuditLog records asks, answers and settings changes; nil records
	// nothing.
	AuditLog *audit.Log
	// Push sends the notice for a new pending approval; nil sends none.
	Push Notifier
	// Redact masks secrets in a command before it is stored or shown;
	// nil stores it as sent.
	Redact *redact.Redactor
	// Timeout is how long an approval waits; 0 is DefaultTimeout.
	Timeout time.Duration
	Log     *slog.Logger
}

// Service runs the policy and holds pending approvals.
type Service struct {
	o    Options
	repo store.ApprovalsRepo
	meta store.MetaRepo
	now  func() time.Time

	mu      sync.Mutex
	modes   map[string]Mode
	rules   ruleSet
	waiters map[string]chan struct{}
}

// New loads the saved modes and rules.
func New(ctx context.Context, o Options) (*Service, error) {
	if o.Store == nil {
		return nil, errors.New("approvals: a store is required")
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Service{o: o, repo: o.Store.Approvals(), meta: o.Store.Meta(), now: time.Now, modes: map[string]Mode{}, waiters: map[string]chan struct{}{}}
	if raw, err := s.meta.Get(ctx, modesKey); err == nil {
		if err := json.Unmarshal([]byte(raw), &s.modes); err != nil {
			return nil, fmt.Errorf("approvals: saved modes (meta %s) do not parse: %w", modesKey, err)
		}
		for t, m := range s.modes {
			if !m.Valid() {
				return nil, fmt.Errorf("approvals: saved mode %q for %s is unknown", m, t)
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var saved []Rule
	if raw, err := s.meta.Get(ctx, rulesKey); err == nil {
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return nil, fmt.Errorf("approvals: saved rules (meta %s) do not parse: %w", rulesKey, err)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	rules, err := merge(saved)
	if err != nil {
		return nil, fmt.Errorf("approvals: saved rules (meta %s): %w", rulesKey, err)
	}
	if s.rules, err = compile(rules); err != nil {
		return nil, err
	}
	return s, nil
}

// Timeout is how long an approval waits.
func (s *Service) Timeout() time.Duration { return s.o.Timeout }

// Request is one tool call a hook reports. Launch fields come from the
// launch token, never from the hook's body.
type Request struct {
	LaunchID     string
	Runner       string
	Agent        string
	Tool         string
	Command      string
	Irreversible bool
}

// Approval is one approval as the API shows it.
type Approval struct {
	ID           string    `json:"id"`
	LaunchID     string    `json:"launchId"`
	Runner       string    `json:"runner"`
	Agent        string    `json:"agent"`
	Tool         string    `json:"tool"`
	Command      string    `json:"command"`
	Rule         string    `json:"rule,omitempty"`
	RuleName     string    `json:"ruleName,omitempty"`
	Irreversible bool      `json:"irreversible"`
	Status       string    `json:"status"`
	Created      time.Time `json:"created"`
	Expires      time.Time `json:"expires"`
	Answered     time.Time `json:"answered,omitzero"`
	AnsweredBy   string    `json:"answeredBy,omitempty"`
}

// Outcome is the policy's answer to one request. Approval is set when
// the call waits for a person (Verdict Ask while pending).
type Outcome struct {
	Verdict  Verdict   `json:"decision"`
	Reason   string    `json:"reason"`
	Approval *Approval `json:"approval,omitempty"`
}

func (s *Service) view(a store.Approval) *Approval {
	v := &Approval{ID: a.ID, LaunchID: a.LaunchID, Runner: a.Runner, Agent: a.Agent, Tool: a.Tool, Command: a.Command,
		Rule: a.Rule, Irreversible: a.Irreversible, Status: a.Status, Created: a.CreatedAt,
		Expires: a.CreatedAt.Add(s.o.Timeout), Answered: a.AnsweredAt, AnsweredBy: a.AnsweredBy}
	s.mu.Lock()
	for _, r := range s.rules.rules {
		if r.ID == a.Rule {
			v.RuleName = r.Name
		}
	}
	s.mu.Unlock()
	return v
}

// outcome reads an approval's state as the hook's answer.
func (s *Service) outcome(a store.Approval) Outcome {
	o := Outcome{Approval: s.view(a)}
	switch a.Status {
	case store.ApprovalAllowed:
		o.Verdict, o.Reason = Allow, "allowed once by "+a.AnsweredBy
	case store.ApprovalDenied:
		o.Verdict, o.Reason = Deny, "denied by "+a.AnsweredBy
	case store.ApprovalExpired:
		o.Verdict, o.Reason = Deny, "nobody answered in time"
	default:
		o.Verdict, o.Reason = Ask, "waiting for approval"
	}
	return o
}

// clean trims a field, refusing invalid UTF-8 and control characters
// other than newlines and tabs in commands.
func clean(name, v string, max int, multiline bool) (string, error) {
	if len(v) > max {
		return "", fmt.Errorf("approvals: %s is longer than %d bytes", name, max)
	}
	if !utf8.ValidString(v) {
		return "", fmt.Errorf("approvals: %s is not UTF-8", name)
	}
	if strings.ContainsFunc(v, func(r rune) bool {
		return r < 0x20 && !(multiline && (r == '\n' || r == '\t' || r == '\r')) || r == 0x7f
	}) {
		return "", fmt.Errorf("approvals: %s holds control characters", name)
	}
	return v, nil
}

// Ask runs the policy for one tool call. Allow and Deny return at
// once (a policy Deny is audited as approval.blocked); Ask stores a
// pending approval, audits approval.ask, and pushes the notice. A
// validation error starts with "approvals: " and means a bad request.
func (s *Service) Ask(ctx context.Context, r Request) (Outcome, error) {
	var err error
	if r.Tool, err = clean("tool", strings.TrimSpace(r.Tool), maxTool, false); err != nil {
		return Outcome{}, err
	}
	if r.Tool == "" {
		return Outcome{}, errors.New("approvals: tool is required")
	}
	if r.Agent, err = clean("agent", strings.TrimSpace(r.Agent), maxAgent, false); err != nil {
		return Outcome{}, err
	}
	if r.Command, err = clean("command", r.Command, MaxCommand, true); err != nil {
		return Outcome{}, err
	}
	s.mu.Lock()
	mode := s.modes[Target(r.Runner)]
	rule, matched := s.rules.match(r.Command)
	s.mu.Unlock()
	verdict, reason := Decide(mode, matched || r.Irreversible)
	if matched && verdict != Allow {
		reason += ": " + rule.Name
	}
	switch verdict {
	case Allow:
		return Outcome{Verdict: Allow, Reason: reason}, nil
	case Deny:
		s.record(ctx, audit.Event{Actor: "launch:" + r.LaunchID, Action: audit.ApprovalBlocked, Target: r.Tool,
			Detail: map[string]string{"runner": r.Runner, "mode": string(mode), "rule": rule.ID}})
		return Outcome{Verdict: Deny, Reason: reason}, nil
	}

	id := make([]byte, 8)
	_, _ = rand.Read(id)
	a := store.Approval{ID: hex.EncodeToString(id), LaunchID: r.LaunchID, Runner: r.Runner, Agent: r.Agent, Tool: r.Tool,
		Command: s.shown(r.Command), Rule: rule.ID, Irreversible: r.Irreversible, Status: store.ApprovalPending,
		CreatedAt: s.now().UTC()}
	if err := s.repo.Create(ctx, a); err != nil {
		return Outcome{}, err
	}
	s.record(ctx, audit.Event{Actor: "launch:" + r.LaunchID, Action: audit.ApprovalAsk, Target: a.ID,
		Detail: map[string]string{"runner": r.Runner, "agent": r.Agent, "tool": r.Tool, "rule": rule.ID}})
	s.notify(a, rule)
	out := s.outcome(a)
	out.Reason = reason
	return out, nil
}

// shown is the command as stored: secrets masked whatever the policy
// mode (a person reads it, and it may reach a screenshot), and capped.
func (s *Service) shown(command string) string {
	if s.o.Redact != nil {
		p := redact.Policy{}
		for _, t := range redact.Types {
			p[t] = redact.Off
		}
		p[redact.Secret] = redact.Mask
		if masked, _, err := s.o.Redact.NewTestSession(p).Redact(command); err == nil {
			command = masked
		}
	}
	if len(command) > shownCommand {
		cut := shownCommand
		for cut > 0 && !utf8.RuneStart(command[cut]) {
			cut--
		}
		command = command[:cut] + " …(cut)"
	}
	return command
}

// notify pushes the notice in the background: the hook never waits on
// a push service. The notice names the agent, the runner and the
// rule, never the command.
func (s *Service) notify(a store.Approval, rule Rule) {
	if s.o.Push == nil {
		return
	}
	who := a.Agent
	if who == "" {
		who = "An agent"
	}
	where := a.Runner
	if where == "" {
		where = "a launch with no runner"
	}
	what := "a " + a.Tool + " call"
	if rule.Name != "" {
		what = rule.Name
	} else if a.Irreversible {
		what = "an irreversible " + a.Tool + " call"
	}
	m := push.Message{Title: "Approval needed", Body: fmt.Sprintf("%s on %s wants to run %s.", who, where, what),
		URL: "/?approval=" + a.ID, Tag: "approval-" + a.ID, TTL: s.o.Timeout, Urgent: true}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.o.Push.Send(ctx, m); err != nil {
			s.o.Log.Warn("approval push failed", "approval", a.ID, "err", err.Error())
		}
	}()
}

func (s *Service) record(ctx context.Context, ev audit.Event) {
	if s.o.AuditLog != nil {
		_, _ = s.o.AuditLog.Record(ctx, ev)
	}
}

// waiter returns the channel closed when id is answered.
func (s *Service) waiter(id string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.waiters[id]
	if !ok {
		ch = make(chan struct{})
		s.waiters[id] = ch
	}
	return ch
}

func (s *Service) wake(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.waiters[id]; ok {
		close(ch)
		delete(s.waiters, id)
	}
}

// Wait returns the approval's outcome once it is answered or wait has
// passed (then still Ask). Only the launch that asked may wait on it.
// An approval past its timeout is expired here.
func (s *Service) Wait(ctx context.Context, launchID, id string, wait time.Duration) (Outcome, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	triedExpiry := false
	for {
		ch := s.waiter(id)
		a, err := s.repo.Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && a.LaunchID != launchID) {
			return Outcome{}, ErrNotFound
		}
		if err != nil {
			return Outcome{}, err
		}
		if a.Status == store.ApprovalPending && !s.now().Before(a.CreatedAt.Add(s.o.Timeout)) && !triedExpiry {
			triedExpiry = true
			s.Expire(ctx)
			continue
		}
		if a.Status != store.ApprovalPending {
			return s.outcome(a), nil
		}
		if triedExpiry {
			// Expiry failed (logged): answer as still waiting.
			return s.outcome(a), nil
		}
		expiry := time.NewTimer(a.CreatedAt.Add(s.o.Timeout).Sub(s.now()))
		select {
		case <-ch:
		case <-expiry.C:
		case <-deadline.C:
			expiry.Stop()
			return s.outcome(a), nil
		case <-ctx.Done():
			expiry.Stop()
			return Outcome{}, ctx.Err()
		}
		expiry.Stop()
	}
}

// Answer records a person's Allow once or Deny. The first answer wins:
// a later one is ErrAnswered.
func (s *Service) Answer(ctx context.Context, id string, allow bool, by, ip string) (*Approval, error) {
	status, action := store.ApprovalDenied, audit.ApprovalDeny
	if allow {
		status, action = store.ApprovalAllowed, audit.ApprovalAllow
	}
	err := s.repo.Answer(ctx, id, status, by, s.now().UTC())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return nil, ErrAnswered
	case err != nil:
		return nil, err
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.record(ctx, audit.Event{Actor: by, Action: action, Target: id, IP: ip,
		Detail: map[string]string{"runner": a.Runner, "agent": a.Agent, "tool": a.Tool, "rule": a.Rule}})
	s.wake(id)
	return s.view(a), nil
}

// Expire ends every approval past its timeout, auditing each as
// approval.expire.
func (s *Service) Expire(ctx context.Context) int {
	now := s.now().UTC()
	ids, err := s.repo.ExpirePending(ctx, now.Add(-s.o.Timeout), now)
	if err != nil {
		s.o.Log.Error("approval expiry failed", "err", err.Error())
		return 0
	}
	for _, id := range ids {
		s.record(ctx, audit.Event{Actor: "system", Action: audit.ApprovalExpire, Target: id})
		s.wake(id)
	}
	return len(ids)
}

// Run expires approvals every interval until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Expire(ctx)
		}
	}
}

// List returns approvals newest first: status "" for all, or one of
// pending, allowed, denied, expired. Limit is 1 to 200 (default 50).
func (s *Service) List(ctx context.Context, status string, limit int) ([]*Approval, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	switch status {
	case "", store.ApprovalPending, store.ApprovalAllowed, store.ApprovalDenied, store.ApprovalExpired:
	default:
		return nil, fmt.Errorf("approvals: unknown status %q", status)
	}
	rows, err := s.repo.List(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*Approval, 0, len(rows))
	for _, a := range rows {
		out = append(out, s.view(a))
	}
	return out, nil
}

// Modes returns the access mode per target.
func (s *Service) Modes() map[string]Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.modes)
}

// SetMode saves target's mode and audits the change.
func (s *Service) SetMode(ctx context.Context, target string, mode Mode, by, ip string) error {
	if !mode.Valid() {
		return fmt.Errorf("approvals: mode must be one of off, critical, all, full")
	}
	runner, isRunner := strings.CutPrefix(target, "runner:")
	if target != Unbound && (!isRunner || runner == "" || len(target) > maxTarget || strings.ContainsFunc(runner, func(r rune) bool { return r < 0x20 })) {
		return fmt.Errorf("approvals: target must be %q or runner:<name>", Unbound)
	}
	s.mu.Lock()
	old := s.modes[target]
	next := maps.Clone(s.modes)
	next[target] = mode
	raw, _ := json.Marshal(next)
	if err := s.meta.Set(ctx, modesKey, string(raw)); err != nil {
		s.mu.Unlock()
		return err
	}
	s.modes = next
	s.mu.Unlock()
	if old != mode {
		s.record(ctx, audit.Event{Actor: by, Action: audit.ApprovalMode, Target: target, IP: ip,
			Detail: map[string]string{"from": string(old), "to": string(mode)}})
	}
	return nil
}

// Rules returns the live rules: built-ins first, then the owner's.
func (s *Service) Rules() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rules.rules)
}

// SetRules saves the owner's rule list (see merge) and audits what
// changed.
func (s *Service) SetRules(ctx context.Context, rules []Rule, by, ip string) error {
	merged, err := merge(rules)
	if err != nil {
		return err
	}
	compiled, err := compile(merged)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(merged)
	s.mu.Lock()
	if err := s.meta.Set(ctx, rulesKey, string(raw)); err != nil {
		s.mu.Unlock()
		return err
	}
	old := s.rules.rules
	s.rules = compiled
	s.mu.Unlock()
	detail := ruleChanges(old, merged)
	if len(detail) > 0 {
		s.record(ctx, audit.Event{Actor: by, Action: audit.ApprovalRules, IP: ip, Detail: detail})
	}
	return nil
}

// ruleChanges describes a rules edit for the audit log, by rule id.
func ruleChanges(old, next []Rule) map[string]string {
	out := map[string]string{}
	before := map[string]Rule{}
	for _, r := range old {
		before[r.ID] = r
	}
	for _, r := range next {
		o, ok := before[r.ID]
		delete(before, r.ID)
		switch {
		case !ok:
			out[r.ID] = "added"
		case o.Pattern != r.Pattern || o.Name != r.Name:
			out[r.ID] = "changed"
		case o.Enabled != r.Enabled && r.Enabled:
			out[r.ID] = "on"
		case o.Enabled != r.Enabled:
			out[r.ID] = "off"
		}
	}
	for id := range before {
		out[id] = "removed"
	}
	return out
}

// Check reports the rule a command matches, for the settings test box.
func (s *Service) Check(command string) (Rule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rules.match(command)
}
