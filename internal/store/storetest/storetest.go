package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

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

	t.Run("Ping", func(t *testing.T) {
		s := newStore(t)
		mustf(t, s.Ping(ctx), "Ping")
		mustf(t, s.Close(), "Close")
		mustFail(t, s.Ping(ctx), "Ping after Close")
	})

	t.Run("Concurrent", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 25 {
					k := fmt.Sprintf("%s/w%d/%d", t.Name(), w, i)
					if err := s.Meta().Set(ctx, k, "v"); err != nil {
						errs <- err
						return
					}
					if _, err := s.Meta().Get(ctx, k); err != nil {
						errs <- err
						return
					}
					if err := s.Ping(ctx); err != nil {
						errs <- err
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
	})
}
