package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// upProvider is a provider whose upstream is a test server.
type upProvider struct{ id, base string }

func (p upProvider) ID() string                     { return p.id }
func (p upProvider) BaseURL() string                { return p.base }
func (p upProvider) Auth(h http.Header, key string) { h.Set("Authorization", "Bearer "+key) }

func TestProvidersRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var sawKey string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"muse-spark-1.3"},{"id":"muse-lite"}]}`))
	}))
	t.Cleanup(up.Close)
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	rt, err := router.New(router.Options{
		Providers: []router.Provider{upProvider{"meta", up.URL}, upProvider{"openai", up.URL}},
		Keys:      metaKey{}, Tokens: launch.New(s), Redactor: r, Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Router: rt})
	session := sessionCookie(t, mux, password)
	get := func(path, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	if w := get("/api/providers", session); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `{"id":"meta","ready":true},{"id":"openai","ready":false}`) {
		t.Fatalf("providers = %d %s", w.Code, w.Body)
	}
	w := get("/api/providers/meta/models", session)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"models":["muse-lite","muse-spark-1.3"]`) {
		t.Fatalf("models = %d %s", w.Code, w.Body)
	}
	if sawKey != "Bearer meta-test-key" {
		t.Fatalf("upstream saw %q, want the vault key", sawKey)
	}
	if strings.Contains(w.Body.String(), "meta-test-key") {
		t.Fatal("the key reached the browser")
	}
	if w := get("/api/providers/openai/models", session); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "add its key in Settings") {
		t.Fatalf("no key = %d %s", w.Code, w.Body)
	}
	if w := get("/api/providers/nope/models", session); w.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", w.Code)
	}
	if w := get("/api/providers/meta/other", session); w.Code != http.StatusNotFound {
		t.Fatalf("bad route = %d", w.Code)
	}
	if w := get("/api/providers", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", w.Code)
	}
}

// vaultKeys is a vault stand-in: Get for the router, Put and Delete for
// Settings.
type vaultKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *vaultKeys) Get(_ context.Context, name string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[name]
	if !ok {
		return nil, context.Canceled
	}
	return v, nil
}

func (k *vaultKeys) Put(_ context.Context, name string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[name] = v
	return nil
}

func (k *vaultKeys) Delete(_ context.Context, name string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, name)
	return nil
}

// fakeKey builds a made-up key from parts, so no file holds a whole
// key shape (the redact tests do the same).
func fakeKey(tag string) string { return "sk" + "-" + tag + strings.Repeat("x", 16) }

func TestProviderKeys(t *testing.T) {
	t.Parallel()
	first, second := fakeKey("first"), fakeKey("second")
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var models atomic.Int32
	var sawKey atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		models.Add(1)
		sawKey.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x"}]}`))
	}))
	t.Cleanup(up.Close)
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	keys := &vaultKeys{m: map[string][]byte{}}
	rt, err := router.New(router.Options{Providers: []router.Provider{upProvider{"openai", up.URL}}, Keys: keys, Tokens: launch.New(s), Redactor: r, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Router: rt, ProviderKeys: keys, AuditLog: audit.New(s, nil)})
	session := sessionCookie(t, mux, password)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	if w := do(http.MethodGet, "/api/providers", ""); !strings.Contains(w.Body.String(), `{"id":"openai","ready":false}`) {
		t.Fatalf("before = %s", w.Body)
	}
	for body, want := range map[string]int{
		`{"key":"short"}`:            http.StatusBadRequest,
		`{"key":"has space inside"}`: http.StatusBadRequest,
		`nope`:                       http.StatusBadRequest,
	} {
		if w := do(http.MethodPut, "/api/providers/openai/key", body); w.Code != want {
			t.Errorf("PUT %s = %d, want %d", body, w.Code, want)
		}
	}
	if w := do(http.MethodPut, "/api/providers/nope/key", `{"key":"`+fakeKey("")+`"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown provider = %d", w.Code)
	}
	if w := do(http.MethodPut, "/api/providers/openai/key", `{"key":" `+first+` "}`); w.Code != http.StatusNoContent {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	if w := do(http.MethodGet, "/api/providers", ""); !strings.Contains(w.Body.String(), `{"id":"openai","ready":true}`) || strings.Contains(w.Body.String(), first) {
		t.Fatalf("after set = %s", w.Body)
	}
	if w := do(http.MethodGet, "/api/providers/openai/models", ""); w.Code != http.StatusOK || sawKey.Load() != "Bearer "+first {
		t.Fatalf("models = %d with %v", w.Code, sawKey.Load())
	}
	// Replacing the key drops the cached list: the next read asks again
	// with the new key, no restart.
	if w := do(http.MethodPut, "/api/providers/openai/key", `{"key":"`+second+`"}`); w.Code != http.StatusNoContent {
		t.Fatalf("replace = %d", w.Code)
	}
	if w := do(http.MethodGet, "/api/providers/openai/models", ""); w.Code != http.StatusOK || models.Load() != 2 || sawKey.Load() != "Bearer "+second {
		t.Fatalf("models after replace: %d calls with %v", models.Load(), sawKey.Load())
	}
	if w := do(http.MethodDelete, "/api/providers/openai/key", ""); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", w.Code)
	}
	if w := do(http.MethodGet, "/api/providers/openai/models", ""); w.Code != http.StatusConflict {
		t.Fatalf("models after remove = %d, want 409", w.Code)
	}
	if w := do(http.MethodPost, "/api/providers/openai/key", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", w.Code)
	}
	// The audit names the provider, never the key.
	var actions []string
	for _, e := range auditActions(t, mux, session) {
		actions = append(actions, e.Action+":"+e.Target)
	}
	got := strings.Join(actions, " ")
	if !strings.Contains(got, "provider.key.set:openai") || !strings.Contains(got, "provider.key.remove:openai") {
		t.Fatalf("audit = %s", got)
	}
	if w := do(http.MethodGet, "/api/audit", ""); strings.Contains(w.Body.String(), first) || strings.Contains(w.Body.String(), second) {
		t.Fatal("a key reached the audit log")
	}
}
