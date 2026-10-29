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
	// Passkeys returns the passkey repo. It is auth's repo: only auth
	// and the store backends may use it.
	Passkeys() PasskeysRepo
	// Secrets returns the sealed-secret repo. It is the vault's repo:
	// only the vault and the store backends may use it.
	Secrets() SecretsRepo
}

// SecretsRepo stores sealed secrets by name. Values arrive already
// encrypted by the vault; the store never sees plaintext and treats
// them as opaque bytes.
type SecretsRepo interface {
	// Put creates or overwrites the sealed value for name. An empty
	// name or an empty value is an error.
	Put(ctx context.Context, name string, sealed []byte, at time.Time) error
	// Get returns the sealed value, or an error wrapping ErrNotFound.
	Get(ctx context.Context, name string) ([]byte, error)
	// Names lists every stored name, sorted.
	Names(ctx context.Context) ([]string, error)
	// Delete removes name. A missing name is not an error.
	Delete(ctx context.Context, name string) error
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
	// EnsureWebAuthnHandle returns the user's opaque WebAuthn user
	// handle, minting 32 random bytes on first call. The handle is
	// never the username, and it never changes once minted. An
	// unknown username is an error wrapping ErrNotFound.
	EnsureWebAuthnHandle(ctx context.Context, username string) ([]byte, error)
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

// Passkey is one registered WebAuthn credential. ID is the credential
// id, PublicKey its COSE-encoded public key; neither is secret, but
// both are opaque to the store. SignCount is the authenticator's
// signature counter at last use, AAGUID identifies the authenticator
// model, and Transports names how it can be reached. BackupEligible
// says the key may leave the device (a synced passkey), BackupState
// says it has. Name is the user-facing label. Times are UTC.
type Passkey struct {
	ID             []byte
	Username       string
	PublicKey      []byte
	SignCount      uint32
	AAGUID         []byte
	Transports     []string
	BackupEligible bool
	BackupState    bool
	Name           string
	CreatedAt      time.Time
	LastUsedAt     time.Time
}

// PasskeysRepo stores WebAuthn credentials by credential id.
type PasskeysRepo interface {
	// Create stores a passkey. A duplicate credential id is an error
	// wrapping ErrExists and never overwrites the stored row. An
	// empty id, username, public key or name is an error.
	Create(ctx context.Context, p Passkey) error
	// ListByUser returns the user's passkeys, oldest first. An
	// unknown username returns an empty list, not an error.
	ListByUser(ctx context.Context, username string) ([]Passkey, error)
	// Get returns the passkey, or an error wrapping ErrNotFound.
	Get(ctx context.Context, id []byte) (Passkey, error)
	// UpdateAfterLogin records a successful sign-in: the new sign
	// count, the backup state the assertion reported, and when it
	// happened. Nothing else changes. A missing passkey is an error
	// wrapping ErrNotFound.
	UpdateAfterLogin(ctx context.Context, id []byte, signCount uint32, backedUp bool, lastUsed time.Time) error
	// Rename changes the user-facing label. An empty name is an
	// error; a missing passkey is an error wrapping ErrNotFound.
	Rename(ctx context.Context, id []byte, name string) error
	// Delete removes the passkey. A missing passkey is not an error.
	Delete(ctx context.Context, id []byte) error
}
