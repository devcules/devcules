package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// AdminUsername is the single bootstrap login. There are no roles in
// M1.1: the admin is the only user and owns the machine.
const AdminUsername = "admin"

// CookieName is the session cookie. It is set with HttpOnly, Secure,
// SameSite=Strict and Path=/; the store keeps only the SHA-256 of the
// value, so a database read cannot replay a session.
const CookieName = "devcules_session"

// IdleTimeout bounds inactivity: a session unused this long dies.
// AbsoluteTimeout bounds the session itself: even an active session
// dies this old and must sign in again. Both are enforced when the
// session is used, not by a background reaper: there is none in M1.1,
// so expired rows wait for their next use (or a later cleanup) to be
// reclaimed.
const (
	IdleTimeout     = 12 * time.Hour
	AbsoluteTimeout = 7 * 24 * time.Hour
)

// MaxSignInFailures caps wrong passwords per SignInWindow per client
// IP before sign-in answers 429. A success clears the count, so a
// legitimate user who mistypes is never locked out; an attacker who
// never guesses right can never clear it. The count is enforced
// approximately under concurrent sign-ins (check then record), which
// is fine for a brute-force brake: the argon2id cost dominates.
const (
	MaxSignInFailures = 5
	SignInWindow      = time.Minute
)

// tokenBytes is the session entropy: 32 random bytes, hex-encoded
// for the cookie.
const tokenBytes = 32

var (
	// ErrInvalidCredentials is the only sign-in failure callers see:
	// unknown user and wrong password are indistinguishable.
	ErrInvalidCredentials = errors.New("auth: invalid username or password")
	// ErrTooManyAttempts reports the rate limit. It names the wait so
	// the caller need not guess.
	ErrTooManyAttempts = errors.New("auth: too many sign-in attempts, try again later")
	// ErrUnauthorized reports a missing, unknown, or expired session.
	ErrUnauthorized = errors.New("auth: unauthorized")
)

// Service is password auth over the store's Users and Sessions repos.
// The zero value is not usable; call New. Now is a clock seam for
// expiry tests; production always uses time.Now.
type Service struct {
	store    store.Store
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

// New returns a Service over s.
func New(s store.Store) *Service {
	return &Service{store: s, failures: map[string][]time.Time{}, now: time.Now}
}

// EnsureAdmin creates the admin user on first start and returns its
// one-time password with created true. Later starts return created
// false: the password is never printed or recoverable again. There is
// no password env var by design: a secret in the environment leaks to
// child processes and logs. Two starters racing on one database is a
// misconfiguration; the loser sees created false and prints nothing.
func (a *Service) EnsureAdmin(ctx context.Context) (password string, created bool, err error) {
	_, err = a.store.Users().PasswordHash(ctx, AdminUsername)
	if err == nil {
		return "", false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", false, fmt.Errorf("auth: cannot check for admin user: %w", err)
	}
	password, err = GeneratePassword()
	if err != nil {
		return "", false, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", false, err
	}
	if err := a.store.Users().Create(ctx, AdminUsername, hash); err != nil {
		if errors.Is(err, store.ErrExists) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("auth: cannot create admin user: %w", err)
	}
	return password, true, nil
}

// SignIn checks the password and opens a session, returning the raw
// cookie value. Failures all return ErrInvalidCredentials after one
// argon2id verification, whether the username exists or not; past the
// per-IP limit they return ErrTooManyAttempts. A success clears the
// IP's failure count. Session creation stores only the SHA-256 of the
// returned value. clientIP must be the connection's remote address:
// no proxy headers are trusted (there is no proxy in M1.1).
func (a *Service) SignIn(ctx context.Context, username, password, clientIP string) (string, error) {
	now := a.now()
	a.mu.Lock()
	var kept []time.Time
	for _, f := range a.failures[clientIP] {
		if now.Sub(f) < SignInWindow {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		delete(a.failures, clientIP)
	} else {
		a.failures[clientIP] = kept
	}
	over := len(kept) >= MaxSignInFailures
	a.mu.Unlock()
	if over {
		return "", ErrTooManyAttempts
	}
	fail := func() (string, error) {
		a.mu.Lock()
		a.failures[clientIP] = append(a.failures[clientIP], now)
		a.mu.Unlock()
		return "", ErrInvalidCredentials
	}

	hash, err := a.store.Users().PasswordHash(ctx, username)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("auth: cannot look up user: %w", err)
		}
		CheckDummyPassword()
		return fail()
	}
	if !CheckPassword(hash, password) {
		return fail()
	}

	a.mu.Lock()
	delete(a.failures, clientIP)
	a.mu.Unlock()

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: cannot mint session: %w", err)
	}
	cookieValue := hex.EncodeToString(raw)
	if err := a.store.Sessions().Create(ctx, store.Session{
		TokenHash: hashSession(cookieValue),
		Username:  username,
		CreatedAt: now,
		LastSeen:  now,
	}); err != nil {
		return "", fmt.Errorf("auth: cannot create session: %w", err)
	}
	return cookieValue, nil
}

// SignOut deletes the session for the cookie value. Unknown values
// are a no-op success: signing out twice is not an error.
func (a *Service) SignOut(ctx context.Context, cookieValue string) error {
	if cookieValue == "" {
		return ErrUnauthorized
	}
	if err := a.store.Sessions().Delete(ctx, hashSession(cookieValue)); err != nil {
		return fmt.Errorf("auth: cannot delete session: %w", err)
	}
	return nil
}

// Authenticate resolves the cookie value to its username, enforcing
// both timeouts. A use refreshes the idle marker, so an active session
// lives until the absolute timeout. Expired sessions are deleted on
// sight (best effort: the verdict is unauthorized either way) and
// report ErrUnauthorized, as do missing and unknown values.
func (a *Service) Authenticate(ctx context.Context, cookieValue string) (string, error) {
	if cookieValue == "" {
		return "", ErrUnauthorized
	}
	digest := hashSession(cookieValue)
	sess, err := a.store.Sessions().Get(ctx, digest)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrUnauthorized
		}
		return "", fmt.Errorf("auth: cannot load session: %w", err)
	}
	now := a.now()
	// At exactly the deadline the session is still valid; past it, it
	// is gone. One convention for both bounds.
	if now.After(sess.CreatedAt.Add(AbsoluteTimeout)) || now.After(sess.LastSeen.Add(IdleTimeout)) {
		_ = a.store.Sessions().Delete(ctx, digest)
		return "", ErrUnauthorized
	}
	if err := a.store.Sessions().Touch(ctx, digest, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrUnauthorized
		}
		return "", fmt.Errorf("auth: cannot refresh session: %w", err)
	}
	return sess.Username, nil
}

// hashSession is the SHA-256 of the cookie value, hex-encoded. The raw
// value never reaches the store.
func hashSession(cookieValue string) string {
	sum := sha256.Sum256([]byte(cookieValue))
	return hex.EncodeToString(sum[:])
}
