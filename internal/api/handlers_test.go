package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// openStore returns two stores: one working, one closed (down).
func openStore(t *testing.T) (up, down store.Store) {
	t.Helper()
	open := func() store.Store {
		s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatalf("Open: %s", err)
		}
		return s
	}
	up = open()
	down = open()
	if err := down.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	return up, down
}

func TestHandlers(t *testing.T) {
	up, down := openStore(t)

	cases := []struct {
		name       string
		method     string
		target     string
		handler    http.HandlerFunc
		wantStatus int
		wantBody   string
	}{
		{"healthz get", http.MethodGet, "/healthz", healthz, http.StatusOK, "ok"},
		{"healthz wrong method", http.MethodPost, "/healthz", healthz, http.StatusMethodNotAllowed, ""},
		{"readyz up", http.MethodGet, "/readyz", readyz(up), http.StatusOK, "ok"},
		{"readyz down", http.MethodGet, "/readyz", readyz(down), http.StatusServiceUnavailable, "not ready: store unreachable"},
		{"readyz wrong method", http.MethodPost, "/readyz", readyz(up), http.StatusMethodNotAllowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantBody != "" && w.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", w.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestHandlerRoutes proves the mux wires both endpoints.
func TestHandlerRoutes(t *testing.T) {
	up, _ := openStore(t)
	mux := Handler(up)
	for _, target := range []string{"/healthz", "/readyz"} {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", target, w.Code)
		}
	}
}
