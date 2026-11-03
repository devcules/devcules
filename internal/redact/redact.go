package redact

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Type is a detector family. Each has its own mode in a Policy.
type Type string

const (
	Secret Type = "SECRET"
	Email  Type = "EMAIL"
	Phone  Type = "PHONE"
	Card   Type = "CARD"
	IPv4   Type = "IPV4"
)

// Types lists every type in display order.
var Types = []Type{Secret, Email, Phone, Card, IPv4}

// Mode says what happens to a type's findings.
type Mode string

const (
	Off   Mode = "off"
	Log   Mode = "log"
	Mask  Mode = "mask"
	Block Mode = "block"
)

// Policy maps every type to a mode.
type Policy map[Type]Mode

// DefaultPolicy masks secrets and leaves personal data off: PII
// scanning is opt-in (PLAN.md §4.2).
func DefaultPolicy() Policy {
	return Policy{Secret: Mask, Email: Off, Phone: Off, Card: Off, IPv4: Off}
}

// Validate reports an unknown type or mode, or a missing type.
func (p Policy) Validate() error {
	for t, m := range p {
		if !slices.Contains(Types, t) {
			return fmt.Errorf("redact: unknown type %q (want one of %v)", t, Types)
		}
		switch m {
		case Off, Log, Mask, Block:
		default:
			return fmt.Errorf("redact: unknown mode %q for %s (want off, log, mask or block)", m, t)
		}
	}
	for _, t := range Types {
		if _, ok := p[t]; !ok {
			return fmt.Errorf("redact: policy has no mode for %s", t)
		}
	}
	return nil
}

// ErrBlocked reports text holding a type whose mode is block.
var ErrBlocked = errors.New("redact: blocked")

// BlockedError names the blocked types, never the values.
type BlockedError struct{ Types []Type }

func (e *BlockedError) Error() string {
	names := make([]string, len(e.Types))
	for i, t := range e.Types {
		names[i] = string(t)
	}
	return "redact: request blocked: it contains " + strings.Join(names, ", ") + " (redaction mode block)"
}

func (e *BlockedError) Unwrap() error { return ErrBlocked }

// Finding is one detection. It never carries the value.
type Finding struct {
	Type        Type   `json:"type"`
	Rule        string `json:"rule"`
	Placeholder string `json:"placeholder,omitempty"`
}

// Counts are a type's running totals since serve started.
type Counts struct {
	Detected uint64 `json:"detected"`
	Masked   uint64 `json:"masked"`
	Blocked  uint64 `json:"blocked"`
}

type counters struct{ detected, masked, blocked atomic.Uint64 }

// Redactor holds the compiled detectors, the placeholder key and the
// counters. It is safe for concurrent use; one per serve.
type Redactor struct {
	rules  []rule
	key    []byte
	counts map[Type]*counters
	// known is the holder's live set of credentials it must never send
	// a model (the "known" values): the placeholder key, runner
	// link tokens, every provider key. find masks them always, off the
	// policy: masking a key the desk holds is never wrong, and the
	// secret rules only catch keys that match a vendor's pattern.
	knownMu sync.RWMutex
	known   []string
}

// New compiles the detectors. key is the per-install placeholder key
// (32 random bytes kept in the vault); it never leaves the Redactor.
func New(key []byte) (*Redactor, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("redact: placeholder key must be at least 32 bytes, got %d", len(key))
	}
	rules, err := loadRules()
	if err != nil {
		return nil, err
	}
	counts := make(map[Type]*counters, len(Types))
	for _, t := range Types {
		counts[t] = &counters{}
	}
	return &Redactor{rules: rules, key: slices.Clone(key), counts: counts}, nil
}

// Rules reports how many secret rules are loaded.
func (r *Redactor) Rules() int { return len(r.rules) }

// minKnown is the shortest value worth masking: shorter ones are
// fragmentation bait, not credentials (the vault refuses anything
// under 8 anyway).
const minKnown = 8

// SetKnown replaces the always-mask values. Values are secrets: they
// stay in memory as supplied but never enter a log, an audit row or a
// placeholder's input beyond the one-way HMAC.
func (r *Redactor) SetKnown(values []string) {
	seen := map[string]bool{}
	known := make([]string, 0, len(values))
	for _, v := range values {
		if len(v) >= minKnown && !seen[v] {
			seen[v] = true
			known = append(known, v)
		}
	}
	r.knownMu.Lock()
	r.known = known
	r.knownMu.Unlock()
}

// Stats returns a copy of the counters.
func (r *Redactor) Stats() map[Type]Counts {
	out := make(map[Type]Counts, len(r.counts))
	for t, c := range r.counts {
		out[t] = Counts{Detected: c.detected.Load(), Masked: c.masked.Load(), Blocked: c.blocked.Load()}
	}
	return out
}

// placeholder derives the stable stand-in for value of type t.
func (r *Redactor) placeholder(t Type, value string) string {
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte(t))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return "REDACTED_" + string(t) + "_" + strings.ToUpper(hex.EncodeToString(mac.Sum(nil)[:5]))
}

// span is one finding's position in the scanned text.
type span struct {
	start, end int
	typ        Type
	rule       string
}

// find runs every detector whose mode is not off. The desk's own
// credentials are matches under Secret whatever the mode says: they
// are the values the holder must never send a model (the
// "known" detector), so the policy only decides whether to mask or
// block one, never whether to miss it.
func (r *Redactor) find(text string, p Policy) []span {
	var spans []span
	if p[Secret] != Off && p[Secret] != "" {
		lower := strings.ToLower(text)
		for i := range r.rules {
			for _, m := range r.rules[i].matches(text, lower) {
				spans = append(spans, span{m[0], m[1], Secret, r.rules[i].id})
			}
		}
		r.knownMu.RLock()
		for _, v := range r.known {
			for off := 0; ; {
				i := strings.Index(text[off:], v)
				if i < 0 {
					break
				}
				start := off + i
				spans = append(spans, span{start, start + len(v), Secret, "desk-secret"})
				off = start + len(v)
			}
		}
		r.knownMu.RUnlock()
	}
	for i := range piiDetectors {
		d := &piiDetectors[i]
		if p[d.typ] == Off || p[d.typ] == "" {
			continue
		}
		for _, m := range d.matches(text) {
			spans = append(spans, span{m[0], m[1], d.typ, strings.ToLower(string(d.typ))})
		}
	}
	return resolveOverlaps(spans)
}

// resolveOverlaps keeps one span per stretch of text: the earliest,
// then the longest. Detectors overlap (a Gitleaks rule and an email
// can claim the same text), and a placeholder replaces one span only.
func resolveOverlaps(spans []span) []span {
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	kept := spans[:0]
	end := -1
	for _, s := range spans {
		if s.start < end {
			continue
		}
		kept = append(kept, s)
		end = s.end
	}
	return kept
}

// Session redacts one model exchange: the request text in, and the
// reply out. It remembers which placeholder stands for which value, so
// only placeholders it issued are restored.
type Session struct {
	r      *Redactor
	policy Policy
	count  bool
	mu     sync.Mutex
	values map[string]string
}

// NewSession starts an exchange under policy p, which must validate.
func (r *Redactor) NewSession(p Policy) *Session {
	return &Session{r: r, policy: p, count: true, values: map[string]string{}}
}

// NewTestSession is NewSession for the settings test box: it does not
// touch the counters.
func (r *Redactor) NewTestSession(p Policy) *Session {
	s := r.NewSession(p)
	s.count = false
	return s
}

// Redact returns text with masked types replaced and the findings. A
// type in block mode fails the whole text with a *BlockedError and
// returns no text.
func (s *Session) Redact(text string) (string, []Finding, error) {
	spans := s.r.find(text, s.policy)
	findings := make([]Finding, 0, len(spans))
	var blocked []Type
	for _, sp := range spans {
		if s.count {
			s.r.counts[sp.typ].detected.Add(1)
		}
		if s.policy[sp.typ] == Block && !slices.Contains(blocked, sp.typ) {
			blocked = append(blocked, sp.typ)
		}
	}
	if len(blocked) > 0 {
		if s.count {
			for _, t := range blocked {
				s.r.counts[t].blocked.Add(1)
			}
		}
		for _, sp := range spans {
			findings = append(findings, Finding{Type: sp.typ, Rule: sp.rule})
		}
		return "", findings, &BlockedError{Types: blocked}
	}
	var b strings.Builder
	last := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sp := range spans {
		f := Finding{Type: sp.typ, Rule: sp.rule}
		if s.policy[sp.typ] == Mask {
			value := text[sp.start:sp.end]
			ph := s.r.placeholder(sp.typ, value)
			s.values[ph] = value
			b.WriteString(text[last:sp.start])
			b.WriteString(ph)
			last = sp.end
			f.Placeholder = ph
			if s.count {
				s.r.counts[sp.typ].masked.Add(1)
			}
		}
		findings = append(findings, f)
	}
	b.WriteString(text[last:])
	return b.String(), findings, nil
}

// Issued reports how many distinct placeholders this session has
// handed out. With none, a reply has nothing to restore.
func (s *Session) Issued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.values)
}

// placeholderRE matches a whole placeholder. Types hold no underscore,
// so the second underscore always starts the 10 hex digits.
var placeholderRE = regexp.MustCompile(`REDACTED_[A-Z0-9]+_[0-9A-F]{10}`)

// HasPlaceholder reports whether text holds a redaction marker, the
// assistant loop's cheap "an argument was masked" check. It is the
// redactor's own pattern, exported here so no caller grows a second
// copy that can drift.
func HasPlaceholder(text []byte) bool { return placeholderRE.Match(text) }

// Restore puts back every placeholder this session issued. Others,
// including look-alikes the model invented, stay as they are.
func (s *Session) Restore(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return placeholderRE.ReplaceAllStringFunc(text, func(ph string) string {
		if v, ok := s.values[ph]; ok {
			return v
		}
		return ph
	})
}
