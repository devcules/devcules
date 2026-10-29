// Package meta is the Meta Model API provider for the model router:
// https://api.meta.ai, OpenAI-style (v1/chat/completions,
// v1/responses, v1/models) and Anthropic-style (v1/messages), with
// the key as a bearer token. Facts and links: docs/integrations.md.
//
// It only names the endpoint and sets the credential. Token checks,
// redaction, restore and streaming belong to internal/router; the key
// comes from the vault (provider/meta/api_key).
package meta
