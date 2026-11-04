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
	lastUsed   time.Time
}

// maxCacheItems bounds a cache whose keys come from request input (the
// meetings cache is keyed per date window): past it the least recently
// used key goes.
const maxCacheItems = 64

// Cache memoises per key with a TTL. A forced refresh (the Sync button) is
// honoured at most once per minRefresh per key so a stuck finger cannot
// burn API credits.
type Cache[T any] struct {
	mu         sync.Mutex
	ttl        time.Duration
	minRefresh time.Duration
	idle       time.Duration // 0 = keep keys until the size cap; else drop keys unused this long
	lastSweep  time.Time
	items      map[string]*cacheItem[T]
}

func NewCache[T any](ttl, minRefresh time.Duration) *Cache[T] {
	return &Cache[T]{ttl: ttl, minRefresh: minRefresh, items: map[string]*cacheItem[T]{}}
}

// evictIdle makes the cache drop keys nobody asked for within d (checked
// at most once per TTL), for key families that roll over with the clock.
// d should be well past the TTL: a key still in use keeps its last good
// value for the stale fallback.
func (c *Cache[T]) evictIdle(d time.Duration) *Cache[T] {
	c.idle = d
	return c
}

// evictLocked drops idle keys and, past maxCacheItems, the least recently
// used ones. keep is the key being served. Caller holds c.mu.
func (c *Cache[T]) evictLocked(now time.Time, keep string) {
	if c.idle > 0 && now.Sub(c.lastSweep) >= c.ttl {
		c.lastSweep = now
		for k, it := range c.items {
			if k != keep && now.Sub(it.lastUsed) > c.idle {
				delete(c.items, k)
			}
		}
	}
	for len(c.items) > maxCacheItems {
		oldest := ""
		for k, it := range c.items {
			if k != keep && (oldest == "" || it.lastUsed.Before(c.items[oldest].lastUsed)) {
				oldest = k
			}
		}
		if oldest == "" {
			return
		}
		delete(c.items, oldest)
	}
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
	it.lastUsed = now
	c.evictLocked(now, key)
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
