// Package providers keeps serve's model provider profiles: each one a
// name, an API format (OpenAI-compatible or Anthropic), a base URL and
// an optional default model, stored in the providers table, with its
// key in the vault (provider/<id>/api_key). The router asks this
// package for a provider on every call, so a profile added or edited
// in Settings serves at once.
//
// It owns the base URL rules: https, or plain http only for localhost
// and private LAN addresses (a local vLLM); never userinfo, a query,
// or a link-local address such as a cloud metadata endpoint. Facts and
// links: docs/providers.md.
package providers
