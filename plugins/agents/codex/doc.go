// Package codex is the Muse adapter (the `codex` CLI). Facts and links: docs/integrations.md.
//
// A launch keeps a private settings file inside the launch dir, never the
// owner's home, pointing the agent at Devcules' model router with the
// per-launch token and installing `devcules hook --agent codex` as an approval
// hook. The agent's own auto-approve is off so Devcules governs approvals.
package codex
