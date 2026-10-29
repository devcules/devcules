package anthropic

import (
	"net/http"
	"strings"
)

// DefaultBaseURL is the upstream origin, without /v1.
const DefaultBaseURL = "https://api.anthropic.com"

// Provider reaches https://api.anthropic.com.
type Provider struct{ base string }

// New returns the provider for baseURL (DefaultBaseURL when empty).
func New(baseURL string) *Provider {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Provider{base: strings.TrimRight(baseURL, "/")}
}

// ID matches the router prefix: /llm/anthropic/... .
func (p *Provider) ID() string { return "anthropic" }

// BaseURL is the upstream origin, without /v1.
func (p *Provider) BaseURL() string { return p.base }

// Auth sets the vendor credential header.
func (p *Provider) Auth(h http.Header, key string) { h.Set("x-api-key", key) }
