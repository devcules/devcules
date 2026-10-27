package store

import (
	"context"
	"errors"
)

// ErrNotFound reports a missing key. Implementations wrap it with the key
// name so callers can match with errors.Is without parsing messages.
var ErrNotFound = errors.New("store: key not found")

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
}
