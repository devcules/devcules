package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// built is a Vite-shaped output: hashed assets plus unhashed entry
// points.
func built() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<div id=\"root\"></div>")},
		"assets/app-abc123.js":       {Data: []byte("console.log(1)")},
		"manifest.webmanifest":       {Data: []byte("{}")},
		"icons/apple-touch-icon.png": {Data: []byte("png")},
	}
}

func htmlGet(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	return r
}

func TestStaticFiles(t *testing.T) {
	h := Handler(built())
	cases := []struct {
		name        string
		request     *http.Request
		wantStatus  int
		wantCache   string
		wantBody    string
		wantContent string
	}{
		{"root serves index", htmlGet("/"), http.StatusOK, "no-cache", `<div id="root"></div>`, "text/html; charset=utf-8"},
		{"spa fallback", htmlGet("/settings"), http.StatusOK, "no-cache", `<div id="root"></div>`, "text/html; charset=utf-8"},
		{"hashed asset immutable", httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil), http.StatusOK, "public, max-age=31536000, immutable", "console.log(1)", "text/javascript; charset=utf-8"},
		{"manifest revalidates", httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil), http.StatusOK, "no-cache", "{}", ""},
		{"icon revalidates", httptest.NewRequest(http.MethodGet, "/icons/apple-touch-icon.png", nil), http.StatusOK, "no-cache", "png", ""},
		{"data miss 404s", httptest.NewRequest(http.MethodGet, "/missing.json", nil), http.StatusNotFound, "", "", ""},
		{"curl root 404s", httptest.NewRequest(http.MethodGet, "/", nil), http.StatusNotFound, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, tc.request)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCache != "" && w.Header().Get("Cache-Control") != tc.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", w.Header().Get("Cache-Control"), tc.wantCache)
			}
			if tc.wantBody != "" && w.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", w.Body.String(), tc.wantBody)
			}
			if tc.wantContent != "" && w.Header().Get("Content-Type") != tc.wantContent {
				t.Fatalf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), tc.wantContent)
			}
		})
	}
}

func TestWrongMethod(t *testing.T) {
	h := Handler(built())
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q, want GET, HEAD", allow)
	}
}

func TestMissingBuild(t *testing.T) {
	// No index.html: the app was never built.
	h := Handler(fstest.MapFS{})
	for _, r := range []*http.Request{htmlGet("/"), httptest.NewRequest(http.MethodGet, "/assets/x.js", nil)} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", r.URL.Path, w.Code)
		}
		if body := w.Body.String(); body != "web UI not built: run npm --prefix web run build" {
			t.Fatalf("body = %q, want the build command", body)
		}
	}
}

// TestEmbeddedPlaceholder proves the committed placeholder keeps Go
// builds working without Node: dist exists but holds no app, so the
// handler answers 503.
func TestEmbeddedPlaceholder(t *testing.T) {
	w := httptest.NewRecorder()
	Embedded().ServeHTTP(w, htmlGet("/"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 until the app is built (body %q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "npm --prefix web run build") {
		t.Fatalf("body = %q, want the build command", w.Body.String())
	}
}
