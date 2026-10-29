package sqlite

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// TestAuditAppendOnly proves the database itself refuses to rewrite
// the audit log: raw UPDATE and DELETE fail, even past the repo.
func TestAuditAppendOnly(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	e := store.AuditEntry{Seq: 1, At: time.Now(), Actor: "admin", Action: "auth.sign_in", PrevHash: []byte{0}, Hash: []byte{1}}
	if err := s.Audit().Append(ctx, e); err != nil {
		t.Fatal(err)
	}
	db := s.(*sqliteStore).db
	for _, q := range []string{
		`UPDATE audit_log SET actor = 'mallory' WHERE seq = 1`,
		`DELETE FROM audit_log WHERE seq = 1`,
		`DELETE FROM audit_log`,
	} {
		_, err := db.ExecContext(ctx, q)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: err = %v, want the append-only refusal", q, err)
		}
	}
	got, err := s.Audit().Last(ctx)
	if err != nil || got.Actor != "admin" {
		t.Fatalf("after the refused writes: %+v, %v", got, err)
	}
}

// TestAuditRace has many writers race for the same next seq: exactly
// one wins each round, the rest get ErrConflict, and no seq repeats.
func TestAuditRace(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	const writers = 8
	var wg sync.WaitGroup
	wins := make(chan int, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Audit().Append(ctx, store.AuditEntry{Seq: 1, At: time.Now(), Actor: "w", Action: "race"})
			if err == nil {
				wins <- i
			}
		}()
	}
	wg.Wait()
	close(wins)
	if n := len(wins); n != 1 {
		t.Fatalf("%d writers took seq 1, want exactly one", n)
	}
}

// TestTraceTimesSortAsText guards the fixed-width time format: a
// whole-second time must sort before a later fractional one.
func TestTraceTimesSortAsText(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)
	for _, ts := range []time.Time{at, at.Add(500 * time.Millisecond)} {
		if _, err := s.Traces().Add(ctx, store.TraceCall{LaunchID: "l", At: ts}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Traces().DeleteBefore(ctx, at.Add(100*time.Millisecond))
	if err != nil || n != 1 {
		t.Fatalf("DeleteBefore = %d, %v; want exactly the whole-second call", n, err)
	}
}
