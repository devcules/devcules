package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound reports a missing key. Implementations wrap it with the key
// name so callers can match with errors.Is without parsing messages.
var ErrNotFound = errors.New("store: key not found")

// ErrExists reports a duplicate row. Implementations wrap it with what
// collided so callers can match with errors.Is without parsing messages.
var ErrExists = errors.New("store: already exists")

// MetaRepo is a tiny string key/value repo for installer-level state
// (schema markers, bootstrap flags). Values are opaque to the store.
type MetaRepo interface {
	// Get returns the value for key, or an error wrapping ErrNotFound.
	Get(ctx context.Context, key string) (string, error)
	// Set creates or overwrites key. An empty key is an error; an empty
	// value is stored as-is.
	Set(ctx context.Context, key, value string) error
	// Delete removes key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
}

// Store is the storage surface every role uses. Later modules extend it
// with one repo at a time; see docs/store.md for how.
type Store interface {
	// Ping verifies the store answers.
	Ping(ctx context.Context) error
	// Close disconnects. Data persists; reopening must be a no-op.
	Close() error
	// SchemaVersion returns the highest applied migration version.
	SchemaVersion(ctx context.Context) (int, error)
	// Meta returns the key/value repo.
	Meta() MetaRepo
	// Users returns the login repo. It is auth's repo: only auth and
	// the store backends may use it.
	Users() UsersRepo
	// Sessions returns the session repo. It is auth's repo: only auth
	// and the store backends may use it.
	Sessions() SessionsRepo
}

// UsersRepo stores one argon2id hash per username. Passwords themselves
// never reach the store; auth hashes before calling Create.
type UsersRepo interface {
	// Create stores the hash for username, or an error wrapping
	// ErrExists when the username is taken. An empty username or an
	// empty hash is an error.
	Create(ctx context.Context, username, passwordHash string) error
	// PasswordHash returns the stored hash, or an error wrapping
	// ErrNotFound when the username is unknown.
	PasswordHash(ctx context.Context, username string) (string, error)
}

// Session is one login. TokenHash is the SHA-256 of the bearer token;
// the token itself is never stored. Times are UTC.
type Session struct {
	TokenHash string
	Username  string
	CreatedAt time.Time
	LastSeen  time.Time
}

// SessionsRepo stores login sessions by token hash.
type SessionsRepo interface {
	// Create stores a session. A duplicate token hash is an error
	// wrapping ErrExists.
	Create(ctx context.Context, s Session) error
	// Get returns the session, or an error wrapping ErrNotFound.
	Get(ctx context.Context, tokenHash string) (Session, error)
	// Touch moves the idle marker to at. A missing session is an
	// error wrapping ErrNotFound.
	Touch(ctx context.Context, tokenHash string, at time.Time) error
	// Delete removes the session. A missing session is not an error.
	Delete(ctx context.Context, tokenHash string) error
}
