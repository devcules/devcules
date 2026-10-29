package meta

import (
	"net/http"
	"strings"
)

// DefaultBaseURL is the Meta Model API origin. The OpenAI-style base an
// SDK would use is DefaultBaseURL + "/v1".
const DefaultBaseURL = "https://api.meta.ai"

// Provider reaches the Meta Model API.
type Provider struct{ base string }

// New returns the provider for baseURL (DefaultBaseURL when empty), for
// example a regional endpoint or a test server.
func New(baseURL string) *Provider {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Provider{base: strings.TrimRight(baseURL, "/")}
}

// ID is "meta": calls go to /llm/meta/... .
func (p *Provider) ID() string { return "meta" }

// BaseURL is the upstream origin, without /v1.
func (p *Provider) BaseURL() string { return p.base }

// Auth sends the key the way both API styles accept it.
func (p *Provider) Auth(h http.Header, key string) {
	h.Set("Authorization", "Bearer "+key)
}
