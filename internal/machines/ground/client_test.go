package ground

import (
	"context"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientGroundEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llm/meta/v1/responses" {
			t.Errorf("path = %q, want the router's responses endpoint", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer lt_test" {
			t.Errorf("auth = %q, want the launch token", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type = %q", ct)
		}
		// answer with the official payload line
		_, _ = w.Write([]byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"<0f>0<|box;x1=100;y1=280;x2=229;y2=359;w=640;h=400|><|mask;x=0;y=0;data=40,60,!!!!!0=iv%plq$vFXiFfp!!|>"}]}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "lt_test")
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	matches, err := c.Ground(context.Background(), img, "green button")
	if err != nil {
		t.Fatalf("Ground: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches: %d", len(matches))
	}
	m := matches[0]
	if m.X1 != 100 || m.ClickX < 23 || m.ClickX > 25 {
		t.Fatalf("match: %+v", m)
	}
}

func TestClientGroundsEmptyPhrase(t *testing.T) {
	c := New("http://localhost:1", "lt")
	if _, err := c.Ground(context.Background(), image.NewRGBA(image.Rect(0, 0, 4, 4)), "  "); err == nil {
		t.Fatal("empty phrase accepted")
	}
}

func TestClientZeroMatchesIsNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"output":[]}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "lt")
	matches, err := c.Ground(context.Background(), image.NewRGBA(image.Rect(0, 0, 4, 4)), "blue button")
	if err != nil {
		t.Fatalf("zero matches must not error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches: %d", len(matches))
	}
}

func TestClientSurfacesSAMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid prompt: object prompts cannot be combined with a phrase"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "lt")
	_, err := c.Ground(context.Background(), image.NewRGBA(image.Rect(0, 0, 4, 4)), "glasses")
	if err == nil || !strings.Contains(err.Error(), "invalid prompt") {
		t.Fatalf("err: %v", err)
	}
}
