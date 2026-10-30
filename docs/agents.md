# Agents (M3.4)

A coding agent CLI runs in a terminal of its own on a runner, with
everything it needs to go through Devcules: its model calls through
the router, Devcules' MCP tools, and an approval hook before every
tool call. The owner starts one from the Terminals tab.

The adapters live in `plugins/agents`, one package per agent. The
runner role owns them; serve only relays the launch. `internal` never
imports them: `cmd/devcules` passes them to the runner's terminal
manager behind the small `pty.Launcher` interface.

| Agent | Package | Providers (own first) | Router path | Since |
|---|---|---|---|---|
| Muse Code | `plugins/agents/musecode` | `meta` | `/llm/meta/v1` | M3.3 |
| Claude Code | `plugins/agents/claudecode` | `anthropic`, `meta` | `/llm/<p>` (it adds `/v1/messages`) | M3.4 |
| Codex | `plugins/agents/codex` | `openai`, `meta` | `/llm/<p>/v1` (it adds `/responses`) | M3.4 |

## Choosing a provider

An agent can run on any provider it speaks (`Agent.Providers()`, its
own first): Meta serves both the Anthropic Messages API and the
OpenAI Responses API (`docs/integrations.md`), so Claude Code and
Codex can run on a Meta key. The new-terminal form lists the agent's
providers from `GET /api/providers` (`[{id, ready}]`: ready means a
key is in the vault) and fills the model list from
`GET /api/providers/{id}/models`, which serve reads from the
provider's `GET v1/models` with the vault key (never the browser) and
reuses for five minutes.

`POST /api/agents {agent, provider?, model?, project?, name?}` checks
that the agent speaks the provider (`400` otherwise), the router
serves it and it has a key (`409` "add its key in Settings"), and
that a model is given when the provider is not the agent's own
(another vendor has no model by the agent's default name). The launch
token is minted for the chosen provider, so the router refuses any
other (`launch.Launch.Allows`), `term.open` carries
`agent.provider`, and the adapter builds its router path from it; an
adapter refuses a provider it cannot speak. `agent.launch` audits the
provider.

## A launch

1. The owner taps **Start** on an agent in Terminals. The browser
   posts `{"agent": "musecode"}` to `POST /api/agents`.
2. Serve asks the runner which agents it offers (`term.agents`), and
   refuses with `404` when the runner has no such adapter, or `409`
   when the CLI is not installed there (the answer carries the
   documented install command), or the provider is not served or has
   no key (see Choosing a provider).
3. Serve mints a launch token tied to that runner and provider
   (audited `launch.mint`), and sends `term.open` with
   `{agent: {id, token, model}}`.
4. The runner's adapter builds the launch: the command, extra
   environment and files. The runner writes the files to a private
   launch dir (`0700`, files `0600`) next to its tmux socket, plus
   `env.sh` with the environment and `launch.token` for the hook, and
   starts the tmux session through a login shell. The token never
   appears on a command line, where other processes could read it.
5. The terminal script sources `env.sh`, runs the agent, then unsets
   the environment and removes the launch dir. When the agent exits,
   the terminal says so and becomes a plain shell. Closing the
   terminal removes the launch dir too.
6. Serve audits `agent.launch` (terminal id, agent, runner, launch
   id, model) and answers `201 {"id", "launch"}`; the browser opens
   the terminal. If the open fails, serve revokes the token again
   (audited `launch.revoke`, reason "agent did not start").

The token is the agent's model API key, its MCP bearer token and the
hook's credential at once, so revoking it
(`DELETE /api/launch-tokens/{id}`, or its 12 hour expiry) cuts all
three.

## Approvals

Each adapter installs `devcules hook --agent <id>` as the agent's own
pre-tool hook. The hook reads the agent's hook payload, turns it into
a Devcules call (tool, command), asks serve (`docs/approvals.md`), and
answers in the agent's hook protocol. Anything that goes wrong (no
token, a payload it cannot read, serve unreachable for 15 minutes, a
refused token) blocks the call: the adapters fail closed.

Agents' hooks often run with a cleared environment, so the hook
command carries `DEVCULES_SERVE_URL` and `DEVCULES_LAUNCH_TOKEN_FILE`
in front of it.

## Muse Code

Facts in `docs/integrations.md`. The adapter:

- detects `muse --version` (with `MUSE_NO_AUTO_UPDATE=1`, so a check
  never updates the CLI) through a login shell with a scrubbed
  environment;
- writes `settings.json` under its own `XDG_CONFIG_HOME` in the
  launch dir, so the owner's `~/.config/muse` is never read or
  changed: provider `meta`, the model (`muse-spark-1.3` unless the
  launch names another), the router as the endpoint with bearer auth,
  Devcules' MCP server as `devcules`, and the hook as a `PreToolUse`
  command hook for every tool (`matcher: "*"`, timeout 960 s, longer
  than an approval's 10 minutes);
- passes the token as `META_API_KEY`;
- runs `muse --model <model> --approval-judge off --disable-approval`:
  Muse's own prompts and its model judge are off because Devcules
  asks instead; Muse's sandbox stays on.

The hook answers Muse the way its docs say: exit `0` with no output
allows, exit `2` with the reason on stderr blocks. Any other exit
would let the call through (Muse fails open), so the adapter never
uses one.


## Claude Code

Facts in `docs/integrations.md`. The adapter:

- detects `claude --version` with nonessential traffic off;
- sets `CLAUDE_CONFIG_DIR` to a dir in the launch dir, so the owner's
  `~/.claude` login, history and settings stay untouched;
- uses the router as an LLM gateway: `ANTHROPIC_BASE_URL` is
  `/llm/anthropic` (Claude Code adds `/v1/messages`) and the token
  goes in `ANTHROPIC_AUTH_TOKEN` (sent as `Authorization: Bearer`).
  `ANTHROPIC_API_KEY` is never set: Claude Code would ask to approve
  it. A launch with a model sets `--model` and every alias to it;
  without one Claude Code keeps its own default;
- turns nonessential traffic off
  (`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`);
- runs `claude --settings <launch>/settings.json --mcp-config
  <launch>/mcp.json`. Command-line settings outrank the project's
  `.claude/settings.json`, and hooks from every file run, so a repo
  cannot drop the Devcules hook. Claude Code reads MCP servers only
  from `--mcp-config` (or `.mcp.json`), never from `settings.json`.

The hook groups follow Claude Code's shape (`{"matcher": "*", "hooks":
[...]}`). An allowed call exits `0` with `{"hookSpecificOutput":
{"hookEventName": "PreToolUse", "permissionDecision": "allow"}}`, so
Claude Code does not ask again; silence would fall through to its own
prompt. A deny exits `2`. Checked with a real run of Claude Code
2.1.312: an allowed Bash call runs, and with agents off it is blocked
with Devcules' reason.

## Codex

Facts in `docs/integrations.md`. The adapter:

- detects `codex --version`;
- sets `CODEX_HOME` to a dir in the launch dir;
- writes `config.toml` with a `devcules` model provider (`base_url` the
  router's `/llm/openai/v1`, `wire_api = "responses"`, the key from
  `DEVCULES_LLM_KEY`), the Devcules MCP server (bearer token from
  `DEVCULES_MCP_TOKEN`) and `[features] hooks = true`; the token
  itself is only in the environment;
- writes `hooks.json` with a `PreToolUse` command hook for every tool;
- runs `codex --sandbox workspace-write`, plus `--model` when the
  launch names one.

Checked with a real run of Codex 0.165.0: it calls
`<base_url>/responses` without a ChatGPT login and lists the Devcules
MCP tools. Codex runs a user hook only after the owner trusted that
exact hook, and it asks at start; until then Codex's own approvals
and sandbox are the guard, which is why the adapter leaves them on.

## Routes (serve, session required)

| Route | Does |
|---|---|
| `GET /api/agents?runner=` | `{runner, mode, agents: [{id, name, provider, installed, version, install}]}`: the runner's agents and its access mode. |
| `POST /api/agents?runner=` | `{"agent", "model"}` (model optional) starts one. CSRF-checked. `201 {id, launch}`. |

## Adding an agent

Write a package under `plugins/agents/<id>` implementing
`agents.Agent` (identity, `Detect`, `InstallHint`, `LaunchSpec`,
`HookCall`, `HookAnswer`), run the shared contract tests from
`plugins/agents/agenttest`, and add it to `agentList` in
`cmd/devcules/agents.go`. Check its facts against the vendor's docs
and record them in `docs/integrations.md`.
