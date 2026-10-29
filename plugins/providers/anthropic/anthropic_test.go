package anthropic

import (
	"net/http"
	"testing"

	"github.com/devcules/devcules/internal/router"
)

var _ router.Provider = (*Provider)(nil)

func TestProvider(t *testing.T) {
	p := New("")
	if p.ID() != "anthropic" || p.BaseURL() != "https://api.anthropic.com" {
		t.Fatalf("defaults: %s %s", p.ID(), p.BaseURL())
	}
	if New("http://127.0.0.1:9/").BaseURL() != "http://127.0.0.1:9" {
		t.Fatal("trailing slash kept")
	}
	h := http.Header{}
	p.Auth(h, "k")
	if h.Get("x-api-key") != "k" {
		t.Fatalf("auth header %v", h)
	}
	if got := h.Get("Authorization"); got != "" {
		t.Fatalf("wrong auth header set: %q", got)
	}
}
