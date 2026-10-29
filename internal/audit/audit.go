package audit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/devcules/devcules/internal/store"
)

// Actions recorded by Devcules. Callers may add their own; these are
// the ones the UI knows how to describe.
const (
	SignIn          = "auth.sign_in"
	SignInFailed    = "auth.sign_in_failed"
	SignOut         = "auth.sign_out"
	PasskeyAdd      = "passkey.add"
	PasskeyRename   = "passkey.rename"
	PasskeyRemove   = "passkey.remove"
	TerminalOpen    = "terminal.open"
	TerminalClose   = "terminal.close"
	RedactPolicy    = "redact.policy"
	LaunchMint      = "launch.mint"
	LaunchRevoke    = "launch.revoke"
	TraceRead       = "trace.read"
	AuditExport     = "audit.export"
	VaultRotate     = "vault.rotate"
	MCPCall         = "mcp.call"
	ApprovalAsk     = "approval.ask"
	ApprovalAllow   = "approval.allow"
	ApprovalDeny    = "approval.deny"
	ApprovalExpire  = "approval.expire"
	ApprovalBlocked = "approval.blocked"
	ApprovalMode    = "approval.mode"
	ApprovalRules   = "approval.rules"
	PushSubscribe   = "push.subscribe"
	PushRemove      = "push.remove"
	AgentLaunch     = "agent.launch"
	// Machines: minting a code, a machine enrolling itself, revoking.
	MachineEnrolCode = "machine.enrol_code"
	MachineEnrol     = "machine.enrol"
	MachineRemove    = "machine.remove"
	maxField         = 200
	maxDetailItems   = 16
)

// Event is what a caller records. Actor is a username, or "system" or
// "cli" for Devcules itself; Target names what was acted on (an id, a
// name). Detail holds short, non-secret context.
type Event struct {
	Actor  string
	Action string
	Target string
	IP     string
	Detail map[string]string
}

// Entry is one stored record, as the API and export show it.
type Entry struct {
	Seq      int64             `json:"seq"`
	At       time.Time         `json:"at"`
	Actor    string            `json:"actor"`
	Action   string            `json:"action"`
	Target   string            `json:"target,omitempty"`
	IP       string            `json:"ip,omitempty"`
	Detail   map[string]string `json:"detail,omitempty"`
	PrevHash string            `json:"prevHash"`
	Hash     string            `json:"hash"`
}

// Log appends to and reads the audit log.
type Log struct {
	repo store.AuditRepo
	log  *slog.Logger
	now  func() time.Time
	mu   sync.Mutex
}

// New returns a Log over the store's audit repo. logger reports
// failed appends (callers carry on: an action that already happened is
// not undone because its record failed); nil discards.
func New(s store.Store, logger *slog.Logger) *Log {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Log{repo: s.Audit(), log: logger, now: time.Now}
}

// genesis is the PrevHash of entry 1.
var genesis = make([]byte, sha256.Size)

// hashEntry chains e to prev: SHA-256 over a version tag, prev, and a
// JSON array of the fields. Detail is a JSON object, whose keys
// encoding/json sorts, so the bytes are canonical.
func hashEntry(prev []byte, e store.AuditEntry) []byte {
	fields, _ := json.Marshal([]any{e.Seq, e.At.UTC().Format(time.RFC3339Nano), e.Actor, e.Action, e.Target, e.IP, nonNilMap(e.Detail)})
	h := sha256.New()
	h.Write([]byte("devcules-audit-v1\n"))
	h.Write(prev)
	h.Write(fields)
	return h.Sum(nil)
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// clip trims s to maxField bytes on a rune boundary.
func clip(s string) string {
	if len(s) <= maxField {
		return s
	}
	s = s[:maxField]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// Record appends ev. It retries when another writer (serve and a CLI
// command share the database) took the next sequence number first.
func (l *Log) Record(ctx context.Context, ev Event) (Entry, error) {
	if ev.Action == "" || ev.Actor == "" {
		return Entry{}, errors.New("audit: an event needs an actor and an action")
	}
	e := store.AuditEntry{
		Actor:  clip(ev.Actor),
		Action: clip(ev.Action),
		Target: clip(ev.Target),
		IP:     clip(ev.IP),
		Detail: map[string]string{},
	}
	for k, v := range ev.Detail {
		if len(e.Detail) == maxDetailItems {
			break
		}
		e.Detail[clip(k)] = clip(v)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	for try := range 50 {
		if try > 0 {
			// Another process won the race; let it finish.
			time.Sleep(time.Duration(rand.IntN(2000)) * time.Microsecond)
		}
		prev := genesis
		e.Seq = 1
		last, lerr := l.repo.Last(ctx)
		switch {
		case lerr == nil:
			prev, e.Seq = last.Hash, last.Seq+1
		case !errors.Is(lerr, store.ErrNotFound):
			err = lerr
			continue
		}
		e.At = l.now().UTC()
		e.PrevHash = prev
		e.Hash = hashEntry(prev, e)
		if err = l.repo.Append(ctx, e); err == nil {
			return toEntry(e), nil
		}
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	l.log.Error("audit append failed", "action", e.Action, "err", err.Error())
	return Entry{}, fmt.Errorf("audit: append %s: %w", e.Action, err)
}

func toEntry(e store.AuditEntry) Entry {
	out := Entry{Seq: e.Seq, At: e.At, Actor: e.Actor, Action: e.Action, Target: e.Target, IP: e.IP,
		PrevHash: hex.EncodeToString(e.PrevHash), Hash: hex.EncodeToString(e.Hash)}
	if len(e.Detail) > 0 {
		out.Detail = e.Detail
	}
	return out
}

// Recent returns up to limit entries older than before (0 = newest),
// newest first.
func (l *Log) Recent(ctx context.Context, before int64, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := l.repo.Before(ctx, before, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEntry(e))
	}
	return out, nil
}

// exportPage is how many entries Export and Verify read at a time.
const exportPage = 500

// Export writes every entry after seq after as one JSON object per
// line, oldest first, and returns how many it wrote.
func (l *Log) Export(ctx context.Context, w io.Writer, after int64) (int, error) {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	n := 0
	for {
		rows, err := l.repo.After(ctx, after, exportPage)
		if err != nil {
			return n, err
		}
		for _, e := range rows {
			if err := enc.Encode(toEntry(e)); err != nil {
				return n, err
			}
			n++
			after = e.Seq
		}
		if len(rows) < exportPage {
			return n, bw.Flush()
		}
	}
}

// BrokenError says where the chain stops holding.
type BrokenError struct {
	Seq    int64
	Reason string
}

func (e *BrokenError) Error() string {
	return fmt.Sprintf("audit: chain broken at entry %d: %s", e.Seq, e.Reason)
}

// Verify walks the whole log and checks every link: sequence numbers
// without gaps, each PrevHash equal to the entry before's Hash, and
// each Hash matching the entry's fields. It returns how many entries
// it checked, and a *BrokenError at the first bad one.
func (l *Log) Verify(ctx context.Context) (int64, error) {
	prev := genesis
	var seq int64
	for {
		rows, err := l.repo.After(ctx, seq, exportPage)
		if err != nil {
			return seq, err
		}
		for _, e := range rows {
			switch {
			case e.Seq != seq+1:
				return seq, &BrokenError{seq + 1, fmt.Sprintf("entry %d is missing (next is %d)", seq+1, e.Seq)}
			case !bytes.Equal(e.PrevHash, prev):
				return seq, &BrokenError{e.Seq, "does not link to the entry before"}
			case !bytes.Equal(e.Hash, hashEntry(prev, e)):
				return seq, &BrokenError{e.Seq, "its fields were changed after it was written"}
			}
			prev, seq = e.Hash, e.Seq
		}
		if len(rows) < exportPage {
			return seq, nil
		}
	}
}

// VerifyLines checks an exported JSON-lines stream on its own, with no
// database: the same rules as Verify, from the exported hashes.
func VerifyLines(r io.Reader) (int64, error) {
	dec := json.NewDecoder(r)
	prev := genesis
	var seq int64
	for {
		var x Entry
		if err := dec.Decode(&x); errors.Is(err, io.EOF) {
			return seq, nil
		} else if err != nil {
			return seq, fmt.Errorf("audit: line %d: %w", seq+1, err)
		}
		ph, err1 := hex.DecodeString(x.PrevHash)
		h, err2 := hex.DecodeString(x.Hash)
		e := store.AuditEntry{Seq: x.Seq, At: x.At, Actor: x.Actor, Action: x.Action, Target: x.Target, IP: x.IP, Detail: x.Detail}
		switch {
		case err1 != nil || err2 != nil:
			return seq, &BrokenError{x.Seq, "hash is not hex"}
		case x.Seq != seq+1:
			return seq, &BrokenError{seq + 1, fmt.Sprintf("entry %d is missing (next is %d)", seq+1, x.Seq)}
		case !bytes.Equal(ph, prev):
			return seq, &BrokenError{x.Seq, "does not link to the entry before"}
		case !bytes.Equal(h, hashEntry(prev, e)):
			return seq, &BrokenError{x.Seq, "its fields were changed after it was written"}
		}
		prev, seq = h, x.Seq
	}
}
