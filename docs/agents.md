# Agents (M3.3)

A coding agent CLI runs in a terminal of its own on a runner, with
everything it needs to go through Devcules: its model calls through
the router, Devcules' MCP tools, and an approval hook before every
tool call. The owner starts one from the Terminals tab.

The adapters live in `plugins/agents`, one package per agent. The
runner role owns them; serve only relays the launch. `internal` never
imports them: `cmd/devcules` passes them to the runner's terminal
manager behind the small `pty.Launcher` interface.

| Agent | Package | Provider | Since |
|---|---|---|---|
| Muse Code | `plugins/agents/musecode` | `meta` | M3.3 |

## A launch

1. The owner taps **Start** on an agent in Terminals. The browser
   posts `{"agent": "musecode"}` to `POST /api/agents`.
2. Serve asks the runner which agents it offers (`term.agents`), and
   refuses with `404` when the runner has no such adapter, or `409`
   when the CLI is not installed there (the answer carries the
   documented install command) or the router lacks its provider.
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
