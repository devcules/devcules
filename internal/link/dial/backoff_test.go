package dial

import (
	"testing"
	"time"
)

// TestBackofferCaps proves the ceiling doubles per consecutive failure
// from min to max and sticks there: the pacing curve is exact math,
// not a sampled guess.
func TestBackofferCaps(t *testing.T) {
	t.Parallel()
	b := backoffer{min: 500 * time.Millisecond, max: 30 * time.Second}
	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for i, w := range want {
		if got := b.cap(); got != w {
			t.Fatalf("cap() after %d failures = %s, want %s", i, got, w)
		}
		b.n++
	}
}

// TestBackofferDraws proves every sleep lands in [0, cap]: full
// jitter spreads restart herds without ever exceeding the ceiling.
func TestBackofferDraws(t *testing.T) {
	t.Parallel()
	b := backoffer{min: 100 * time.Millisecond, max: 800 * time.Millisecond}
	for range 1000 {
		cap := b.cap()
		got := b.next()
		if got < 0 || got >= cap {
			t.Fatalf("next() = %s outside [0, %s)", got, cap)
		}
	}
	b.reset()
	if got := b.cap(); got != 100*time.Millisecond {
		t.Fatalf("cap() after reset = %s, want min", got)
	}
}

// TestBackofferNonPositive is the degenerate config: no wait, no block.
func TestBackofferNonPositive(t *testing.T) {
	t.Parallel()
	b := backoffer{}
	if got := b.next(); got != 0 {
		t.Fatalf("next() = %s, want 0", got)
	}
}
