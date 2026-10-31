// Package assistants is the conversational helper slot: the Assistant
// interface (PLAN.md §8), the conversation loop behind it, and the
// web plumbing (sessions, gating, user-message redaction) that the
// built-in assistant and future style adapters (hermes, openclaw)
// share.
//
// The loop mirrors the coding-agents gateway: the model proposes, the
// approvals service disposes. A user message is redacted on the way in
// (the model and the stored history only ever see placeholders), every
// tool call the model asks for goes through the same approvals policy
// an agent's call does, and the whole turn is buffered as events the
// API serves as JSON (it does not stream). Adapters never see a
// provider key: the model caller is handed an upstream URL whose
// credential live beside serve.
//
// It does not store conversations across restarts (the in-memory
// SessionStore is the default until a store-suite implementation
// lands), does not hold chat per launch, and does not speak MCP to the
// browser: the web tier serves a small JSON shape per turn.
package assistants
