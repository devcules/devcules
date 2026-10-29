package router

import "net/http"

// Provider is a model vendor reached through the router. Adding one
// means adding one package under plugins/providers/.
type Provider interface {
	// ID names the provider in the URL (/llm/{id}/...) and in the
	// vault (provider/{id}/api_key).
	ID() string
	// BaseURL is the upstream origin the routed path is appended to,
	// without a trailing slash and without /v1 (e.g.
	// https://api.meta.ai).
	BaseURL() string
	// Auth sets the provider's credential on an upstream request.
	Auth(h http.Header, key string)
}

// KeySecret is the vault name holding a provider's API key.
func KeySecret(id string) string { return "provider/" + id + "/api_key" }
