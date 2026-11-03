package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	h := handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	// The demo needs the canvas Pay button and the marked currency bug.
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(body, `<canvas id="pay"`) || !strings.Contains(body, `data-bug="currency"`) {
		t.Fatalf("GET / = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / = %d, want 405", w.Code)
	}
}

func TestListenAddr(t *testing.T) {
	if listenAddr("") != ":8080" || listenAddr("127.0.0.1:9000") != "127.0.0.1:9000" {
		t.Fatal("ADDR is not honoured, or the default is not :8080")
	}
}
