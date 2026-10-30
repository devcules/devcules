package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestModels lists a provider's models with the vault key, serve-side,
// and reuses the list instead of calling again.
func TestModels(t *testing.T) {
	f := newFixture(t, 0, nil)
	var calls atomic.Int32
	var auth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta-2"},{"id":"alpha-1"},{"id":"alpha-1"},{"id":""}]}`))
	}))
	t.Cleanup(up.Close)
	f.router.catalog.(fixed)["mock"] = testProvider{base: up.URL}
	ctx := context.Background()

	list, err := f.router.Models(ctx, "mock")
	if err != nil {
		t.Fatalf("Models: %s", err)
	}
	if len(list) != 2 || list[0] != "alpha-1" || list[1] != "zeta-2" {
		t.Fatalf("models = %q, want sorted, unique, no blanks", list)
	}
	if auth != "Bearer "+upstreamKey {
		t.Fatalf("upstream saw %q, want the vault key", auth)
	}
	if _, err := f.router.Models(ctx, "mock"); err != nil || calls.Load() != 1 {
		t.Fatalf("second call = %v after %d upstream calls, want the cached list", err, calls.Load())
	}
	if _, err := f.router.Models(ctx, "nope"); err != ErrUnknownProvider {
		t.Fatalf("unknown = %v", err)
	}
	if ok, err := f.router.Ready(ctx, "mock"); !ok || err != nil {
		t.Fatalf("Ready(mock) = %v %v", ok, err)
	}
	delete(f.keys, KeySecret("mock"))
	if ok, _ := f.router.Ready(ctx, "mock"); ok {
		t.Fatal("Ready without a key")
	}
	f.router.models.at = nil
	if _, err := f.router.Models(ctx, "mock"); err != ErrNoKey {
		t.Fatalf("no key = %v", err)
	}
}

func TestModelsUpstreamError(t *testing.T) {
	f := newFixture(t, 0, nil)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(up.Close)
	f.router.catalog.(fixed)["mock"] = testProvider{base: up.URL}
	if _, err := f.router.Models(context.Background(), "mock"); err == nil {
		t.Fatal("a 401 model list returned no error")
	}
}

// TestModelsPaged follows Anthropic's has_more / last_id paging.
func TestModelsPaged(t *testing.T) {
	f := newFixture(t, 0, nil)
	var pages []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-b"},{"id":"claude-a"}],"has_more":true,"last_id":"claude-a"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-c"}],"has_more":false,"last_id":"claude-c"}`))
	}))
	t.Cleanup(up.Close)
	f.router.catalog.(fixed)["mock"] = testProvider{base: up.URL}
	list, err := f.router.Models(context.Background(), "mock")
	if err != nil {
		t.Fatalf("Models: %s", err)
	}
	if len(list) != 3 || list[0] != "claude-a" || list[2] != "claude-c" {
		t.Fatalf("models = %q, want both pages", list)
	}
	if len(pages) != 2 || pages[0] != "" || pages[1] != "limit=1000&after_id=claude-a" {
		t.Fatalf("queries = %q", pages)
	}
}

// swapCatalog is a Catalog that changes after the router starts, as
// Settings changes the saved profiles.
type swapCatalog struct {
	mu sync.Mutex
	m  map[string]Provider
}

func (c *swapCatalog) Lookup(_ context.Context, id string) (Provider, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[id]
	return p, ok
}

func (c *swapCatalog) IDs(context.Context) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for id := range c.m {
		out = append(out, id)
	}
	return out
}

func TestCatalogResolvesPerCall(t *testing.T) {
	f := newFixture(t, 0, nil)
	cat := &swapCatalog{m: map[string]Provider{}}
	o := f.router.o
	o.Catalog = cat
	rt, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	f.router = rt
	if w := f.do(http.MethodGet, "/llm/mock/v1/models", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("before = %d, want 404", w.Code)
	}
	if rt.Serves(context.Background(), "mock") {
		t.Fatal("serves an unsaved provider")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"late-1"}]}`))
	}))
	t.Cleanup(up.Close)
	cat.mu.Lock()
	cat.m["mock"] = testProvider{base: up.URL}
	cat.mu.Unlock()
	if w := f.do(http.MethodGet, "/llm/mock/v1/models", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "late-1") {
		t.Fatalf("after = %d %s, want the new profile served without a restart", w.Code, w.Body)
	}
	if ids := rt.Providers(context.Background()); len(ids) != 1 || ids[0] != "mock" {
		t.Fatalf("Providers = %v", ids)
	}
}

func TestProbe(t *testing.T) {
	f := newFixture(t, 0, nil)
	var calls atomic.Int32
	var auth atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"b"},{"id":"a"}]}`))
	}))
	t.Cleanup(up.Close)
	ctx := context.Background()
	list, err := f.router.Probe(ctx, testProvider{base: up.URL}, "typed-key-123")
	if err != nil || len(list) != 2 || list[0] != "a" || auth.Load() != "Bearer typed-key-123" {
		t.Fatalf("Probe = %v %v with %v", list, err, auth.Load())
	}
	// An empty key is the saved one; nothing is cached.
	if _, err := f.router.Probe(ctx, testProvider{base: up.URL}, ""); err != nil || calls.Load() != 2 || auth.Load() != "Bearer "+upstreamKey {
		t.Fatalf("saved-key probe = %v after %d calls with %v", err, calls.Load(), auth.Load())
	}
	if _, err := f.router.Probe(ctx, otherProvider{base: up.URL}, ""); err != ErrNoKey {
		t.Fatalf("no saved key = %v", err)
	}
}

// otherProvider has no key in the fixture's vault.
type otherProvider struct{ base string }

func (p otherProvider) ID() string                     { return "other" }
func (p otherProvider) BaseURL() string                { return p.base }
func (p otherProvider) Auth(h http.Header, key string) { h.Set("Authorization", "Bearer "+key) }

func TestRedirectsAreNotFollowed(t *testing.T) {
	f := newFixture(t, 0, nil)
	var stolen atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { stolen.Add(1) }))
	t.Cleanup(elsewhere.Close)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(up.Close)
	f.router.catalog.(fixed)["mock"] = testProvider{base: up.URL}
	w := f.do(http.MethodGet, "/llm/mock/v1/models", "", nil)
	if w.Code != http.StatusFound || stolen.Load() != 0 {
		t.Fatalf("redirect = %d, %d calls elsewhere: the key must not follow a redirect", w.Code, stolen.Load())
	}
}
