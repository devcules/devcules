// Package codex is the Codex adapter (OpenAI's `codex` CLI). Facts and
// links: docs/integrations.md.
//
// A launch gets its own CODEX_HOME inside the launch dir, so the
// owner's ~/.codex login, history and config are never read or
// changed. Its config.toml adds a "devcules" model provider on the
// router's OpenAI provider (Responses API, the launch token from an
// env var) and the Devcules MCP server; hooks.json installs a
// PreToolUse hook that runs `devcules hook --agent codex`. Codex runs a
// user hook only once the owner trusted it (it asks at start), so its
// own approvals and its workspace-write sandbox stay on as well.
//
// It does not install Codex or touch the owner's account.
package codex
