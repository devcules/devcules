package api

import (
	"bytes"
	"context"
	"encoding/json"
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

type noKeys struct{}

func (noKeys) Get(context.Context, string) ([]byte, error) { return nil, context.Canceled }

type namedProvider string

func (p namedProvider) ID() string               { return string(p) }
func (p namedProvider) BaseURL() string          { return "http://127.0.0.1:1" }
func (p namedProvider) Auth(http.Header, string) {}

func launchFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	tokens := launch.New(s)
	rt, err := router.New(router.Options{Providers: []router.Provider{namedProvider("meta")}, Keys: noKeys{}, Tokens: tokens, Redactor: r, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	return Handler(Options{Store: s, Auth: svc, Web: testWeb(), Router: rt, Launches: tokens}), password
}

func TestLaunchTokensAPI(t *testing.T) {
	mux, password := launchFixture(t)
	session := sessionCookie(t, mux, password)

	w := doJSON(mux, http.MethodPost, "/api/launch-tokens", `{"label":"curl test","provider":"meta","ttlMinutes":60}`, session)
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("mint = %d %s", w.Code, w.Body)
	}
	var minted struct {
		Token  string
		Launch launch.Launch
	}
	_ = json.Unmarshal(w.Body.Bytes(), &minted)
	if !strings.HasPrefix(minted.Token, launch.Prefix) || minted.Launch.Provider != "meta" || !minted.Launch.Active {
		t.Fatalf("minted = %+v", minted)
	}

	w = doJSON(mux, http.MethodGet, "/api/launch-tokens", "", session)
	if w.Code != 200 || strings.Contains(w.Body.String(), minted.Token) || !strings.Contains(w.Body.String(), `"label":"curl test"`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}

	// The token reaches the router (no key stored, so 503 after the
	// token check), and a session cookie alone does not.
	r := doJSON(mux, http.MethodPost, "/llm/meta/v1/chat/completions", `{}`, session)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("router with a session cookie only = %d, want 401", r.Code)
	}
	req := doJSONBearer(mux, "/llm/meta/v1/chat/completions", `{}`, minted.Token)
	if req.Code != http.StatusServiceUnavailable || !strings.Contains(req.Body.String(), "no API key") {
		t.Fatalf("router with the token = %d %s", req.Code, req.Body)
	}

	if w := doJSON(mux, http.MethodDelete, "/api/launch-tokens/"+minted.Launch.ID, "", session); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", w.Code)
	}
	if req := doJSONBearer(mux, "/llm/meta/v1/chat/completions", `{}`, minted.Token); req.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d", req.Code)
	}
	if w := doJSON(mux, http.MethodDelete, "/api/launch-tokens/nope", "", session); w.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown = %d", w.Code)
	}

	for _, bad := range []string{`{"label":""}`, `{"label":"x","provider":"acme"}`, `{"label":"x","ttlMinutes":99999}`, `nope`} {
		if w := doJSON(mux, http.MethodPost, "/api/launch-tokens", bad, session); w.Code != http.StatusBadRequest {
			t.Fatalf("mint %s = %d %s, want 400", bad, w.Code, w.Body)
		}
	}
	if w := doJSON(mux, http.MethodGet, "/api/launch-tokens", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("list without session = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodPut, "/api/launch-tokens", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodGet, "/api/launch-tokens/x", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET one = %d", w.Code)
	}
}

func doJSONBearer(mux http.Handler, target, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestRouterNotConfigured(t *testing.T) {
	mux, password := fixture(t, "")
	session := sessionCookie(t, mux, password)
	if w := doJSON(mux, http.MethodPost, "/llm/meta/v1/chat/completions", "{}", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("router = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodGet, "/api/launch-tokens", "", session); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("launch tokens = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodDelete, "/api/launch-tokens/x", "", session); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("revoke = %d", w.Code)
	}
}
