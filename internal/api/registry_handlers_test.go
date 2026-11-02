package api

// registry_handlers_test.go — route-shape coverage for the extensions
// registry surface. These run the real mux with the real cookie-session
// guard (fixture + signInCredentials), and use the kinds' unauthenticated
// and bad-input paths so no test shells out to claude, codex, npx or the
// network (fleet/install/search hit those only on the happy path).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// registryReq issues one request against the mux, with or without the
// session cookie.
func registryReq(t *testing.T, mux http.Handler, method, target, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if cookie != "" {
		r = withSession(r, cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestRegistryKindRouting(t *testing.T) {
	mux, password := fixture(t, "")
	cookie := sessionCookie(t, mux, password)

	cases := []struct {
		name   string
		target string
		want   int
	}{
		// Deliberate stub: agents are a known future kind, so they get a
		// 501 the UI can show, not a 404 that implies a broken route.
		{"agent kind 501", "/api/registry/agent/search", http.StatusNotImplemented},
		{"unknown kind 404", "/api/registry/widget/search", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := registryReq(t, mux, http.MethodGet, tc.target, cookie, "")
			if w.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d (body %q)", tc.target, w.Code, tc.want, w.Body.String())
			}
			var out struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Error.Code == "" {
				t.Fatalf("error body carries no machine code: %q", w.Body.String())
			}
		})
	}
}

func TestRegistryMethodRouting(t *testing.T) {
	mux, password := fixture(t, "")
	cookie := sessionCookie(t, mux, password)

	// A path the dispatcher knows (installed exists for GET) hit with the
	// wrong method falls through to 404, not 405 and not a half-match.
	w := registryReq(t, mux, http.MethodGet, "/api/registry/mcp/install", cookie, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/registry/mcp/install = %d, want 404", w.Code)
	}
}

func TestRegistryExplainIsStubbed(t *testing.T) {
	mux, password := fixture(t, "")
	cookie := sessionCookie(t, mux, password)

	// Explain is intentionally not wired to an inference backend yet: a
	// 409 with a pointer, so the UI shows "not available" rather than a
	// 404 that reads as a missing route.
	w := registryReq(t, mux, http.MethodPost, "/api/registry/explain", cookie, `{"question":"what does it do"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("POST /api/registry/explain = %d, want 409 (body %q)", w.Code, w.Body.String())
	}
}

func TestRegistryFleetRequiresSession(t *testing.T) {
	mux, _ := fixture(t, "")
	for _, target := range []string{"/api/mcp/fleet", "/api/skills/fleet", "/api/registry/mcp/search", "/api/registry/mcp/installed"} {
		w := registryReq(t, mux, http.MethodGet, target, "", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s with no session = %d, want 401", target, w.Code)
		}
	}
}

func TestRegistryInstallBadBody(t *testing.T) {
	mux, password := fixture(t, "")
	cookie := sessionCookie(t, mux, password)

	// Missing name: install refuses before touching the installer.
	w := registryReq(t, mux, http.MethodPost, "/api/registry/mcp/install", cookie, `{"entry":{"id":"mcp-official:x"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("install with no name = %d, want 400 (body %q)", w.Code, w.Body.String())
	}
}
