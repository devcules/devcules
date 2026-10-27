package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/bus"
	"github.com/devcules/devcules/internal/bus/bustest"
)

// TestContract runs the shared suite against the memory bus.
func TestContract(t *testing.T) {
	bustest.Run(t, func(t *testing.T) bus.Bus {
		t.Helper()
		return New()
	})
}

// waitWatchers polls the internal watcher count to zero: the watchers are
// the only goroutines this package creates, so zero proves none outlive
// their subscription without depending on global goroutine counts.
func waitWatchers(t *testing.T, b *memoryBus, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.watchers() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watchers stuck at %d after %s", b.watchers(), msg)
}

// TestWatchersDrain covers both exits precisely: per-subscription cancel
// and whole-bus Close.
func TestWatchersDrain(t *testing.T) {
	ctx := context.Background()

	b := New().(*memoryBus)
	stops := make([]context.CancelFunc, 5)
	for i := range stops {
		subCtx, stop := context.WithCancel(ctx)
		stops[i] = stop
		if _, err := b.Subscribe(subCtx, "t"); err != nil {
			t.Fatalf("Subscribe: %s", err)
		}
	}
	if got := b.watchers(); got != 5 {
		t.Fatalf("watchers = %d, want 5 live subscriptions", got)
	}
	for _, stop := range stops {
		stop()
	}
	waitWatchers(t, b, "unsubscribe")
	b.Close()

	b2 := New().(*memoryBus)
	for range 5 {
		if _, err := b2.Subscribe(ctx, "t"); err != nil {
			t.Fatalf("Subscribe: %s", err)
		}
	}
	b2.Close()
	waitWatchers(t, b2, "Close")
}

// TestUnsubscribeForgetsTopic guards against a slow leak: topics are cheap
// to invent (one per terminal session, say), so an empty topic must not
// stay in the table after its last subscriber leaves.
func TestUnsubscribeForgetsTopic(t *testing.T) {
	b := New().(*memoryBus)
	defer b.Close()
	for i := range 20 {
		ctx, stop := context.WithCancel(context.Background())
		if _, err := b.Subscribe(ctx, fmt.Sprintf("session-%d", i)); err != nil {
			t.Fatalf("Subscribe: %s", err)
		}
		stop()
	}
	waitWatchers(t, b, "unsubscribe")
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := len(b.subs); n != 0 {
		t.Fatalf("%d empty topics left in the table, want 0", n)
	}
}
