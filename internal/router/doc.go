// Package router is serve's model router: every model call an agent
// makes goes agent → router → provider, at /llm/{provider}/...
//
// The router checks the caller's per-launch token (internal/launch),
// redacts the request body with internal/redact under the live policy,
// adds the real provider key from the vault, and forwards the call. The
// reply comes back with this exchange's placeholders restored, streamed
// replies included: OpenAI-style Chat Completions and Responses events
// and Anthropic-style Messages events each restore in their delta
// fields without breaking a placeholder that spans two events.
//
// Routed paths: POST v1/chat/completions, v1/responses, v1/messages and
// v1/messages/count_tokens, and GET v1/models[/{id}]. Anything else is
// 404: the router is a narrow door, not an open proxy.
//
// Providers are plug-ins (plugins/providers/...) handed in by serve;
// the router never imports them. It never logs or returns a request or
// reply body, a token or a key: its log line per call carries the
// provider, path, status, duration, launch id and redaction counts.
package router
