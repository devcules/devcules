package router

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	f.router.providers["mock"] = testProvider{base: up.URL}
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
	f.router.providers["mock"] = testProvider{base: up.URL}
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
	f.router.providers["mock"] = testProvider{base: up.URL}
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
