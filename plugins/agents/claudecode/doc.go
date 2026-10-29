// Package claudecode is the Claude Code adapter (the `claude` CLI).
// Facts and links: docs/integrations.md.
//
// A launch gets its own config dir (CLAUDE_CONFIG_DIR inside the
// launch dir), so the owner's ~/.claude login, history and settings
// are never read or changed. Claude Code reaches the model router as
// an LLM gateway (ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN, on the
// router's Anthropic provider), loads the Devcules MCP
// server from --mcp-config, and gets a PreToolUse hook from
// --settings, which outranks project settings. The hook answers
// "allow" in JSON, so Claude Code skips its own prompt for calls
// Devcules allowed; a denied or undecided call exits 2.
//
// It does not install Claude Code or touch the owner's account.
package claudecode
