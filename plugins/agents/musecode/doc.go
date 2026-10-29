// Package musecode is the Muse Code adapter (Meta's terminal coding
// agent, the `muse` CLI). Facts and links: docs/integrations.md.
//
// A launch gets its own settings dir (XDG_CONFIG_HOME inside the
// launch dir), so the owner's ~/.config/muse is never read or
// changed. That settings.json points the Meta provider at the model
// router (endpoint_transport with bearer auth, the launch token as
// META_API_KEY), adds the Devcules MCP server, and installs a
// PreToolUse hook that runs `devcules hook --agent musecode`. Muse's
// own approval prompts and its approval judge are off: Devcules is the
// approver, and a hook that cannot reach serve blocks the call.
//
// It does not install Muse Code, read the owner's Muse login, or
// change the owner's settings.
package musecode
