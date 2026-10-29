// Package agents is the coding agent slot: the Agent interface that
// each adapter (musecode, later claudecode and codex) implements, and
// the Registry the runner uses to list and launch them.
//
// An adapter knows one agent CLI: how to detect it, its documented
// install command (shown, never run: Devcules never bundles or
// installs agent CLIs), and how to launch it pointed at serve. A
// launch gets the model router as its endpoint with a per-launch
// token, the Devcules MCP server, and an approval hook that runs
// `devcules hook --agent <id>`. The adapter also translates that
// hook's input and answer between the agent's own hook protocol and
// Devcules approvals.
//
// It does not run anything itself: the runner's terminal manager
// writes the launch's files and starts it in tmux, and serve decides
// approvals. Adapters never see a provider key, only launch tokens.
package agents
