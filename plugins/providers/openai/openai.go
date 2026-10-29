package openai

import (
	"net/http"
	"strings"
)

// DefaultBaseURL is the upstream origin, without /v1.
const DefaultBaseURL = "https://api.openai.com"

// Provider reaches https://api.openai.com.
type Provider struct{ base string }

// New returns the provider for baseURL (DefaultBaseURL when empty).
func New(baseURL string) *Provider {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Provider{base: strings.TrimRight(baseURL, "/")}
}

// ID matches the router prefix: /llm/openai/... .
func (p *Provider) ID() string { return "openai" }

// BaseURL is the upstream origin, without /v1.
func (p *Provider) BaseURL() string { return p.base }

// Auth sets the vendor credential header.
func (p *Provider) Auth(h http.Header, key string) { h.Set("Authorization", "Bearer "+key) }
