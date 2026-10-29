package audit

import (
	"bytes"
	"context"
	gosql "database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

func newLog(t *testing.T) (*Log, store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.db")
	dbPaths.Store(t.Name(), path)
	s, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	l := New(s, nil)
	at := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	l.now = func() time.Time { at = at.Add(1500 * time.Millisecond); return at }
	return l, s
}

// dbPaths maps a test to its database file, for tamper.
var dbPaths sync.Map

func record(t *testing.T, l *Log, action, target string) Entry {
	t.Helper()
	e, err := l.Record(context.Background(), Event{Actor: "admin", Action: action, Target: target, IP: "192.0.2.7", Detail: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRecordChainsAndVerifies(t *testing.T) {
	l, _ := newLog(t)
	ctx := context.Background()
	if n, err := l.Verify(ctx); n != 0 || err != nil {
		t.Fatalf("empty log: %d %v", n, err)
	}
	a := record(t, l, SignIn, "")
	b := record(t, l, LaunchMint, "lt_1")
	if a.Seq != 1 || b.Seq != 2 || b.PrevHash != a.Hash || a.PrevHash != strings.Repeat("0", 64) {
		t.Fatalf("chain: %+v %+v", a, b)
	}
	if n, err := l.Verify(ctx); n != 2 || err != nil {
		t.Fatalf("Verify = %d, %v", n, err)
	}
	recent, err := l.Recent(ctx, 0, 0)
	if err != nil || len(recent) != 2 || recent[0].Seq != 2 || recent[0].Detail["k"] != "v" {
		t.Fatalf("Recent = %+v, %v", recent, err)
	}
	older, _ := l.Recent(ctx, 2, 10)
	if len(older) != 1 || older[0].Seq != 1 {
		t.Fatalf("Recent before 2 = %+v", older)
	}
}

func TestRecordValidatesAndClips(t *testing.T) {
	l, _ := newLog(t)
	ctx := context.Background()
	if _, err := l.Record(ctx, Event{Action: SignIn}); err == nil {
		t.Fatal("no actor accepted")
	}
	detail := map[string]string{}
	for i := range 40 {
		detail[strings.Repeat("k", i+1)] = "v"
	}
	e, err := l.Record(ctx, Event{Actor: "admin", Action: SignIn, Target: strings.Repeat("é", 300), Detail: detail})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Target) > maxField+len("…") || !strings.HasSuffix(e.Target, "…") || len(e.Detail) != maxDetailItems {
		t.Fatalf("not clipped: %d bytes, %d details", len(e.Target), len(e.Detail))
	}
	if _, err := l.Verify(ctx); err != nil {
		t.Fatalf("a clipped entry must still verify: %v", err)
	}
}

// tamper changes stored rows behind the triggers' back, as someone
// with the database file could.
func tamper(t *testing.T, sql string) {
	t.Helper()
	path, _ := dbPaths.Load(t.Name())
	db, err := gosql.Open("sqlite", path.(string))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`DROP TRIGGER audit_log_no_update`, `DROP TRIGGER audit_log_no_delete`, sql} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestVerifyCatchesTampering(t *testing.T) {
	for _, tc := range []struct {
		name, sql, reason string
		seq               int64
	}{
		{"edited field", `UPDATE audit_log SET actor = 'mallory' WHERE seq = 2`, "changed", 2},
		{"deleted row", `DELETE FROM audit_log WHERE seq = 2`, "missing", 2},
		{"relinked", `UPDATE audit_log SET prev_hash = x'00' WHERE seq = 3`, "link", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := newLog(t)
			for range 3 {
				record(t, l, SignIn, "")
			}
			tamper(t, tc.sql)
			_, err := l.Verify(context.Background())
			var be *BrokenError
			if !errors.As(err, &be) || be.Seq != tc.seq || !strings.Contains(be.Reason, tc.reason) {
				t.Fatalf("Verify = %v, want broken at %d (%s)", err, tc.seq, tc.reason)
			}
		})
	}
}

func TestExportAndVerifyLines(t *testing.T) {
	l, _ := newLog(t)
	ctx := context.Background()
	for i := range exportPage + 3 {
		record(t, l, TraceRead, string(rune('a'+i%26)))
	}
	var buf bytes.Buffer
	n, err := l.Export(ctx, &buf, 0)
	if err != nil || n != exportPage+3 {
		t.Fatalf("Export = %d, %v", n, err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != n {
		t.Fatalf("%d lines for %d entries", len(lines), n)
	}
	if got, err := VerifyLines(strings.NewReader(buf.String())); got != int64(n) || err != nil {
		t.Fatalf("VerifyLines = %d, %v", got, err)
	}
	var tail bytes.Buffer
	if n, _ := l.Export(ctx, &tail, int64(exportPage)); n != 3 {
		t.Fatalf("Export after = %d, want 3", n)
	}

	// An edited line, a dropped line and junk each fail.
	var first Entry
	_ = json.Unmarshal([]byte(lines[0]), &first)
	first.Actor = "mallory"
	edited, _ := json.Marshal(first)
	for name, text := range map[string]string{
		"edited":  string(edited) + "\n" + strings.Join(lines[1:], "\n"),
		"dropped": lines[0] + "\n" + strings.Join(lines[2:], "\n"),
		"junk":    "{nope",
		"bad hex": `{"seq":1,"prevHash":"zz","hash":"zz"}`,
	} {
		if _, err := VerifyLines(strings.NewReader(text)); err == nil {
			t.Errorf("%s export verified", name)
		}
	}
}

// TestConcurrentRecord has two Logs (serve and a CLI) share one
// database: every entry lands, in one unbroken chain.
func TestConcurrentRecord(t *testing.T) {
	l1, s := newLog(t)
	l2 := New(s, nil)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := l1
			if i%2 == 1 {
				l = l2
			}
			if _, err := l.Record(context.Background(), Event{Actor: "admin", Action: SignIn}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n, err := l1.Verify(context.Background()); n != 20 || err != nil {
		t.Fatalf("Verify = %d, %v", n, err)
	}
}

type failingRepo struct{ store.AuditRepo }

func (failingRepo) Last(context.Context) (store.AuditEntry, error) {
	return store.AuditEntry{}, errors.New("disk on fire")
}

func (failingRepo) After(context.Context, int64, int) ([]store.AuditEntry, error) {
	return nil, errors.New("disk on fire")
}

func (failingRepo) Before(context.Context, int64, int) ([]store.AuditEntry, error) {
	return nil, errors.New("disk on fire")
}

func TestStoreErrors(t *testing.T) {
	l, _ := newLog(t)
	l.repo = failingRepo{l.repo}
	ctx := context.Background()
	if _, err := l.Record(ctx, Event{Actor: "a", Action: "b"}); err == nil || !strings.Contains(err.Error(), "fire") {
		t.Fatalf("Record = %v", err)
	}
	if _, err := l.Recent(ctx, 0, 5); err == nil {
		t.Fatal("Recent hid an error")
	}
	if _, err := l.Export(ctx, &bytes.Buffer{}, 0); err == nil {
		t.Fatal("Export hid an error")
	}
	if _, err := l.Verify(ctx); err == nil {
		t.Fatal("Verify hid an error")
	}
}
