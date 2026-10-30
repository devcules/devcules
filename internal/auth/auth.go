package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/go-webauthn/webauthn/webauthn"
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

// maxFailureIPs bounds the per-IP failures map: past this many
// tracked addresses, recording a new one evicts the address whose
// oldest failure is earliest. Attackers cannot grow the map without
// bound, and eviction only forgets old failures, never grants access.
const maxFailureIPs = 10000

// maxArgonHashes caps concurrent argon2id verifications. One hash
// holds 64 MiB briefly, so unbounded sign-ins would let a login flood
// exhaust memory; extra sign-ins wait their turn, bounded by the
// request context.
const maxArgonHashes = 2

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

// Service is sign-in over the store's Users, Sessions and Passkeys
// repos: passwords always, passkeys when WithPasskeys enables them.
// The zero value is not usable; call New. now is a clock seam for
// expiry tests; production always uses time.Now.
type Service struct {
	store    store.Store
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
	argon    chan struct{}
	log      *slog.Logger

	// Passkey state, set once by WithPasskeys before serving. wa is
	// nil when passkeys are off; passkeysOff then says why.
	wa          *webauthn.WebAuthn
	passkeysOff string
	ceremonies  map[string]ceremony
	// loginMu serializes passkey logins so the sign-counter
	// check and its writeback are one atomic step.
	loginMu sync.Mutex
}

// Option configures a Service. WithPasskeys is the only one.
type Option func(*Service)

// New returns a Service over s.
func New(s store.Store, opts ...Option) *Service {
	a := &Service{
		store:      s,
		failures:   map[string][]time.Time{},
		now:        time.Now,
		argon:      make(chan struct{}, maxArgonHashes),
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		ceremonies: map[string]ceremony{},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// ResetPassword gives username a fresh random password, stores only
// its hash, and signs the user out everywhere. The password comes back
// to the caller (the CLI prints it once to the operator's terminal);
// nothing else keeps it. An unknown username wraps store.ErrNotFound.
func (a *Service) ResetPassword(ctx context.Context, username string) (password string, signedOut int, err error) {
	if _, err := a.store.Users().PasswordHash(ctx, username); err != nil {
		return "", 0, err
	}
	password, err = GeneratePassword()
	if err != nil {
		return "", 0, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", 0, err
	}
	if err := a.store.Users().SetPasswordHash(ctx, username, hash); err != nil {
		return "", 0, err
	}
	n, err := a.store.Sessions().DeleteForUser(ctx, username)
	if err != nil {
		return "", 0, fmt.Errorf("auth: password reset but sessions not revoked: %w", err)
	}
	return password, n, nil
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
	if a.overLimit(clientIP, now) {
		return "", ErrTooManyAttempts
	}
	fail := func() (string, error) {
		a.recordFailure(clientIP, now)
		return "", ErrInvalidCredentials
	}

	hash, err := a.store.Users().PasswordHash(ctx, username)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("auth: cannot look up user: %w", err)
		}
		if err := a.checkDummy(ctx); err != nil {
			return "", err
		}
		return fail()
	}
	ok, err := a.checkPassword(ctx, hash, password)
	if err != nil {
		return "", err
	}
	if !ok {
		return fail()
	}

	a.clearFailures(clientIP)
	return a.mintSession(ctx, username, now)
}

// overLimit prunes the IP's failures outside the window and reports
// whether the survivors reach the limit. Password and passkey
// sign-ins share this limiter and its map.
func (a *Service) overLimit(clientIP string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
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
	return len(kept) >= MaxSignInFailures
}

// recordFailure notes one more failure for the IP. A new address past
// the map bound evicts the address with the oldest failure: the map
// stays small, and eviction only forgets old failures.
func (a *Service) recordFailure(clientIP string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, known := a.failures[clientIP]; !known && len(a.failures) >= maxFailureIPs {
		// Every entry holds at least one failure: recordFailure only
		// appends, and overLimit deletes empties instead of storing
		// them.
		oldest, at := "", now
		first := true
		for ip, failures := range a.failures {
			if first || failures[0].Before(at) {
				oldest, at, first = ip, failures[0], false
			}
		}
		delete(a.failures, oldest)
	}
	a.failures[clientIP] = append(a.failures[clientIP], now)
}

// clearFailures forgets the IP's failures after a success, so a
// legitimate user who mistypes is never locked out.
func (a *Service) clearFailures(clientIP string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, clientIP)
}

// checkPassword verifies the password against its hash, waiting for
// an argon2id slot first. A cancelled wait fails with the context,
// not as invalid credentials: the caller stopped listening.
func (a *Service) checkPassword(ctx context.Context, hash, password string) (bool, error) {
	release, err := a.argonSlot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return CheckPassword(hash, password), nil
}

// checkDummy burns one argon2id verification for an unknown username,
// through the same slot as real verifications.
func (a *Service) checkDummy(ctx context.Context) error {
	release, err := a.argonSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	CheckDummyPassword()
	return nil
}

// argonSlot waits for one of the maxArgonHashes concurrent hash
// slots. The wait is bounded by ctx.
func (a *Service) argonSlot(ctx context.Context) (func(), error) {
	select {
	case a.argon <- struct{}{}:
		return func() { <-a.argon }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: waiting for a password check: %w", ctx.Err())
	}
}

// mintSession opens a session for username and returns the raw cookie
// value. Only the value's SHA-256 reaches the store.
func (a *Service) mintSession(ctx context.Context, username string, now time.Time) (string, error) {
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
