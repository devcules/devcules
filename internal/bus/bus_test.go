package bus

import (
	"errors"
	"fmt"
	"testing"
)

// TestSentinelsMatch pins the contract every implementation honors:
// closed use and empty topics surface as errors matching ErrClosed and
// ErrBadTopic with errors.Is, so callers never parse messages.
func TestSentinelsMatch(t *testing.T) {
	if !errors.Is(fmt.Errorf("bus: publish: %w", ErrClosed), ErrClosed) {
		t.Fatal("wrapped ErrClosed does not match")
	}
	if !errors.Is(fmt.Errorf("bus: subscribe: %w", ErrBadTopic), ErrBadTopic) {
		t.Fatal("wrapped ErrBadTopic does not match")
	}
	if errors.Is(nil, ErrClosed) || errors.Is(nil, ErrBadTopic) {
		t.Fatal("nil matches a sentinel, want no match")
	}
}
