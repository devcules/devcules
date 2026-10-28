package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// The suite asserts through four helpers instead of inline ifs. A passing
// run only executes taken branches, so a per-check if would leave its
// failure branch uncovered no matter how thorough the suite is; helpers
// keep one failure branch each. The broken-store probe run exercises those
// branches, proving they fail loudly when behaviour is wrong.
func mustf(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %s", msg, err)
	}
}

func check(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Fatalf("want %s", msg)
	}
}

func mustNotFound(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%s = %v, want an error wrapping ErrNotFound", msg, err)
	}
}

func mustFail(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded, want an error", msg)
	}
}

// mustCancel requires err to be a cancellation callers can match with
// errors.Is, so a readiness probe can tell "cancelled" from "broken".
func mustCancel(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%s: error = %v, want one matching context.Canceled with errors.Is", msg, err)
	}
}

func mustExists(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, store.ErrExists) {
		t.Fatalf("%s = %v, want an error wrapping ErrExists", msg, err)
	}
}

// Run executes the shared store.Store contract suite. newStore opens a
// store; every call in one Run shares the same database, so subtests use
// unique keys and the reopen subtest owns the close/reopen sequence.
// Implementations call it once from their own test with a constructor
// pointed at an isolated database.
func Run(t *testing.T, newStore func(t *testing.T) store.Store) {
	t.Helper()
	ctx := context.Background()
	key := func(t *testing.T, name string) string {
		t.Helper()
		return t.Name() + "/" + name
	}

	t.Run("FreshMigrates", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		v, err := s.SchemaVersion(ctx)
		mustf(t, err, "SchemaVersion")
		check(t, v >= 1, fmt.Sprintf("version at least 1 after a fresh open, got %d", v))
		mustf(t, s.Meta().Set(ctx, key(t, "fresh"), "1"), "Meta.Set on a fresh store")
	})

	t.Run("ReopenKeepsData", func(t *testing.T) {
		s1 := newStore(t)
		v1, err := s1.SchemaVersion(ctx)
		mustf(t, err, "SchemaVersion")
		k := key(t, "survivor")
		mustf(t, s1.Meta().Set(ctx, k, "v1"), "Set")
		mustf(t, s1.Close(), "Close")
		s2 := newStore(t)
		defer s2.Close()
		v2, err := s2.SchemaVersion(ctx)
		mustf(t, err, "SchemaVersion after reopen")
		check(t, v2 == v1, fmt.Sprintf("version %d after reopen, want the pre-close %d (reopen must be a no-op)", v2, v1))
		got, err := s2.Meta().Get(ctx, k)
		mustf(t, err, "Get after reopen")
		check(t, got == "v1", fmt.Sprintf("value %q after reopen, want %q", got, "v1"))
	})

	t.Run("Meta", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		m := s.Meta()
		k := key(t, "k")

		mustf(t, m.Set(ctx, k, "one"), "Set")
		got, err := m.Get(ctx, k)
		mustf(t, err, "Get")
		check(t, got == "one", fmt.Sprintf("value %q, want %q", got, "one"))
		mustf(t, m.Set(ctx, k, "two"), "overwrite Set")
		got, err = m.Get(ctx, k)
		mustf(t, err, "Get after overwrite")
		check(t, got == "two", fmt.Sprintf("value %q, want %q", got, "two"))
		_, err = m.Get(ctx, key(t, "missing"))
		mustNotFound(t, err, "Get missing")
		mustf(t, m.Delete(ctx, k), "Delete")
		_, err = m.Get(ctx, k)
		mustNotFound(t, err, "Get after Delete")
		mustf(t, m.Delete(ctx, key(t, "missing")), "Delete missing (must be a no-op)")
		mustf(t, m.Set(ctx, key(t, "empty"), ""), "Set empty value")
		got, err = m.Get(ctx, key(t, "empty"))
		mustf(t, err, "Get empty value")
		check(t, got == "", fmt.Sprintf("empty value %q, want %q", got, ""))
		mustFail(t, m.Set(ctx, "", "v"), "Set empty key")
	})

	t.Run("Users", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		u := s.Users()
		name := key(t, "alice")

		_, err := u.PasswordHash(ctx, name)
		mustNotFound(t, err, "PasswordHash unknown user")
		mustFail(t, u.Create(ctx, "", "hash"), "Create empty username")
		mustFail(t, u.Create(ctx, name, ""), "Create empty hash")
		mustf(t, u.Create(ctx, name, "hash-one"), "Create")
		got, err := u.PasswordHash(ctx, name)
		mustf(t, err, "PasswordHash")
		check(t, got == "hash-one", fmt.Sprintf("hash %q, want %q", got, "hash-one"))
		mustf(t, u.Create(ctx, key(t, "bob"), "hash-bob"), "Create second user")
		mustExists(t, u.Create(ctx, name, "hash-two"), "Create duplicate user")
		got, err = u.PasswordHash(ctx, name)
		mustf(t, err, "PasswordHash after duplicate Create")
		check(t, got == "hash-one", fmt.Sprintf("hash %q after duplicate, want %q (duplicates must not overwrite)", got, "hash-one"))
	})

	t.Run("Sessions", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Sessions()
		user := key(t, "carol")
		mustf(t, s.Users().Create(ctx, user, "hash"), "Create user for sessions")
		at := time.Now().UTC().Truncate(time.Second)
		sess := store.Session{TokenHash: key(t, "tok"), Username: user, CreatedAt: at, LastSeen: at}

		_, err := r.Get(ctx, sess.TokenHash)
		mustNotFound(t, err, "Get unknown session")
		mustNotFound(t, r.Touch(ctx, key(t, "missing"), at), "Touch missing session")
		mustf(t, r.Create(ctx, sess), "Create")
		mustExists(t, r.Create(ctx, sess), "Create duplicate session")
		got, err := r.Get(ctx, sess.TokenHash)
		mustf(t, err, "Get")
		check(t, got.TokenHash == sess.TokenHash && got.Username == sess.Username &&
			got.CreatedAt.Equal(sess.CreatedAt) && got.LastSeen.Equal(sess.LastSeen),
			fmt.Sprintf("session %+v, want %+v (times compare by instant, not ==: storage drops monotonic clocks)", got, sess))
		later := at.Add(time.Hour)
		mustf(t, r.Touch(ctx, sess.TokenHash, later), "Touch")
		got, err = r.Get(ctx, sess.TokenHash)
		mustf(t, err, "Get after Touch")
		check(t, got.LastSeen.Equal(later), fmt.Sprintf("last seen %s, want %s", got.LastSeen, later))
		check(t, got.CreatedAt.Equal(at), fmt.Sprintf("created %s changed by Touch, want %s", got.CreatedAt, at))
		mustf(t, r.Delete(ctx, sess.TokenHash), "Delete")
		_, err = r.Get(ctx, sess.TokenHash)
		mustNotFound(t, err, "Get after Delete")
		mustf(t, r.Delete(ctx, sess.TokenHash), "Delete missing (must be a no-op)")
	})

	t.Run("Ping", func(t *testing.T) {
		s := newStore(t)
		mustf(t, s.Ping(ctx), "Ping")
		mustf(t, s.Close(), "Close")
		mustFail(t, s.Ping(ctx), "Ping after Close")
	})

	t.Run("Concurrent", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		// Sessions reference a user, so one exists before the race.
		user := key(t, "racer")
		mustf(t, s.Users().Create(ctx, user, "hash"), "Create race user")
		var wg sync.WaitGroup
		errs := make(chan error, 128)
		race := func(err error) bool {
			if err != nil {
				errs <- err
				return true
			}
			return false
		}
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 25 {
					k := fmt.Sprintf("%s/w%d/%d", t.Name(), w, i)
					if race(s.Meta().Set(ctx, k, "v")) {
						return
					}
					if _, err := s.Meta().Get(ctx, k); race(err) {
						return
					}
					at := time.Now().UTC().Truncate(time.Second)
					if race(s.Sessions().Create(ctx, store.Session{TokenHash: k, Username: user, CreatedAt: at, LastSeen: at})) {
						return
					}
					if _, err := s.Sessions().Get(ctx, k); race(err) {
						return
					}
					if race(s.Sessions().Touch(ctx, k, at)) {
						return
					}
					if race(s.Sessions().Delete(ctx, k)) {
						return
					}
					if race(s.Ping(ctx)) {
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			mustf(t, err, "concurrent use")
		}
	})

	t.Run("ContextCanceled", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		canceled, stop := context.WithCancel(ctx)
		stop()
		_, err := s.Meta().Get(canceled, key(t, "k"))
		mustCancel(t, err, "Get with canceled context")
		mustCancel(t, s.Meta().Set(canceled, key(t, "k"), "v"), "Set with canceled context")
		mustCancel(t, s.Ping(canceled), "Ping with canceled context")
		mustCancel(t, s.Users().Create(canceled, key(t, "u"), "h"), "Users.Create with canceled context")
		_, err = s.Users().PasswordHash(canceled, key(t, "u"))
		mustCancel(t, err, "Users.PasswordHash with canceled context")
		_, err = s.Sessions().Get(canceled, key(t, "h"))
		mustCancel(t, err, "Sessions.Get with canceled context")
	})
}
