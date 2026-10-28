package auth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// openStore opens an isolated SQLite store. Auth owns no storage; the
// tests run against the real backend so expiry and hashing meet real
// rows.
func openStore(t *testing.T) store.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustBootstrap(t *testing.T, svc *Service) string {
	t.Helper()
	password, created, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureAdmin: %s", err)
	}
	if !created {
		t.Fatal("EnsureAdmin created = false on a fresh store, want true")
	}
	return password
}

func TestEnsureAdminBootstrap(t *testing.T) {
	svc := New(openStore(t))
	password := mustBootstrap(t, svc)

	if len(password) != GeneratedPasswordLength {
		t.Fatalf("password length = %d, want %d", len(password), GeneratedPasswordLength)
	}
	for _, c := range password {
		if !strings.ContainsRune(passwordAlphabet, c) {
			t.Fatalf("password %q contains %q, outside the unambiguous alphabet", password, c)
		}
	}
	// The printed password signs in: bootstrap is end to end.
	if _, err := svc.SignIn(context.Background(), AdminUsername, password, "10.0.0.1"); err != nil {
		t.Fatalf("SignIn with bootstrap password: %s", err)
	}
	// Later starts never print again.
	password, created, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("second EnsureAdmin: %s", err)
	}
	if created || password != "" {
		t.Fatalf("second EnsureAdmin = (%q, %v), want (\"\", false)", password, created)
	}
}

// raceStore simulates a lost bootstrap race: the admin check misses
// while Create hits the other starter's row.
type raceUsers struct {
	store.UsersRepo
}

func (r raceUsers) PasswordHash(context.Context, string) (string, error) {
	return "", fmt.Errorf("auth test blinds the admin check: %w", store.ErrNotFound)
}

type raceStore struct {
	store.Store
}

func (r raceStore) Users() store.UsersRepo {
	return raceUsers{UsersRepo: r.Store.Users()}
}

func TestEnsureAdminRaceLoser(t *testing.T) {
	s := openStore(t)
	svc := New(s)
	mustBootstrap(t, svc)
	// The admin exists, but the check is blind to it: Create collides
	// and the loser must report created false, not an error.
	loser := New(raceStore{Store: s})
	password, created, err := loser.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureAdmin race loser: %s", err)
	}
	if created || password != "" {
		t.Fatalf("race loser = (%q, %v), want (\"\", false)", password, created)
	}
}

func TestEnsureAdminStoreDown(t *testing.T) {
	s := openStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	_, _, err := New(s).EnsureAdmin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot check for admin user") {
		t.Fatalf("EnsureAdmin on a closed store = %v, want a lookup error", err)
	}
}

func TestSignInFailures(t *testing.T) {
	ctx := context.Background()
	newSvc := func(t *testing.T) (*Service, string) {
		t.Helper()
		svc := New(openStore(t))
		return svc, mustBootstrap(t, svc)
	}

	t.Run("unknown user matches wrong password", func(t *testing.T) {
		svc, password := newSvc(t)
		if _, err := svc.SignIn(ctx, AdminUsername, password+"x", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("wrong password = %v, want ErrInvalidCredentials", err)
		}
		if _, err := svc.SignIn(ctx, "nobody", "whatever", "10.0.0.2"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("unknown user = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		svc, _ := newSvc(t)
		ip := "10.0.0.3"
		for i := range MaxSignInFailures {
			if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("failure %d = %v, want ErrInvalidCredentials", i+1, err)
			}
		}
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("failure %d = %v, want ErrTooManyAttempts", MaxSignInFailures+1, err)
		}
		// Another client is unaffected.
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", "10.0.0.4"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("other IP = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("window slides", func(t *testing.T) {
		svc, _ := newSvc(t)
		cur := time.Now()
		svc.now = func() time.Time { return cur }
		ip := "10.0.0.5"
		for range MaxSignInFailures + 1 {
			_, _ = svc.SignIn(ctx, AdminUsername, "wrong", ip)
		}
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("before slide = %v, want ErrTooManyAttempts", err)
		}
		cur = cur.Add(SignInWindow).Add(time.Second)
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("after slide = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("success clears the count", func(t *testing.T) {
		svc, password := newSvc(t)
		ip := "10.0.0.6"
		for range MaxSignInFailures - 1 {
			_, _ = svc.SignIn(ctx, AdminUsername, "wrong", ip)
		}
		if _, err := svc.SignIn(ctx, AdminUsername, password, ip); err != nil {
			t.Fatalf("SignIn: %s", err)
		}
		for i := range MaxSignInFailures {
			if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("post-success failure %d = %v, want ErrInvalidCredentials", i+1, err)
			}
		}
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("post-success overflow = %v, want ErrTooManyAttempts", err)
		}
	})

	t.Run("sentinel messages", func(t *testing.T) {
		if ErrInvalidCredentials.Error() != "auth: invalid username or password" {
			t.Fatalf("invalid message = %q", ErrInvalidCredentials)
		}
		if ErrTooManyAttempts.Error() != "auth: too many sign-in attempts, try again later" {
			t.Fatalf("limited message = %q", ErrTooManyAttempts)
		}
		if ErrUnauthorized.Error() != "auth: unauthorized" {
			t.Fatalf("unauthorized message = %q", ErrUnauthorized)
		}
	})
}

func TestSignInStoreErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("lookup down", func(t *testing.T) {
		s := openStore(t)
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %s", err)
		}
		_, err := New(s).SignIn(ctx, AdminUsername, "x", "10.0.0.7")
		if err == nil || !strings.Contains(err.Error(), "cannot look up user") {
			t.Fatalf("SignIn on a closed store = %v, want a lookup error", err)
		}
	})

	t.Run("session create down", func(t *testing.T) {
		svc := New(openStore(t))
		password := mustBootstrap(t, svc)
		broken := New(failSessionStore{Store: svc.store})
		_, err := broken.SignIn(ctx, AdminUsername, password, "10.0.0.8")
		if err == nil || !strings.Contains(err.Error(), "cannot create session") {
			t.Fatalf("SignIn with failing sessions = %v, want a create error", err)
		}
	})
}

// failSessions breaks session writes so SignIn's error path is
// reachable without racing a real close.
type failSessions struct {
	store.SessionsRepo
}

func (f failSessions) Create(context.Context, store.Session) error {
	return errors.New("sessions unavailable")
}

type failSessionStore struct {
	store.Store
}

func (f failSessionStore) Sessions() store.SessionsRepo {
	return failSessions{SessionsRepo: f.Store.Sessions()}
}

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()
	svc := New(openStore(t))
	password := mustBootstrap(t, svc)
	cookieValue, err := svc.SignIn(ctx, AdminUsername, password, "10.0.0.9")
	if err != nil {
		t.Fatalf("SignIn: %s", err)
	}
	if len(cookieValue) != tokenBytes*2 {
		t.Fatalf("cookie value length = %d, want %d (32 hex-encoded bytes)", len(cookieValue), tokenBytes*2)
	}
	who, err := svc.Authenticate(ctx, cookieValue)
	if err != nil {
		t.Fatalf("Authenticate: %s", err)
	}
	if who != AdminUsername {
		t.Fatalf("Authenticate = %q, want %q", who, AdminUsername)
	}
	for _, bad := range []string{"", "not-a-session", cookieValue[:len(cookieValue)-2] + "ff"} {
		if _, err := svc.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Authenticate(%q) = %v, want ErrUnauthorized", bad, err)
		}
	}
	if err := svc.SignOut(ctx, cookieValue); err != nil {
		t.Fatalf("SignOut: %s", err)
	}
	if _, err := svc.Authenticate(ctx, cookieValue); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate after SignOut = %v, want ErrUnauthorized", err)
	}
	// Signing out twice is not an error.
	if err := svc.SignOut(ctx, cookieValue); err != nil {
		t.Fatalf("second SignOut: %s", err)
	}
	if err := svc.SignOut(ctx, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("SignOut(\"\") = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticateStoreDown(t *testing.T) {
	ctx := context.Background()
	svc := New(openStore(t))
	password := mustBootstrap(t, svc)
	cookieValue, err := svc.SignIn(ctx, AdminUsername, password, "10.0.0.10")
	if err != nil {
		t.Fatalf("SignIn: %s", err)
	}
	if err := svc.store.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	if _, err := svc.Authenticate(ctx, cookieValue); err == nil || !strings.Contains(err.Error(), "cannot load session") {
		t.Fatalf("Authenticate on a closed store = %v, want a load error", err)
	}
	if err := svc.SignOut(ctx, cookieValue); err == nil || !strings.Contains(err.Error(), "cannot delete session") {
		t.Fatalf("SignOut on a closed store = %v, want a delete error", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	svc := New(s)
	password := mustBootstrap(t, svc)
	start := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	cur := start
	svc.now = func() time.Time { return cur }
	cookieValue, err := svc.SignIn(ctx, AdminUsername, password, "10.0.0.11")
	if err != nil {
		t.Fatalf("SignIn: %s", err)
	}

	t.Run("idle boundary", func(t *testing.T) {
		cur = start.Add(IdleTimeout)
		if _, err := svc.Authenticate(ctx, cookieValue); err != nil {
			t.Fatalf("Authenticate at exactly the idle deadline: %s", err)
		}
		// That use refreshed the idle marker, so expiry is now one
		// full idle window past it.
		cur = cur.Add(IdleTimeout).Add(time.Second)
		if _, err := svc.Authenticate(ctx, cookieValue); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Authenticate past idle = %v, want ErrUnauthorized", err)
		}
		// Expired sessions are reclaimed on sight.
		if _, err := s.Sessions().Get(ctx, hashSession(cookieValue)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expired session still stored: %v", err)
		}
	})

	t.Run("absolute timeout", func(t *testing.T) {
		cur = start
		second, err := svc.SignIn(ctx, AdminUsername, password, "10.0.0.12")
		if err != nil {
			t.Fatalf("SignIn: %s", err)
		}
		// Stay active in 11-hour steps: every use refreshes idle, but
		// the absolute clock never moves.
		for cur = cur.Add(11 * time.Hour); !cur.After(start.Add(AbsoluteTimeout)); cur = cur.Add(11 * time.Hour) {
			if _, err := svc.Authenticate(ctx, second); err != nil {
				t.Fatalf("Authenticate at %s: %s", cur, err)
			}
		}
		if _, err := svc.Authenticate(ctx, second); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Authenticate past absolute = %v, want ErrUnauthorized", err)
		}
	})
}
