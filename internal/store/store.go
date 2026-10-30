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
	// LaunchTokens returns the per-launch token repo. It is launch's
	// repo: only internal/launch and the store backends may use it.
	LaunchTokens() LaunchTokensRepo
	// Audit returns the append-only audit repo. It is audit's repo:
	// only internal/audit and the store backends may use it.
	Audit() AuditRepo
	// Traces returns the model call trace repo. It is trace's repo:
	// only internal/trace and the store backends may use it.
	Traces() TracesRepo
	// Approvals returns the approval repo. It is approvals' repo: only
	// internal/approvals and the store backends may use it.
	Approvals() ApprovalsRepo
	// Machines returns the enrolled-machine repo (M4.1: machines' repo).
	Machines() MachinesRepo
	// EnrolCodes returns the one-time enrolment code repo (M4.1).
	EnrolCodes() EnrolCodesRepo
	// Projects returns the project repo: named directories on a runner
	// or machine that terminals and agents open in.
	Projects() ProjectsRepo
	// Providers returns the model provider profile repo. It is the
	// provider service's repo: only internal/providers and the store
	// backends may use it.
	Providers() ProvidersRepo

	// PushSubscriptions returns the Web Push subscription repo. It is
	// push's repo: only internal/push and the store backends may use
	// it.
	PushSubscriptions() PushSubscriptionsRepo
}

// ErrConflict reports a write that lost a race: an audit append whose
// sequence number is not the next one. Implementations wrap it.
var ErrConflict = errors.New("store: conflicting write")

// AuditEntry is one audit record. Seq counts from 1 with no gaps.
// Hash is SHA-256 over PrevHash and the entry's other fields (see
// internal/audit); the store keeps both and never computes them.
// Detail holds short, non-secret context. Times are UTC.
type AuditEntry struct {
	Seq      int64
	At       time.Time
	Actor    string
	Action   string
	Target   string
	IP       string
	Detail   map[string]string
	PrevHash []byte
	Hash     []byte
}

// AuditRepo is append-only: it has no update or delete, and the
// backends refuse both at the database level too.
type AuditRepo interface {
	// Append stores e. Its Seq must be exactly one past the last
	// entry's (1 when empty), or the error wraps ErrConflict.
	Append(ctx context.Context, e AuditEntry) error
	// Last returns the newest entry, or an error wrapping ErrNotFound
	// when the log is empty.
	Last(ctx context.Context) (AuditEntry, error)
	// After returns up to limit entries with Seq > after, oldest first.
	After(ctx context.Context, after int64, limit int) ([]AuditEntry, error)
	// Before returns up to limit entries with Seq < before, newest
	// first. before <= 0 means from the newest.
	Before(ctx context.Context, before int64, limit int) ([]AuditEntry, error)
}

// TraceCall is one model call through the router, as the provider saw
// it: Question, Reply and tool call arguments carry placeholders, never
// redacted values. Redacted counts findings per redaction type.
type TraceCall struct {
	ID           int64
	LaunchID     string
	At           time.Time
	Provider     string
	Path         string
	Model        string
	Status       int
	Stream       bool
	DurationMS   int64
	InputTokens  int64
	OutputTokens int64
	Redacted     map[string]int
	Question     string
	Reply        string
	ToolCalls    []TraceToolCall
}

// TraceToolCall is one tool call a model asked for.
type TraceToolCall struct {
	Name string
	Args string
}

// TraceSession sums one launch's calls.
type TraceSession struct {
	LaunchID     string
	Calls        int
	InputTokens  int64
	OutputTokens int64
	Redacted     int
	First, Last  time.Time
}

// TracesRepo stores model call traces by launch.
type TracesRepo interface {
	// Add stores c and returns its new ID. An empty LaunchID is an
	// error.
	Add(ctx context.Context, c TraceCall) (int64, error)
	// Summaries returns up to limit launches with calls, latest call
	// first.
	Summaries(ctx context.Context, limit int) ([]TraceSession, error)
	// Calls returns a launch's calls, oldest first (none is not an
	// error).
	Calls(ctx context.Context, launchID string) ([]TraceCall, error)
	// DeleteBefore removes calls made before the given time and
	// returns how many went.
	DeleteBefore(ctx context.Context, before time.Time) (int, error)
}

// LaunchToken is one per-launch credential: what an agent presents to
// the model router instead of a real provider key. TokenHash is the
// SHA-256 of the token; the token itself is never stored. Provider
// limits the token to one provider id ("" allows any). Runner is the
// runner the launch runs on ("" when it is not tied to one), which
// decides its approval access mode. RevokedAt and LastUsedAt are zero
// until set. Times are UTC.
type LaunchToken struct {
	ID         string
	TokenHash  []byte
	Label      string
	Provider   string
	Runner     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RevokedAt  time.Time
	LastUsedAt time.Time
}

// LaunchTokensRepo stores per-launch tokens by id and token hash.
type LaunchTokensRepo interface {
	// Create stores a token. A duplicate id or hash is an error
	// wrapping ErrExists. An empty id or hash is an error.
	Create(ctx context.Context, t LaunchToken) error
	// ByHash returns the token with that hash, or an error wrapping
	// ErrNotFound.
	ByHash(ctx context.Context, hash []byte) (LaunchToken, error)
	// List returns every stored token, newest first.
	List(ctx context.Context) ([]LaunchToken, error)
	// Revoke marks the token revoked at at. Revoking twice keeps the
	// first time. A missing id is an error wrapping ErrNotFound.
	Revoke(ctx context.Context, id string, at time.Time) error
	// Touch records a use at at. A missing id is an error wrapping
	// ErrNotFound.
	Touch(ctx context.Context, id string, at time.Time) error
	// DeleteExpired removes tokens that expired before before and
	// reports how many went.
	DeleteExpired(ctx context.Context, before time.Time) (int, error)
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
	// SetPasswordHash replaces the stored hash (a password reset). An
	// unknown username wraps ErrNotFound; an empty hash is an error.
	SetPasswordHash(ctx context.Context, username, passwordHash string) error
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
	// DeleteForUser removes every session of username and reports how
	// many went (a password reset signs the user out everywhere).
	DeleteForUser(ctx context.Context, username string) (int, error)
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

// Approval statuses. An approval starts pending and is answered once.
const (
	ApprovalPending = "pending"
	ApprovalAllowed = "allowed"
	ApprovalDenied  = "denied"
	ApprovalExpired = "expired"
)

// Approval is one decision an agent's tool call waits on. Command is
// the call as shown to the person answering, already redacted. Rule
// names the critical rule that matched ("" when none did, e.g. under
// "ask for everything"). AnsweredAt and AnsweredBy stay empty while
// pending. Times are UTC.
type Approval struct {
	ID           string
	LaunchID     string
	Runner       string
	Agent        string
	Tool         string
	Command      string
	Rule         string
	Irreversible bool
	Status       string
	CreatedAt    time.Time
	AnsweredAt   time.Time
	AnsweredBy   string
}

// ApprovalsRepo stores approvals by id.
type ApprovalsRepo interface {
	// Create stores a new approval. An empty id is an error; a
	// duplicate id wraps ErrExists.
	Create(ctx context.Context, a Approval) error
	// Get returns one approval, or an error wrapping ErrNotFound.
	Get(ctx context.Context, id string) (Approval, error)
	// Answer moves a pending approval to status (allowed, denied or
	// expired). A missing id wraps ErrNotFound; one already answered
	// wraps ErrConflict, so the first answer always wins.
	Answer(ctx context.Context, id, status, by string, at time.Time) error
	// List returns up to limit approvals, newest first; status ""
	// means any status.
	List(ctx context.Context, status string, limit int) ([]Approval, error)
	// ExpirePending expires every approval still pending that was
	// created before before, answered at at, and returns their ids.
	ExpirePending(ctx context.Context, before, at time.Time) ([]string, error)
}

// PushSubscription is one browser's Web Push subscription: where to
// send (Endpoint) and the keys to encrypt for (P256dh, the browser's
// public key, and Auth, its 16-byte secret). Label names the device
// for the settings list. LastOKAt is zero until a push is accepted.
type PushSubscription struct {
	Endpoint  string
	P256dh    []byte
	Auth      []byte
	Label     string
	CreatedAt time.Time
	LastOKAt  time.Time
}

// PushSubscriptionsRepo stores subscriptions by endpoint.
type PushSubscriptionsRepo interface {
	// Put creates or replaces the subscription for its endpoint. An
	// empty endpoint or empty keys are an error.
	Put(ctx context.Context, s PushSubscription) error
	// List returns every subscription, oldest first.
	List(ctx context.Context) ([]PushSubscription, error)
	// Delete removes one. A missing endpoint is not an error.
	Delete(ctx context.Context, endpoint string) error
	// Delivered records an accepted push at at. A missing endpoint
	// wraps ErrNotFound.
	Delivered(ctx context.Context, endpoint string, at time.Time) error
}

type Machine struct {
	ID         string
	Name       string
	PubKey     []byte
	EnrolledAt time.Time
	LastSeen   time.Time
}

type MachinesRepo interface {
	Create(ctx context.Context, m Machine) error
	Get(ctx context.Context, id string) (Machine, error)
	GetByName(ctx context.Context, name string) (Machine, error)
	List(ctx context.Context) ([]Machine, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
}

type EnrolCode struct {
	ID        string
	CodeHash  []byte
	Label     string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

type EnrolCodesRepo interface {
	Create(ctx context.Context, c EnrolCode) error
	GetByHash(ctx context.Context, hash []byte) (EnrolCode, error)
	Use(ctx context.Context, id string, at time.Time) error
	DeleteExpired(ctx context.Context, before time.Time) (int, error)
	List(ctx context.Context) ([]EnrolCode, error)
}

// Project is a named working directory on one runner or machine:
// terminals and agents opened from it start in Dir. Repo is the git
// URL it was cloned from, empty for an existing directory.
type Project struct {
	ID     string
	Name   string
	Kind   string // "runner" or "machine"
	Target string // the runner or machine name
	Dir    string
	Repo   string
	// Git marks a repository (cloned, or a picked folder holding .git).
	Git       bool
	CreatedAt time.Time
}

// ProjectsRepo stores projects. Create wraps ErrExists for a taken
// name; Get wraps ErrNotFound; Delete of a missing id is a no-op.
type ProjectsRepo interface {
	Create(ctx context.Context, p Project) error
	Get(ctx context.Context, id string) (Project, error)
	List(ctx context.Context) ([]Project, error)
	Delete(ctx context.Context, id string) error
}

// ProviderProfile is one model provider the router serves at
// /llm/<ID>/...: an endpoint, the API format it speaks, and an
// optional default model. Its key is a vault secret
// (provider/<ID>/api_key), never a column here.
type ProviderProfile struct {
	ID   string
	Name string
	// Format is "openai" (Bearer auth, v1/chat/completions and
	// v1/responses) or "anthropic" (x-api-key, v1/messages).
	Format string
	// AlsoAnthropic marks an OpenAI-format endpoint that serves
	// v1/messages too, as Meta's does.
	AlsoAnthropic bool
	// BaseURL is the address as the owner typed it, trimmed; a
	// trailing /v1 is allowed.
	BaseURL string
	// API is which OpenAI API agents use: "auto", "chat" or
	// "responses". Anthropic profiles keep "auto".
	API string
	// Model is the default model; "" means the agent picks.
	Model string
	// Default marks the one profile picked first. Create honours it;
	// Update leaves it alone and SetDefault moves it.
	Default   bool
	CreatedAt time.Time
}

// ProvidersRepo stores provider profiles. Create wraps ErrExists for a
// taken id; Get, Update and SetDefault wrap ErrNotFound; Delete of a
// missing id is a no-op. At most one profile is the default.
type ProvidersRepo interface {
	Create(ctx context.Context, p ProviderProfile) error
	Get(ctx context.Context, id string) (ProviderProfile, error)
	// List is sorted by name, then id.
	List(ctx context.Context) ([]ProviderProfile, error)
	Update(ctx context.Context, p ProviderProfile) error
	SetDefault(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}
