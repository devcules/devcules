package launch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/devcules/devcules/internal/store"
)

const (
	// Prefix starts every token, so a leaked one is recognisable.
	Prefix = "dvl_"
	// DefaultTTL is a token's life when the caller names none.
	DefaultTTL = 12 * time.Hour
	// MaxTTL caps a token's life.
	MaxTTL = 7 * 24 * time.Hour
	// maxLabel caps the free-text label.
	maxLabel = 100
	// maxRunner matches the link's runner name limit.
	maxRunner = 64
	// touchEvery limits last-used writes to one per token per minute.
	touchEvery = time.Minute
)

var (
	// ErrInvalid reports an unknown or malformed token. Callers answer
	// 401 and never say which part was wrong.
	ErrInvalid = errors.New("launch: invalid token")
	// ErrExpired reports a token past its expiry.
	ErrExpired = errors.New("launch: token expired")
	// ErrRevoked reports a revoked token.
	ErrRevoked = errors.New("launch: token revoked")
	// ErrNotFound reports an unknown token id on revoke.
	ErrNotFound = errors.New("launch: no such token")
)

// Launch describes a token without its secret.
type Launch struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Provider string    `json:"provider"`
	Runner   string    `json:"runner"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	Revoked  time.Time `json:"revoked,omitzero"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Active   bool      `json:"active"`
}

// Allows reports whether the token may call provider.
func (l Launch) Allows(provider string) bool {
	return l.Provider == "" || l.Provider == provider
}

// Tokens mints, checks and revokes per-launch tokens.
type Tokens struct {
	repo store.LaunchTokensRepo
	now  func() time.Time

	mu      sync.Mutex
	touched map[string]time.Time
}

// New returns Tokens over the store's launch token repo.
func New(s store.Store) *Tokens {
	return &Tokens{repo: s.LaunchTokens(), now: time.Now, touched: map[string]time.Time{}}
}

// Hash returns the stored form of a token.
func Hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Mint creates a token for label, limited to provider when it is not
// empty, living ttl (DefaultTTL when zero). The token is returned once
// and never stored.
func (t *Tokens) Mint(ctx context.Context, label, provider string, ttl time.Duration) (string, Launch, error) {
	return t.MintOn(ctx, "", label, provider, ttl)
}

// MintOn is Mint for a launch on runner, whose approval access mode
// then applies to the launch's tool calls. An empty runner is Mint.
func (t *Tokens) MintOn(ctx context.Context, runner, label, provider string, ttl time.Duration) (string, Launch, error) {
	label = strings.TrimSpace(label)
	switch {
	case len(runner) > maxRunner || strings.ContainsFunc(runner, unicode.IsControl):
		return "", Launch{}, fmt.Errorf("launch: runner must be a runner's name, up to %d bytes", maxRunner)
	case label == "":
		return "", Launch{}, fmt.Errorf("launch: a label is required (say what the token is for)")
	case len(label) > maxLabel:
		return "", Launch{}, fmt.Errorf("launch: label is longer than %d bytes", maxLabel)
	case ttl < 0 || ttl > MaxTTL:
		return "", Launch{}, fmt.Errorf("launch: ttl must be between 1 minute and %s", MaxTTL)
	case ttl == 0:
		ttl = DefaultTTL
	case ttl < time.Minute:
		return "", Launch{}, fmt.Errorf("launch: ttl must be between 1 minute and %s", MaxTTL)
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	token := Prefix + base64.RawURLEncoding.EncodeToString(secret)
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	now := t.now().UTC().Truncate(time.Second)
	row := store.LaunchToken{
		ID:        hex.EncodeToString(idBytes),
		TokenHash: Hash(token),
		Label:     label,
		Provider:  provider,
		Runner:    runner,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := t.repo.Create(ctx, row); err != nil {
		return "", Launch{}, err
	}
	return token, t.describe(row), nil
}

// Check returns the launch a token belongs to, or ErrInvalid,
// ErrExpired or ErrRevoked. A good check records the use (at most once
// a minute per token).
func (t *Tokens) Check(ctx context.Context, token string) (Launch, error) {
	if !strings.HasPrefix(token, Prefix) || len(token) != len(Prefix)+43 {
		return Launch{}, ErrInvalid
	}
	row, err := t.repo.ByHash(ctx, Hash(token))
	if errors.Is(err, store.ErrNotFound) {
		return Launch{}, ErrInvalid
	}
	if err != nil {
		return Launch{}, err
	}
	l := t.describe(row)
	switch {
	case !row.RevokedAt.IsZero():
		return l, ErrRevoked
	case !t.now().Before(row.ExpiresAt):
		return l, ErrExpired
	}
	t.touch(ctx, row.ID)
	return l, nil
}

// touch records a use, throttled. A failed write is not the caller's
// problem: last-used is informational.
func (t *Tokens) touch(ctx context.Context, id string) {
	now := t.now()
	t.mu.Lock()
	last, seen := t.touched[id]
	if seen && now.Sub(last) < touchEvery {
		t.mu.Unlock()
		return
	}
	t.touched[id] = now
	t.mu.Unlock()
	_ = t.repo.Touch(ctx, id, now)
}

// List returns every token, newest first, without secrets.
func (t *Tokens) List(ctx context.Context) ([]Launch, error) {
	rows, err := t.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Launch, len(rows))
	for i, r := range rows {
		out[i] = t.describe(r)
	}
	return out, nil
}

// Revoke ends a token now. Revoking twice is fine.
func (t *Tokens) Revoke(ctx context.Context, id string) error {
	err := t.repo.Revoke(ctx, id, t.now())
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// Prune deletes tokens that expired more than a day ago, so the list
// stays readable; recently expired ones stay visible for a while.
func (t *Tokens) Prune(ctx context.Context) (int, error) {
	return t.repo.DeleteExpired(ctx, t.now().Add(-24*time.Hour))
}

func (t *Tokens) describe(r store.LaunchToken) Launch {
	return Launch{
		ID:       r.ID,
		Label:    r.Label,
		Provider: r.Provider,
		Runner:   r.Runner,
		Created:  r.CreatedAt,
		Expires:  r.ExpiresAt,
		Revoked:  r.RevokedAt,
		LastUsed: r.LastUsedAt,
		Active:   r.RevokedAt.IsZero() && t.now().Before(r.ExpiresAt),
	}
}
