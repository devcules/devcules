package store

import (
	"errors"
	"fmt"
	"testing"
)

// TestErrNotFoundWrapping pins the contract every implementation honors:
// a missing key surfaces as an error matching ErrNotFound with errors.Is,
// so callers never parse messages.
func TestErrNotFoundWrapping(t *testing.T) {
	err := fmt.Errorf("store: meta key %q not found: %w", "k", ErrNotFound)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrapped %v does not match ErrNotFound", err)
	}
	if errors.Is(nil, ErrNotFound) {
		t.Fatal("nil matches ErrNotFound, want no match")
	}
}
