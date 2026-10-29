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
		tok := store.LaunchToken{ID: id, TokenHash: hash, Label: "musecode in t1", Provider: "meta", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}

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
		check(t, got.ID == id && got.Label == tok.Label && got.Provider == "meta" &&
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
