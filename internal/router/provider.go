package router

import (
	"context"
	"net/http"
	"slices"
)

// Provider is a model endpoint reached through the router: a saved
// provider profile (internal/providers), or a fixed one in tests.
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

// Catalog resolves provider ids when a call arrives, so a profile
// added or edited in Settings serves at once, without a restart.
type Catalog interface {
	// Lookup returns the provider with id, if there is one.
	Lookup(ctx context.Context, id string) (Provider, bool)
	// IDs lists every provider id, sorted.
	IDs(ctx context.Context) []string
}

// fixed is a Catalog of providers known at start.
type fixed map[string]Provider

// Fixed returns a Catalog of ps; a duplicate id keeps the last one.
func Fixed(ps ...Provider) Catalog {
	f := fixed{}
	for _, p := range ps {
		f[p.ID()] = p
	}
	return f
}

func (f fixed) Lookup(_ context.Context, id string) (Provider, bool) {
	p, ok := f[id]
	return p, ok
}

func (f fixed) IDs(context.Context) []string {
	out := make([]string, 0, len(f))
	for id := range f {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
