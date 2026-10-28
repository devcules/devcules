package linktest

import (
	"io"
	"net/http"
	"testing"
)

// TestPipeRoundTrip proves the harness itself: a plain HTTP request
// from the client reaches the served handler and comes back, all over
// a pipe with no socket. The hub and dial suites build on this for
// full WebSocket upgrades.
func TestPipeRoundTrip(t *testing.T) {
	t.Parallel()
	s := Serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	res, err := s.Client().Get("http://pipe/probe")
	if err != nil {
		t.Fatalf("GET /probe: %s", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("GET /probe = %q, want ok", body)
	}
	res, err = s.Client().Get("http://pipe/missing")
	if err != nil {
		t.Fatalf("GET /missing: %s", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /missing = %d, want 404", res.StatusCode)
	}

	// Restart swaps the stack: the same client reaches the new handler.
	s.Restart(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("v2"))
	}))
	res, err = s.Client().Get("http://pipe/probe")
	if err != nil {
		t.Fatalf("GET after restart: %s", err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "v2" {
		t.Fatalf("GET after restart = %q, want v2", body)
	}
}
