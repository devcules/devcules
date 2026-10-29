package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
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

func mustConflict(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("%s = %v, want an error wrapping ErrConflict", msg, err)
	}
}

func mustExists(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, store.ErrExists) {
		t.Fatalf("%s = %v, want an error wrapping ErrExists", msg, err)
	}
}

// checkPasskey requires got to equal want field by field. Times
// compare by instant: storage drops monotonic clocks.
func checkPasskey(t *testing.T, got, want store.Passkey, msg string) {
	t.Helper()
	check(t, bytes.Equal(got.ID, want.ID), fmt.Sprintf("%s id %x, want %x", msg, got.ID, want.ID))
	check(t, got.Username == want.Username, fmt.Sprintf("%s username %q, want %q", msg, got.Username, want.Username))
	check(t, bytes.Equal(got.PublicKey, want.PublicKey), fmt.Sprintf("%s public key %x, want %x", msg, got.PublicKey, want.PublicKey))
	check(t, got.SignCount == want.SignCount, fmt.Sprintf("%s sign count %d, want %d", msg, got.SignCount, want.SignCount))
	check(t, bytes.Equal(got.AAGUID, want.AAGUID), fmt.Sprintf("%s AAGUID %x, want %x", msg, got.AAGUID, want.AAGUID))
	check(t, slices.Equal(got.Transports, want.Transports), fmt.Sprintf("%s transports %q, want %q", msg, got.Transports, want.Transports))
	check(t, got.BackupEligible == want.BackupEligible, fmt.Sprintf("%s backup eligible %v, want %v", msg, got.BackupEligible, want.BackupEligible))
	check(t, got.BackupState == want.BackupState, fmt.Sprintf("%s backup state %v, want %v", msg, got.BackupState, want.BackupState))
	check(t, got.Name == want.Name, fmt.Sprintf("%s name %q, want %q", msg, got.Name, want.Name))
	check(t, got.CreatedAt.Equal(want.CreatedAt), fmt.Sprintf("%s created %s, want %s", msg, got.CreatedAt, want.CreatedAt))
	check(t, got.LastUsedAt.Equal(want.LastUsedAt), fmt.Sprintf("%s last used %s, want %s", msg, got.LastUsedAt, want.LastUsedAt))
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
	id := func(t *testing.T, name string) []byte {
		t.Helper()
		return []byte(t.Name() + "/" + name)
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

	t.Run("Passkeys", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Passkeys()
		user := key(t, "dana")
		mustf(t, s.Users().Create(ctx, user, "hash"), "Create user for passkeys")
		at := time.Now().UTC().Truncate(time.Second)
		aaguid := bytes.Repeat([]byte{0x7a}, 16)
		first := store.Passkey{
			ID: id(t, "first"), Username: user, PublicKey: []byte("cose-key-one"),
			SignCount: 3, AAGUID: aaguid, Transports: []string{"internal", "hybrid"},
			BackupEligible: true, BackupState: false,
			Name: "Phone", CreatedAt: at, LastUsedAt: at,
		}

		_, err := r.Get(ctx, id(t, "missing"))
		mustNotFound(t, err, "Get unknown passkey")
		got, err := r.ListByUser(ctx, key(t, "nobody"))
		mustf(t, err, "ListByUser unknown user")
		check(t, len(got) == 0, fmt.Sprintf("ListByUser unknown user has %d entries, want none", len(got)))
		bad := first
		bad.ID = nil
		mustFail(t, r.Create(ctx, bad), "Create empty id")
		bad = first
		bad.Username = ""
		mustFail(t, r.Create(ctx, bad), "Create empty username")
		bad = first
		bad.PublicKey = nil
		mustFail(t, r.Create(ctx, bad), "Create empty public key")
		bad = first
		bad.Name = ""
		mustFail(t, r.Create(ctx, bad), "Create empty name")

		mustf(t, r.Create(ctx, first), "Create")
		gotOne, err := r.Get(ctx, first.ID)
		mustf(t, err, "Get")
		checkPasskey(t, gotOne, first, "stored passkey")
		mustExists(t, r.Create(ctx, first), "Create duplicate passkey")
		gotOne, err = r.Get(ctx, first.ID)
		mustf(t, err, "Get after duplicate Create")
		checkPasskey(t, gotOne, first, "passkey after duplicate Create (duplicates must not overwrite)")

		second := first
		second.ID = id(t, "second")
		second.PublicKey = []byte("cose-key-two")
		second.Transports = nil
		second.BackupEligible = false
		second.Name = "Laptop"
		second.CreatedAt = at.Add(time.Hour)
		second.LastUsedAt = at.Add(time.Hour)
		mustf(t, r.Create(ctx, second), "Create second passkey")
		// Same-instant tie: the credential id orders the list.
		tieA := second
		tieA.ID = []byte(key(t, "tie/a"))
		tieA.PublicKey = []byte("cose-key-tie-a")
		tieA.Name = "Tie A"
		tieB := second
		tieB.ID = []byte(key(t, "tie/b"))
		tieB.PublicKey = []byte("cose-key-tie-b")
		tieB.Name = "Tie B"
		mustf(t, r.Create(ctx, tieB), "Create tie B first")
		mustf(t, r.Create(ctx, tieA), "Create tie A second")
		listed, err := r.ListByUser(ctx, user)
		mustf(t, err, "ListByUser")
		check(t, len(listed) == 4, fmt.Sprintf("ListByUser has %d entries, want 4", len(listed)))
		if len(listed) == 4 {
			checkPasskey(t, listed[0], first, "ListByUser oldest")
			checkPasskey(t, listed[1], second, "ListByUser second")
			checkPasskey(t, listed[2], tieA, "ListByUser tie broken by id")
			checkPasskey(t, listed[3], tieB, "ListByUser tie broken by id")
		}

		used := at.Add(2 * time.Hour)
		mustNotFound(t, r.UpdateAfterLogin(ctx, id(t, "missing"), 9, true, used), "UpdateAfterLogin missing passkey")
		mustf(t, r.UpdateAfterLogin(ctx, first.ID, 9, true, used), "UpdateAfterLogin")
		after := first
		after.SignCount = 9
		after.BackupState = true
		after.LastUsedAt = used
		gotOne, err = r.Get(ctx, first.ID)
		mustf(t, err, "Get after UpdateAfterLogin")
		checkPasskey(t, gotOne, after, "passkey after UpdateAfterLogin (only count, backup state and use time change)")

		mustFail(t, r.Rename(ctx, first.ID, ""), "Rename empty name")
		mustNotFound(t, r.Rename(ctx, id(t, "missing"), "Ghost"), "Rename missing passkey")
		mustf(t, r.Rename(ctx, first.ID, "Tablet"), "Rename")
		after.Name = "Tablet"
		gotOne, err = r.Get(ctx, first.ID)
		mustf(t, err, "Get after Rename")
		checkPasskey(t, gotOne, after, "passkey after Rename (only the name changes)")

		mustf(t, r.Delete(ctx, first.ID), "Delete")
		_, err = r.Get(ctx, first.ID)
		mustNotFound(t, err, "Get after Delete")
		mustf(t, r.Delete(ctx, first.ID), "Delete missing (must be a no-op)")
	})

	t.Run("WebAuthnHandle", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		u := s.Users()
		_, err := u.EnsureWebAuthnHandle(ctx, key(t, "ghost"))
		mustNotFound(t, err, "EnsureWebAuthnHandle unknown user")
		mustf(t, u.Create(ctx, key(t, "erin"), "hash"), "Create user for handle")
		first, err := u.EnsureWebAuthnHandle(ctx, key(t, "erin"))
		mustf(t, err, "EnsureWebAuthnHandle")
		check(t, len(first) == 32, fmt.Sprintf("handle length = %d, want 32 random bytes", len(first)))
		again, err := u.EnsureWebAuthnHandle(ctx, key(t, "erin"))
		mustf(t, err, "EnsureWebAuthnHandle again")
		check(t, bytes.Equal(again, first), "second EnsureWebAuthnHandle minted a new handle, want the stored one")
		mustf(t, u.Create(ctx, key(t, "fred"), "hash"), "Create second user for handle")
		other, err := u.EnsureWebAuthnHandle(ctx, key(t, "fred"))
		mustf(t, err, "EnsureWebAuthnHandle second user")
		check(t, !bytes.Equal(other, first), "two users share one handle, want distinct handles")
	})

	t.Run("Secrets", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Secrets()
		name := key(t, "provider/meta")
		at := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)

		_, err := r.Get(ctx, name)
		mustNotFound(t, err, "Get missing secret")
		mustFail(t, r.Put(ctx, "", []byte("x"), at), "Put with an empty name")
		mustFail(t, r.Put(ctx, name, nil, at), "Put with an empty value")
		mustf(t, r.Put(ctx, name, []byte{1, 2, 3}, at), "Put")
		got, err := r.Get(ctx, name)
		mustf(t, err, "Get")
		check(t, bytes.Equal(got, []byte{1, 2, 3}), fmt.Sprintf("sealed = %v, want [1 2 3]", got))
		mustf(t, r.Put(ctx, name, []byte{4}, at.Add(time.Minute)), "overwrite Put")
		got, err = r.Get(ctx, name)
		mustf(t, err, "Get after overwrite")
		check(t, bytes.Equal(got, []byte{4}), fmt.Sprintf("sealed = %v after overwrite, want [4]", got))

		other := key(t, "a-first")
		mustf(t, r.Put(ctx, other, []byte{9}, at), "Put second")
		names, err := r.Names(ctx)
		mustf(t, err, "Names")
		check(t, slices.Contains(names, name) && slices.Contains(names, other), fmt.Sprintf("Names = %v, want both stored names", names))
		check(t, slices.IsSorted(names), fmt.Sprintf("Names = %v, want sorted", names))

		mustf(t, r.Delete(ctx, name), "Delete")
		_, err = r.Get(ctx, name)
		mustNotFound(t, err, "Get after Delete")
		mustf(t, r.Delete(ctx, name), "Delete missing (must be a no-op)")
	})

	t.Run("LaunchTokens", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.LaunchTokens()
		at := time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)
		id := key(t, "lt")
		hash := []byte(key(t, "hash-") + "0123456789abcdef")
		tok := store.LaunchToken{ID: id, TokenHash: hash, Label: "musecode in t1", Provider: "meta", Runner: "laptop", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}

		_, err := r.ByHash(ctx, hash)
		mustNotFound(t, err, "ByHash missing token")
		mustFail(t, r.Create(ctx, store.LaunchToken{TokenHash: hash}), "Create with an empty id")
		mustFail(t, r.Create(ctx, store.LaunchToken{ID: id}), "Create with an empty hash")
		mustf(t, r.Create(ctx, tok), "Create")
		mustExists(t, r.Create(ctx, tok), "Create duplicate id")
		dupHash := tok
		dupHash.ID = key(t, "other")
		mustExists(t, r.Create(ctx, dupHash), "Create duplicate hash")

		got, err := r.ByHash(ctx, hash)
		mustf(t, err, "ByHash")
		check(t, got.ID == id && got.Label == tok.Label && got.Provider == "meta" && got.Runner == "laptop" &&
			got.CreatedAt.Equal(at) && got.ExpiresAt.Equal(at.Add(time.Hour)) &&
			got.RevokedAt.IsZero() && got.LastUsedAt.IsZero(), fmt.Sprintf("ByHash = %+v", got))

		mustf(t, r.Touch(ctx, id, at.Add(time.Minute)), "Touch")
		mustf(t, r.Revoke(ctx, id, at.Add(2*time.Minute)), "Revoke")
		mustf(t, r.Revoke(ctx, id, at.Add(3*time.Minute)), "Revoke again")
		got, err = r.ByHash(ctx, hash)
		mustf(t, err, "ByHash after Revoke")
		check(t, got.LastUsedAt.Equal(at.Add(time.Minute)), fmt.Sprintf("LastUsedAt = %v", got.LastUsedAt))
		check(t, got.RevokedAt.Equal(at.Add(2*time.Minute)), fmt.Sprintf("RevokedAt = %v, want the first revoke", got.RevokedAt))
		mustNotFound(t, r.Touch(ctx, key(t, "nope"), at), "Touch missing")
		mustNotFound(t, r.Revoke(ctx, key(t, "nope"), at), "Revoke missing")

		newer := store.LaunchToken{ID: key(t, "lt2"), TokenHash: []byte(key(t, "hash2-") + "0123456789abcdef"), CreatedAt: at.Add(time.Hour), ExpiresAt: at.Add(3 * time.Hour)}
		mustf(t, r.Create(ctx, newer), "Create second")
		list, err := r.List(ctx)
		mustf(t, err, "List")
		var ids []string
		for _, lt := range list {
			if lt.ID == id || lt.ID == newer.ID {
				ids = append(ids, lt.ID)
			}
		}
		check(t, slices.Equal(ids, []string{newer.ID, id}), fmt.Sprintf("List order = %v, want newest first", ids))

		n, err := r.DeleteExpired(ctx, at.Add(2*time.Hour))
		mustf(t, err, "DeleteExpired")
		check(t, n >= 1, fmt.Sprintf("DeleteExpired removed %d, want at least 1", n))
		_, err = r.ByHash(ctx, hash)
		mustNotFound(t, err, "ByHash after DeleteExpired")
		_, err = r.ByHash(ctx, newer.TokenHash)
		mustf(t, err, "ByHash of an unexpired token after DeleteExpired")
	})

	t.Run("Audit", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Audit()
		// The log may hold earlier runs' entries on a shared backend:
		// continue from wherever it is.
		var base int64
		last, err := r.Last(ctx)
		if err == nil {
			base = last.Seq
		} else {
			mustNotFound(t, err, "Last on an empty log")
		}
		at := time.Date(2026, 10, 29, 4, 0, 0, 123456789, time.UTC)
		mk := func(seq int64, action string) store.AuditEntry {
			return store.AuditEntry{Seq: seq, At: at.Add(time.Duration(seq) * time.Second), Actor: "admin",
				Action: action, Target: key(t, "t"), IP: "192.0.2.1",
				Detail: map[string]string{"provider": "meta"}, PrevHash: []byte{byte(seq)}, Hash: []byte{byte(seq + 1)}}
		}
		mustFail(t, r.Append(ctx, store.AuditEntry{Seq: 0}), "Append seq 0")
		mustConflict(t, r.Append(ctx, mk(base+2, "skip")), "Append skipping a seq")
		first := mk(base+1, "auth.sign_in")
		mustf(t, r.Append(ctx, first), "Append")
		mustConflict(t, r.Append(ctx, mk(base+1, "again")), "Append a taken seq")
		second := mk(base+2, "launch.mint")
		second.Detail = nil
		mustf(t, r.Append(ctx, second), "Append with nil detail")

		got, err := r.Last(ctx)
		mustf(t, err, "Last")
		check(t, got.Seq == base+2 && got.Action == "launch.mint" && got.Detail != nil && len(got.Detail) == 0,
			fmt.Sprintf("Last = %+v", got))
		after, err := r.After(ctx, base, 10)
		mustf(t, err, "After")
		check(t, len(after) == 2 && after[0].Seq == base+1 && after[1].Seq == base+2, fmt.Sprintf("After = %d entries", len(after)))
		a := after[0]
		check(t, a.At.Equal(first.At) && a.Actor == "admin" && a.Action == first.Action && a.Target == first.Target &&
			a.IP == first.IP && a.Detail["provider"] == "meta" && bytes.Equal(a.PrevHash, first.PrevHash) &&
			bytes.Equal(a.Hash, first.Hash), fmt.Sprintf("After[0] = %+v, want %+v (times to the nanosecond)", a, first))
		limited, err := r.After(ctx, base, 1)
		mustf(t, err, "After with a limit")
		check(t, len(limited) == 1, "After to honour its limit")
		newest, err := r.Before(ctx, 0, 2)
		mustf(t, err, "Before from the newest")
		check(t, len(newest) == 2 && newest[0].Seq == base+2 && newest[1].Seq == base+1, "Before(0) newest first")
		older, err := r.Before(ctx, base+2, 1)
		mustf(t, err, "Before")
		check(t, len(older) == 1 && older[0].Seq == base+1, "Before to start under its bound")
	})

	t.Run("Traces", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Traces()
		at := time.Date(2026, 10, 29, 5, 0, 0, 500, time.UTC)
		la, lb := key(t, "la"), key(t, "lb")
		_, err := r.Add(ctx, store.TraceCall{})
		mustFail(t, err, "Add with no launch id")
		c1 := store.TraceCall{LaunchID: la, At: at, Provider: "meta", Path: "v1/chat/completions", Model: "muse-spark-1.3",
			Status: 200, Stream: true, DurationMS: 812, InputTokens: 10, OutputTokens: 4,
			Redacted: map[string]int{"SECRET": 2, "EMAIL": 1}, Question: "fix REDACTED_SECRET_0123456789",
			Reply: "done", ToolCalls: []store.TraceToolCall{{Name: "read_file", Args: `{"path":"a.go"}`}}}
		id1, err := r.Add(ctx, c1)
		mustf(t, err, "Add")
		id2, err := r.Add(ctx, store.TraceCall{LaunchID: lb, At: at.Add(time.Second), Provider: "meta", Path: "v1/models", Status: 200})
		mustf(t, err, "Add to another launch")
		id3, err := r.Add(ctx, store.TraceCall{LaunchID: la, At: at.Add(2 * time.Second), Provider: "meta", Path: "v1/messages", Status: 400, InputTokens: 5})
		mustf(t, err, "Add a second call")
		check(t, id1 < id2 && id2 < id3, fmt.Sprintf("ids %d %d %d to increase", id1, id2, id3))

		calls, err := r.Calls(ctx, la)
		mustf(t, err, "Calls")
		check(t, len(calls) == 2 && calls[0].ID == id1 && calls[1].ID == id3, fmt.Sprintf("Calls = %d, want two oldest first", len(calls)))
		g := calls[0]
		check(t, g.LaunchID == la && g.At.Equal(at) && g.Provider == "meta" && g.Path == c1.Path && g.Model == c1.Model &&
			g.Status == 200 && g.Stream && g.DurationMS == 812 && g.InputTokens == 10 && g.OutputTokens == 4 &&
			g.Redacted["SECRET"] == 2 && g.Redacted["EMAIL"] == 1 && g.Question == c1.Question && g.Reply == "done" &&
			len(g.ToolCalls) == 1 && g.ToolCalls[0] == c1.ToolCalls[0], fmt.Sprintf("Calls[0] = %+v", g))
		check(t, calls[1].Redacted != nil && len(calls[1].ToolCalls) == 0, "an empty call to read back empty")
		none, err := r.Calls(ctx, key(t, "nobody"))
		mustf(t, err, "Calls for an unknown launch")
		check(t, len(none) == 0, "no calls for an unknown launch")

		sessions, err := r.Summaries(ctx, 100)
		mustf(t, err, "Summaries")
		var sa, sb *store.TraceSession
		order := []string{}
		for i := range sessions {
			switch sessions[i].LaunchID {
			case la:
				sa = &sessions[i]
				order = append(order, la)
			case lb:
				sb = &sessions[i]
				order = append(order, lb)
			}
		}
		check(t, sa != nil && sb != nil && slices.Equal(order, []string{la, lb}), fmt.Sprintf("Summaries order = %v, want latest call first", order))
		check(t, sa.Calls == 2 && sa.InputTokens == 15 && sa.OutputTokens == 4 && sa.Redacted == 3 &&
			sa.First.Equal(at) && sa.Last.Equal(at.Add(2*time.Second)), fmt.Sprintf("session = %+v", *sa))
		one, err := r.Summaries(ctx, 1)
		mustf(t, err, "Summaries with a limit")
		check(t, len(one) == 1, "Summaries to honour its limit")

		n, err := r.DeleteBefore(ctx, at.Add(1500*time.Millisecond))
		mustf(t, err, "DeleteBefore")
		check(t, n >= 2, fmt.Sprintf("DeleteBefore removed %d, want at least 2", n))
		calls, err = r.Calls(ctx, la)
		mustf(t, err, "Calls after DeleteBefore")
		check(t, len(calls) == 1 && calls[0].ID == id3, "only the later call to survive")
	})

	t.Run("Approvals", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Approvals()
		at := time.Date(2026, 10, 29, 6, 0, 0, 250, time.UTC)
		mk := func(name string, created time.Time) store.Approval {
			return store.Approval{ID: key(t, name), LaunchID: key(t, "l"), Runner: "laptop", Agent: "musecode", Tool: "Bash",
				Command: "git push --force", Rule: "force-push", Irreversible: true, Status: store.ApprovalPending, CreatedAt: created}
		}
		a, b, c := mk("a", at), mk("b", at.Add(time.Second)), mk("c", at.Add(2*time.Second))
		mustFail(t, r.Create(ctx, store.Approval{}), "Create with an empty id")
		for _, x := range []store.Approval{a, b, c} {
			mustf(t, r.Create(ctx, x), "Create")
		}
		mustExists(t, r.Create(ctx, a), "Create duplicate")
		_, err := r.Get(ctx, key(t, "nope"))
		mustNotFound(t, err, "Get missing")
		got, err := r.Get(ctx, a.ID)
		mustf(t, err, "Get")
		check(t, got.LaunchID == a.LaunchID && got.Runner == "laptop" && got.Agent == "musecode" && got.Tool == "Bash" &&
			got.Command == a.Command && got.Rule == "force-push" && got.Irreversible && got.Status == store.ApprovalPending &&
			got.CreatedAt.Equal(at) && got.AnsweredAt.IsZero() && got.AnsweredBy == "", fmt.Sprintf("Get = %+v", got))

		mustf(t, r.Answer(ctx, b.ID, store.ApprovalAllowed, "admin", at.Add(time.Minute)), "Answer")
		mustConflict(t, r.Answer(ctx, b.ID, store.ApprovalDenied, "admin", at.Add(2*time.Minute)), "Answer twice")
		mustNotFound(t, r.Answer(ctx, key(t, "nope"), store.ApprovalDenied, "admin", at), "Answer missing")
		got, err = r.Get(ctx, b.ID)
		mustf(t, err, "Get answered")
		check(t, got.Status == store.ApprovalAllowed && got.AnsweredBy == "admin" && got.AnsweredAt.Equal(at.Add(time.Minute)),
			fmt.Sprintf("answered = %+v, want the first answer", got))

		mine := func(list []store.Approval) []string {
			var ids []string
			for _, x := range list {
				if x.ID == a.ID || x.ID == b.ID || x.ID == c.ID {
					ids = append(ids, x.ID)
				}
			}
			return ids
		}
		all, err := r.List(ctx, "", 1000)
		mustf(t, err, "List")
		check(t, slices.Equal(mine(all), []string{c.ID, b.ID, a.ID}), fmt.Sprintf("List = %v, want newest first", mine(all)))
		pending, err := r.List(ctx, store.ApprovalPending, 1000)
		mustf(t, err, "List pending")
		check(t, slices.Equal(mine(pending), []string{c.ID, a.ID}), fmt.Sprintf("List pending = %v", mine(pending)))
		one, err := r.List(ctx, "", 1)
		mustf(t, err, "List with a limit")
		check(t, len(one) == 1, "List to honour its limit")

		ids, err := r.ExpirePending(ctx, at.Add(1500*time.Millisecond), at.Add(time.Hour))
		mustf(t, err, "ExpirePending")
		check(t, slices.Contains(ids, a.ID) && !slices.Contains(ids, b.ID) && !slices.Contains(ids, c.ID), fmt.Sprintf("expired %v, want only the old pending one", ids))
		got, err = r.Get(ctx, a.ID)
		mustf(t, err, "Get expired")
		check(t, got.Status == store.ApprovalExpired && got.AnsweredAt.Equal(at.Add(time.Hour)), fmt.Sprintf("expired = %+v", got))
		mustConflict(t, r.Answer(ctx, a.ID, store.ApprovalAllowed, "admin", at), "Answer an expired approval")
	})

	t.Run("PushSubscriptions", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.PushSubscriptions()
		at := time.Date(2026, 10, 29, 7, 0, 0, 0, time.UTC)
		ep1, ep2 := "https://push.example.test/"+key(t, "1"), "https://push.example.test/"+key(t, "2")
		mustFail(t, r.Put(ctx, store.PushSubscription{P256dh: []byte{4}, Auth: []byte{1}}), "Put with no endpoint")
		mustFail(t, r.Put(ctx, store.PushSubscription{Endpoint: ep1}), "Put with no keys")
		mustf(t, r.Put(ctx, store.PushSubscription{Endpoint: ep1, P256dh: []byte{4, 1}, Auth: []byte{9}, Label: "phone", CreatedAt: at}), "Put")
		mustf(t, r.Put(ctx, store.PushSubscription{Endpoint: ep2, P256dh: []byte{4, 2}, Auth: []byte{8}, Label: "laptop", CreatedAt: at.Add(time.Second)}), "Put second")
		mustf(t, r.Put(ctx, store.PushSubscription{Endpoint: ep1, P256dh: []byte{4, 3}, Auth: []byte{7}, Label: "phone again", CreatedAt: at}), "Put replaces")
		mustf(t, r.Delivered(ctx, ep2, at.Add(time.Minute)), "Delivered")
		mustNotFound(t, r.Delivered(ctx, "https://push.example.test/nope", at), "Delivered missing")
		list, err := r.List(ctx)
		mustf(t, err, "List")
		var got []store.PushSubscription
		for _, x := range list {
			if x.Endpoint == ep1 || x.Endpoint == ep2 {
				got = append(got, x)
			}
		}
		check(t, len(got) == 2 && got[0].Endpoint == ep1 && got[1].Endpoint == ep2, "List oldest first, one row per endpoint")
		check(t, bytes.Equal(got[0].P256dh, []byte{4, 3}) && bytes.Equal(got[0].Auth, []byte{7}) && got[0].Label == "phone again" &&
			got[0].CreatedAt.Equal(at) && got[0].LastOKAt.IsZero(), fmt.Sprintf("replaced = %+v", got[0]))
		check(t, got[1].LastOKAt.Equal(at.Add(time.Minute)), fmt.Sprintf("delivered = %+v", got[1]))
		mustf(t, r.Delete(ctx, ep1), "Delete")
		mustf(t, r.Delete(ctx, ep1), "Delete missing (must be a no-op)")
		list, err = r.List(ctx)
		mustf(t, err, "List after Delete")
		for _, x := range list {
			check(t, x.Endpoint != ep1, "the deleted subscription to be gone")
		}
	})

	t.Run("Machines", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.Machines()
		at := time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)
		m := store.Machine{ID: key(t, "m1"), Name: "kitchen-mac", PubKey: id(t, "pub"), EnrolledAt: at}

		mustf(t, r.Create(ctx, m), "Create")
		mustExists(t, r.Create(ctx, m), "Create twice")
		got, err := r.Get(ctx, m.ID)
		mustf(t, err, "Get")
		check(t, got.Name == m.Name && bytes.Equal(got.PubKey, m.PubKey), fmt.Sprintf("got = %+v", got))
		_, err = r.Get(ctx, key(t, "nope"))
		mustNotFound(t, err, "Get missing")
		byName, err := r.GetByName(ctx, m.Name)
		mustf(t, err, "GetByName")
		check(t, byName.ID == m.ID, fmt.Sprintf("byName = %+v", byName))
		_, err = r.GetByName(ctx, "nope")
		mustNotFound(t, err, "GetByName missing")
		list, err := r.List(ctx)
		mustf(t, err, "List")
		check(t, slices.ContainsFunc(list, func(o store.Machine) bool { return o.ID == m.ID }), fmt.Sprintf("List = %+v", list))
	})

	t.Run("EnrolCodes", func(t *testing.T) {
		s := newStore(t)
		defer s.Close()
		r := s.EnrolCodes()
		now := time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)
		h := id(t, "hash-0123456789abcdef")
		c := store.EnrolCode{ID: key(t, "ec1"), CodeHash: h, Label: "kitchen-mac", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}

		mustf(t, r.Create(ctx, c), "Create")
		got, err := r.GetByHash(ctx, h)
		mustf(t, err, "GetByHash")
		check(t, got.Label == c.Label && got.ExpiresAt.Equal(c.ExpiresAt), fmt.Sprintf("got = %+v", got))
		_, err = r.GetByHash(ctx, id(t, "other"))
		mustNotFound(t, err, "GetByHash missing")
		used := now.Add(time.Minute)
		mustf(t, r.Use(ctx, c.ID, used), "Use")
		got, err = r.GetByHash(ctx, h)
		mustf(t, err, "GetByHash after Use")
		check(t, got.UsedAt.Equal(used), fmt.Sprintf("UsedAt = %v", got.UsedAt))
		// The hash is unique: a second code with it must conflict.
		mustExists(t, r.Create(ctx, c), "Create same hash twice")
		// Expired, unused codes prune; fresh ones stay. (A used code
		// keeps its row: the hash proves what was redeemed.)
		dead := store.EnrolCode{ID: key(t, "ec2"), CodeHash: id(t, "hash2-0123456789ab"), Label: "dead", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
		mustf(t, r.Create(ctx, dead), "Create dead")
		fresh := store.EnrolCode{ID: key(t, "ec3"), CodeHash: id(t, "hash3-0123456789ab"), Label: "x", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		mustf(t, r.Create(ctx, fresh), "Create fresh")
		// The cutoff sits between the two expiries.
		n, err := r.DeleteExpired(ctx, now.Add(30*time.Minute))
		mustf(t, err, "DeleteExpired")
		check(t, n >= 1, fmt.Sprintf("DeleteExpired removed %d, want at least 1", n))
		_, err = r.GetByHash(ctx, dead.CodeHash)
		mustNotFound(t, err, "expired unused code gone after DeleteExpired")
		_, err = r.GetByHash(ctx, fresh.CodeHash)
		mustf(t, err, "fresh code survives DeleteExpired")
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
					pk := store.Passkey{ID: []byte(k), Username: user, PublicKey: []byte("pk"), AAGUID: make([]byte, 16), Name: "n", CreatedAt: at, LastUsedAt: at}
					if race(s.Passkeys().Create(ctx, pk)) {
						return
					}
					if _, err := s.Passkeys().Get(ctx, pk.ID); race(err) {
						return
					}
					if race(s.Passkeys().UpdateAfterLogin(ctx, pk.ID, 2, false, at)) {
						return
					}
					if race(s.Passkeys().Rename(ctx, pk.ID, "m")) {
						return
					}
					if race(s.Passkeys().Delete(ctx, pk.ID)) {
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
		_, err = s.Users().EnsureWebAuthnHandle(canceled, key(t, "u"))
		mustCancel(t, err, "Users.EnsureWebAuthnHandle with canceled context")
		valid := store.Passkey{ID: id(t, "pk"), Username: key(t, "u"), PublicKey: []byte("pk"), Name: "n"}
		mustCancel(t, s.Passkeys().Create(canceled, valid), "Passkeys.Create with canceled context")
		_, err = s.Passkeys().Get(canceled, id(t, "pk"))
		mustCancel(t, err, "Passkeys.Get with canceled context")
		_, err = s.Passkeys().ListByUser(canceled, key(t, "u"))
		mustCancel(t, err, "Passkeys.ListByUser with canceled context")
		mustCancel(t, s.Passkeys().UpdateAfterLogin(canceled, id(t, "pk"), 1, false, time.Now()), "Passkeys.UpdateAfterLogin with canceled context")
		mustCancel(t, s.Passkeys().Rename(canceled, id(t, "pk"), "n"), "Passkeys.Rename with canceled context")
		mustCancel(t, s.Passkeys().Delete(canceled, id(t, "pk")), "Passkeys.Delete with canceled context")
	})
}
