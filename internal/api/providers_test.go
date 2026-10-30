package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
