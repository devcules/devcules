package desk

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCache_TTLRefreshThrottleAndStale(t *testing.T) {
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	now := base
	Now = func() time.Time { return now }
	t.Cleanup(func() { Now = time.Now })

	c := NewCache[int](10*time.Minute, 20*time.Second)
	calls := 0
	var fail error
	fetch := func(context.Context) (int, error) {
		calls++
		if fail != nil {
			return 0, fail
		}
		return calls, nil
	}
	ctx := context.Background()

	e := c.Get(ctx, "k", false, fetch)
	if e.Value != 1 || e.Cached || e.Stale || e.Error != "" {
		t.Fatalf("first fetch: %+v", e)
	}
	// fresh: served from cache
	now = base.Add(5 * time.Minute)
	if e = c.Get(ctx, "k", false, fetch); e.Value != 1 || !e.Cached || calls != 1 {
		t.Fatalf("fresh value should be cached: %+v calls=%d", e, calls)
	}
	// forced refresh fetches again
	if e = c.Get(ctx, "k", true, fetch); e.Value != 2 || calls != 2 {
		t.Fatalf("forced refresh should fetch: %+v calls=%d", e, calls)
	}
	// a second force within minRefresh is ignored
	now = now.Add(5 * time.Second)
	if e = c.Get(ctx, "k", true, fetch); e.Value != 2 || calls != 2 || !e.Cached {
		t.Fatalf("second force too soon must be served from cache: %+v calls=%d", e, calls)
	}
	// TTL expiry refetches; a failure keeps the last good value marked stale
	now = now.Add(11 * time.Minute)
	fail = errors.New("upstream down")
	if e = c.Get(ctx, "k", false, fetch); e.Value != 2 || !e.Stale || e.Error != "upstream down" || calls != 3 {
		t.Fatalf("failed refetch should return stale last value: %+v calls=%d", e, calls)
	}
	// a key with no value and a failing fetch reports only the error
	if e = c.Get(ctx, "other", false, fetch); e.Value != 0 || e.Error == "" || e.Stale {
		t.Fatalf("no value + failure: %+v", e)
	}
}
