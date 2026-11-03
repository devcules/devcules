// Package desk feeds the Desk tab: weather (Open-Meteo), news (Tavily) and
// meetings (an ICS feed). Everything here is read-only against outside
// services, cached, and never lets a network failure blank a widget: a
// failed refresh returns the last good value marked stale.
package desk

import (
	"context"
	"sync"
	"time"
)

// Now is swapped by tests.
var Now = time.Now

// Entry is what a widget gets: the value, when it was fetched, and whether
// the last refresh failed (then Value is the previous good one and Error
// says why).
type Entry[T any] struct {
	Value     T         `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
	Stale     bool      `json:"stale,omitempty"`
	Error     string    `json:"error,omitempty"`
	Cached    bool      `json:"cached,omitempty"`
}

type cacheItem[T any] struct {
	entry      Entry[T]
	hasValue   bool
	lastForced time.Time
}

// Cache memoises per key with a TTL. A forced refresh (the Sync button) is
// honoured at most once per minRefresh per key so a stuck finger cannot
// burn API credits.
type Cache[T any] struct {
	mu         sync.Mutex
	ttl        time.Duration
	minRefresh time.Duration
	items      map[string]*cacheItem[T]
}

func NewCache[T any](ttl, minRefresh time.Duration) *Cache[T] {
	return &Cache[T]{ttl: ttl, minRefresh: minRefresh, items: map[string]*cacheItem[T]{}}
}

// Get returns the cached entry when fresh, otherwise fetches. refresh=true
// bypasses the TTL (subject to minRefresh).
func (c *Cache[T]) Get(ctx context.Context, key string, refresh bool, fetch func(context.Context) (T, error)) Entry[T] {
	now := Now()
	c.mu.Lock()
	it, ok := c.items[key]
	if !ok {
		it = &cacheItem[T]{}
		c.items[key] = it
	}
	fresh := it.hasValue && now.Sub(it.entry.UpdatedAt) < c.ttl
	forcedTooSoon := refresh && !it.lastForced.IsZero() && now.Sub(it.lastForced) < c.minRefresh
	if it.hasValue && (fresh && !refresh || forcedTooSoon) {
		e := it.entry
		e.Cached = true
		c.mu.Unlock()
		return e
	}
	if refresh {
		it.lastForced = now
	}
	c.mu.Unlock()

	v, err := fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		it.entry = Entry[T]{Value: v, UpdatedAt: Now()}
		it.hasValue = true
		return it.entry
	}
	if it.hasValue {
		e := it.entry
		e.Stale = true
		e.Error = err.Error()
		e.Cached = true
		return e
	}
	return Entry[T]{Error: err.Error()}
}

// Forget drops one key (used when its inputs, e.g. the location, change).
func (c *Cache[T]) Forget(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// Clear drops every key: used when the inputs of a whole family of keys
// change (the meetings cache is keyed per date window).
func (c *Cache[T]) Clear() {
	c.mu.Lock()
	clear(c.items)
	c.mu.Unlock()
}
