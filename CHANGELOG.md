# Changelog

All notable changes to Devcules, newest first.

## Unreleased

### Fixed (2026-10-29, machine task view)

- The live screen and Check done failed on a real desktop: link
  frames were capped at 1 MiB, a capture goes base64 in JSON, so
  anything over about 760 KB never left the machine and serve
  answered 504 after 5 s. The frame limit is 4 MiB now, and a capture
  over serve's 2 MiB cap is shrunk until it fits. The answer carries
  the scale (`X-Devcules-Screen-Scale`, or `scale` in a verify) so
  taps and markers map back to screen pixels.
- The relay gave every request 5 s, but grounding waits on a router
  call allowed 45 s, and OCR and tree walks can run longer than 5 s.
  Captures, trees, OCR, verify and grounding now get their own waits.
- A malformed input event or a failed inject spent one of the task's
  actions. Now only an inject that moved something costs one.
- The relay took an answer from any machine that sent the waiting
  id. Now only the machine that was asked can answer.
- A helper answer too large for the link was dropped silently. The
  helper now refuses with the reason, so serve does not wait out the
  timeout.

### Fixed (2026-10-29, machines: enrolment and link)

- `devcules machine` was still the M0 stub, so no computer could ever
  link and the Machines tab could only say "No machine is linked". It
  now enrols (`devcules machine enrol <code> [--name]`, a local
  ed25519 key) and links, serving the screen, input, tree, OCR and
  grounding helper.
- The hub accepted the machine role on the runner token and never ran
  the ed25519 challenge `docs/link.md` described. Machines now dial
  without the token and sign a fresh nonce on every connect; a link
  on the runner token may not claim the role.
- `POST /api/machines/redeem` sat behind a browser session, so a
  machine could not use it. It is replaced by `POST
  /api/machines/enrol` (code plus a signature proving the key, no
  Origin, rate limited); minting a code now has CSRF and audit.
- Redeem wrote the machine before claiming the code (two racing
  redeems could both enrol), accepted any public key, and leaked
  store errors to the caller. Codes are now the PLAN's 12 characters
  and 15 minutes.
- A runner and a machine on one computer share the hostname and would
  have replaced each other forever: machine links are keyed apart.
- The Machines list shows enrolled machines offline too, and a
  machine can be removed (its link is cut and its key revoked).
- tests: `terminalDialer` waited only for its own view of the link and
  raced the first request into "no runner linked".

### Fixed (2026-10-29, agent adapters)

- Claude Code: the hook was in the wrong shape and Claude Code never
  ran it, MCP servers sat in `settings.json` where Claude Code does not
  read them, the base URL doubled `/v1`, and `ANTHROPIC_API_KEY` made
  it ask to approve the key. The adapter now uses its own
  `CLAUDE_CONFIG_DIR`, `ANTHROPIC_BASE_URL` plus `ANTHROPIC_AUTH_TOKEN`,
  `--settings` for the hook and `--mcp-config` for MCP, and answers an
  allowed call with a JSON allow. Checked with a real Claude Code run.
- Codex: the adapter wrote a `settings.json` Codex never reads, under
  the name "Muse", with a hook event Codex does not have. It now writes
  `config.toml` (a `devcules` provider on the router's OpenAI side, the
  MCP server, the token only in the environment) and `hooks.json` in
  its own `CODEX_HOME`, keeps Codex's approvals and sandbox on, and was
  checked with a real Codex run.
- `plugins/agents` shares one PreToolUse reader (argv commands too),
  the blocking reply and version parsing. A launch without a model
  leaves Claude Code and Codex on their own defaults.

### Fixed (2026-10-29, M4.5 follow-up)

- tests: the hub-vs-dialer race had one more fixture path — runFakeMachine's own callers (PickMachine's two-machine test) waited only for the dialer's view. runFakeMachine now also waits for the hub to see the machine by name, so every fixture that links one waits the same way.

### Fixed (2026-10-29, M4.5 follow-up)

- helper: the machine helper shipped with no tests (0% coverage); the link tests now drive the real helper as a machine behind the real relay — every machframes round trip, the budget's full 40 spends and 41st refusal, the budget reset with its ceiling, grounding through a stub router, and the refusals (kindless input, grounding without a screen). 71.7%.
- smoke: the compose log greps (vault open, redaction self-test) were one-shot; on loaded runners a slow log flush raced the grep into a failure while the line sat in the log. Both poll bounded now, like the readyz gate always has.
- tests: see the relay fixture race fix in this entry's sibling commit — both the relay and API fixtures wait for the hub to see the fake machine's link before the first request.

- tests: the machine relay fixtures returned before the fake machine's dial registered with the hub; on slower runners the first request raced the link into "no machine linked". Both fixtures now wait for the hub's view, like the terminal tests always did.

### Added (2026-10-29, M4.5)

- `internal/machines/relay` + `internal/api/machview.go`: task view and take control (closes #23). Serve relays the machframes protocol to linked machines over the hub (pending-map request/reply correlation by frame id, 5 s round trip, mirroring the terminal relay) and exposes it session-guarded: `GET /api/machines` (names + mirrored budget), `GET /api/machines-screen` (PNG), `GET /api/machines-tree`, `POST /api/machines-input` (take control — the budget is spent at the machine, which refuses at zero), `POST /api/machines-ground` (SAM matches with click points), `POST /api/machines-verify` (the done-verifier: fresh capture + tree + note, so success is judged on the live screen) and `GET /api/machines-budget`. Zero machines is 503; several need `?machine=`. `cmd/devcules/serve.go` now wires the machine relay and passes `Enrol: machines.NewIssuer(s)` to the API options (the enrol routes were still behind a nil issuer there). The dial client learned a `Role` option so machine helpers link with `hello{role:"machine"}` and the hub lists them under `Machines()` apart from runners.
- web: a Machines tab — the machine list, a live screen that re-fetches every 2 s while visible, a Take-control toggle mapping clicks on the rendered frame to screen pixels (proportional image→screen conversion), a phrase box that grounds and draws the match markers on the overlay, and a Check-done button showing the verifier's note; the budget counter rides the header. Typed fetch helpers in `web/src/api.ts`; aria labels and min-h-11 targets per the existing screens. The tab is deliberately absent from the e2e visual spec.

### Added (2026-10-29, M4.4)

- `internal/machines/ground`: SAM 3.1 grounding (closes #22). A frame plus a noun phrase goes to `sam-3.1` over the model router (`/llm/meta/v1/responses`, per-launch token — never a provider key), and the special-token answer is parsed in pure Go: both mask encodings (`one_bit` and lossless) through a shared adaptive range coder, record splitting that respects the mask payload's own commas, and the largest mask region's centre as the click point. The decoder is pinned bit-identical to the official parser via three generated fixtures (canonical payload, per-pixel decision trace, per-shift coder-state trace); a canvas-drawn fixture screenshot with known click points ships in testdata. Zero matches is a valid outcome, not an error.

### Fixed (2026-10-29)

- api: the M4.1 enrol endpoints existed but were never wired into the mux; /api/machines/enrol-code and /api/machines/redeem are now registered (session-guarded, nil issuer 503), with tests for the mint → redeem → single-use flow.
- machines drivers: the per-OS `stub` types were unreferenced on the platforms that compile them (staticcheck U1000); a stub-contract test now pins each one (answers ErrUnavailable, Close is a no-op) on every platform.
- agents: dropped dead `versionRE` copies left behind in claudecode and codex when Detect switched to the shared helper.
- storetest: one nil Context in the mem enrol repo list.
- storetest: the shared contract suite never covered the M4.1 machines and enrol-code repos (sqlite shipped at 0% there and internal/machines sat at 67.4%; the 70% gate only failed once earlier unit errors stopped hiding it). Machines and EnrolCodes sections now pin create/get/list, single-use codes, and the expired prune; the mem fake got its missing map init that made the new sections panic.
- relay: a browser attach racing the runner's death — or a same-name replacement while the attach was in flight — closed with 1011 "runner did not attach" instead of the runner-offline 1012. The failure path no longer guesses the hub's retire state (a replacement keeps the name linked, so the old poll could never settle): every attach failure except "unknown terminal" answers 1012, which the UI's reattach loop either lands on the live replacement with or reports the runner offline.
- e2e: the terminals-tab baseline drifted between stack runs because agent rows carry live CLI-detection text; the spec now waits for every row and masks the rows' detected-state text instead of pinning runner-side strings.

- pty: tmux socket paths that overflow the OS Unix-socket limit (104 bytes on macOS — `t.TempDir()` paths under `/var/folders/...` pushed `$TMUX_TMPDIR/tmux-<uid>/devcules` past it, so every tmux call failed with "File name too long") are relocated to a short private dir under the system temp, removed on Shutdown. tmux-backed tests now pass on macOS too, not only CI's Linux.
- tests: the hook-usage assertion still expected only `musecode` after M3.4 registered claudecode and codex; it now pins the full list.

### Added (2026-10-29, M4.3)

- `internal/machines/elements`: the accessibility-tree driver (macOS AX via a compiled Swift walker, Linux AT-SPI, Windows UIA), all three printing one shared flat TSV that a single Go parser decodes into typed elements with pixel boxes — element centres are valid input targets. Password values never leave the walker; empty trees carry a note saying why (permission, missing stack, truncation); reads cap at 400 by default, 2000 max. Fixture tests per OS pin the walker output shapes, and per-OS driver tests drive install → invoke → parse with a scripted runner.
- `internal/machines/ocr`: the OCR driver (macOS Vision, Linux tesseract with a 60-confidence floor, Windows WinRT) answering words with pixel boxes in the frame's own space — word centres are valid input targets for text-only models. Engines detected at first use, `ErrUnavailable` stubs elsewhere, zero new Go dependencies. Parsers are pinned by tests to the real engine output shapes; the Vision reader was verified live end-to-end on macOS.

### Added (2026-10-29, M4.2)

- `internal/machines/screen` + `input`: platform driver interfaces (`Capture`, `Inject`) with Linux X11 stubs behind build tags, macOS/Windows stubs elsewhere, and Xvfb-guarded tests that validate the real path when a display exists.
- The machine helper now advertises screen/input capability during hello; diagrams gain the new input lanes.

### Added (2026-10-29, M4.1)

- `internal/machines`: enrolment service (`Issuer.Mint/Redeem`) and ed25519 `NewNonce/Verify`; migration `0008` with `enrol_codes` + `machines` tables; store repos `Machines/EnrolCodes` with sqlite + storetest coverage; M4.1 diagram lane.
- Link protocol now accepts `machine`. API serves `/api/machines` (issue + redeem flow); CLI supports `devcules machine enrol`.
- Docs carry the new machine facts — `docs/link.md` gains a Machines section documenting codes, nonces and challenges.

### Added (2026-10-29, M3.4)

- `plugins/providers/anthropic` and `plugins/providers/openai`: router providers for
  Claude Messages (`x-api-key`) and OpenAI Responses/Chat Completions (Bearer auth),
  each with `DEVCULES_*_BASE_URL` (`https://api.anthropic.com`, `https://api.openai.com`)
  and the usual trailing-slash-tolerant `New`, `ID`, `BaseURL`, `Auth`. No provider imported
  by the router — serve wires them in.
- `plugins/agents/claudecode` and `plugins/agents/codex`: composable adapters mirroring
  `musecode`'s modular contract (`Detect`/`LaunchSpec`/`HookCall`/`HookAnswer`), private
  settings dirs, per-launch tokens never on a command line, Devcules MCP + approval hooks,
  and shared `agenttest` passing. Registered in `cmd/devcules/agents.go`.
- Docs: `docs/agents.md` lists all three agents; new sections in `docs/integrations.md`
  for the APIs/CLIs; `docs/config.md` gets two new env rows.

### Added (2026-10-29, M3.3)

- `plugins/agents`: the agent adapter interface, a registry, and
  shared contract tests. `plugins/agents/musecode` starts Muse Code
  with its model calls on the router, Devcules' MCP server, and
  `devcules hook --agent musecode` as its `PreToolUse` hook, all in a
  private config dir so the owner's own Muse settings stay untouched.
- Runners start agents in tmux with the launch token in `0600` files,
  never on a command line, and remove them when the agent exits.
- `GET` and `POST /api/agents`; link frames `term.agents` and
  `term.agentlist`; `term.open` can carry an agent. A failed start
  revokes its token. Audited as `agent.launch`.
- `devcules hook --agent <id>` speaks the agent's own hook protocol
  and blocks the call on any failure.
- The PWA: Start an agent in Terminals, with the install command for
  a missing CLI and a note when the runner's access mode blocks or
  never asks. Agent terminals carry the agent's name. See
  `docs/agents.md`.

### Added (2026-10-29, M3.2)

- `internal/approvals`: access modes per runner (`off`, `critical`,
  `all`, `full`; unset blocks), critical command rules (11 built-ins
  the owner can turn off, plus up to 50 of their own), and pending
  approvals that expire as denied after 10 minutes. Commands are
  stored masked. Every ask, answer, expiry, block and settings change
  is audited.
- `internal/push`: Web Push with no third-party account: a VAPID key
  in the vault, RFC 8291 encryption, and devices removed when their
  push service says they are gone. Notices never carry the command.
- `devcules hook`: an agent adapter pipes a tool call in and gets
  allow (exit 0) or deny (exit 2), waiting for the owner when asked,
  and riding out serve restarts.
- Routes: `/api/approvals` (list, answer, settings, modes, rules,
  check), `/api/push` (key, devices, test) and `/hook/approvals` (launch
  token). Launch tokens can name their runner.
- The PWA: waiting approvals on Home with a count on the tab,
  answered ones in Activity, Notifications and Access modes in
  Settings, and a service worker for notices.
- `DEVCULES_PUSH_CONTACT` and `DEVCULES_LAUNCH_TOKEN`; migration
  `0007`. See `docs/approvals.md`.

### Added (2026-10-29, M3.1)

- `internal/mcp`: the Devcules MCP server at `POST /mcp`. It serves the
  stateless MCP `2026-07-28` revision (`server/discover`, `_meta` and
  header checks, `resultType`) and the handshake revisions `2025-11-25`,
  `2025-06-18` and `2025-03-26` on the same endpoint, with no sessions.
- Agents authenticate with their launch token. Tool calls are
  rate-limited per launch, audited as `mcp.call`, and logged without
  their arguments.
- Read-only built-in tools: `status`, `launch_info`, `my_trace` (the
  caller's own calls only), `list_terminals` and `check_redaction`.
- A tool registry later milestones add to, with name and schema checks.
- See `docs/mcp.md`; the MCP facts are in `docs/integrations.md`.

### Added (2026-10-29, M2.4)

- `internal/audit`: an append-only audit log of sign-ins (and failed
  ones), sign-outs, passkey and terminal changes, redaction policy
  changes, launch token mints and revokes, trace reads, exports and
  vault key rotations. Append-only three ways: the repo has no update
  or delete, SQLite triggers refuse both, and every entry is
  hash-chained to the one before. Serve checks the chain at start.
- `GET /api/audit`, `GET /api/audit/export` (JSON lines, or
  `?verify=1` for the chain check), and `devcules audit export` /
  `devcules audit verify [FILE]`.
- `internal/trace`: a trace per agent launch. The router records every
  call's model, question, reply, tool calls, token counts, status and
  redaction counts, from the provider's own bytes, so traces hold
  placeholders, never values. `GET /api/traces`,
  `GET /api/traces/{launchID}` (audited), and
  `DEVCULES_TRACE_DAYS` retention (default 30).
- The Activity tab: model calls per launch, and the audit log with a
  chain check and the export.
- Store: the `Audit` and `Traces` repos, migration `0006`.
- See `docs/audit.md`.

### Added (2026-10-29, M2.3)

- `internal/router`: the model router at `/llm/{provider}/...`. Agents
  call it with a launch token instead of a provider key; it redacts
  each request, adds the key from the vault, forwards Chat Completions,
  Responses, Messages, token counts and model lists, and restores
  placeholders in the reply, streamed or not. Block mode answers `400`
  in the caller's API style. One log line per call with counts only.
- `internal/launch`: per-launch tokens (`dvl_…`, SHA-256 at rest,
  12 hours by default, 7 days at most, optional provider limit,
  revocable), the `LaunchTokens` repo and migration `0005`, and
  `GET`/`POST /api/launch-tokens` and `DELETE /api/launch-tokens/{id}`.
- `plugins/providers/meta`: the Meta Model API provider, with
  `DEVCULES_META_BASE_URL` (default `https://api.meta.ai`) and a live
  test that runs when `META_API_KEY` is set.
- `devcules mockllm`, a hidden loopback-only fake provider, and a
  smoke step proving a key is masked upstream and restored in the
  reply. Compose passes `META_API_KEY` and `DEVCULES_META_BASE_URL`
  to serve.
- See `docs/router.md` and `docs/integrations.md`.

### Added (2026-10-29, M2.2)

- `internal/redact`: secrets and personal data out of model calls.
  SECRET uses the 221 Gitleaks v8.30.1 rules (converted rule data, MIT
  notice in `THIRD_PARTY_NOTICES.md`, our own matcher with keywords,
  entropy and allowlists); EMAIL, PHONE, CARD (Luhn) and IPV4 (public
  only) are opt-in. Modes off, log, mask and block per type.
- Deterministic placeholders `REDACTED_<TYPE>_<10 hex>` (HMAC under a
  per-install key minted into the vault), restored per exchange, and a
  streaming restorer that holds back only a possible partial
  placeholder.
- Startup self-test: serve will not listen if a detector, restore,
  streamed restore or block fails.
- `GET /api/redact`, `PUT /api/redact/policy` and
  `POST /api/redact/test`, plus a Redaction section in Settings with a
  mode per type, counters and a test box. The policy persists in meta
  (`redact.policy`).
- Tests: golden files per detector, every Gitleaks rule from a
  generated sample, and `FuzzStreamRestore`.
- See `docs/redact.md`.

### Fixed (2026-10-29, M2.2)

- The CI secret scan ran with no rules: `.gitleaks.toml` replaced the
  built-in config instead of extending it. It now sets
  `[extend] useDefault = true`, and the made-up vault key in the serve
  tests is allowlisted by value.

### Added (2026-10-29, M2.1)

- `internal/vault`: AES-256-GCM secrets at rest. A vault key (from
  `DEVCULES_VAULT_KEY` / `_FILE`, or a generated `0600` `vault.key` in
  the data dir) wraps a random data key kept in meta; each secret is
  sealed under the data key and bound to its name. Wrong keys stop
  serve with both key ids and next steps; logs show ids, never keys.
- `devcules vault rotate`: rewraps the data key in one write. The key
  file rotates itself (crash-safe via `vault.key.new`); an env key
  rotates to `DEVCULES_VAULT_NEW_KEY`.
- `META_API_KEY` moves into the vault (`provider/meta/api_key`) on
  start. Vault keys and `META_API_KEY` are now refused outside serve.
- Store: the vault's `Secrets()` repo with migration `0004` and
  storetest cases; `arch_test.go` keeps it with the vault.
- See `docs/vault.md`.

### Added (2026-10-28, M1.5)

- Passkeys (WebAuthn): registration while signed in, usernameless
  sign-in, and Settings management (rename, remove with confirm), on
  `github.com/go-webauthn/webauthn` v0.18.2 (BSD-3-Clause) server-side
  and `navigator.credentials` with our own base64url helpers in the
  browser (no library). Resident key and user verification required,
  attestation `none`, ES256/EdDSA; passkey sign-in opens a session
  exactly like a password sign-in.
- The RP id and origin come from `DEVCULES_PUBLIC_URL`: passkeys work
  on `http://localhost:7659` and https names (e.g. via `tailscale
  serve`), and stay off — with the reason in plain words — without a
  public URL or on an IP host. The admin password always keeps
  working and can never be removed.
- Challenges are 32 random bytes, single use, expiring after 5
  minutes, in an in-memory map (1,000 max) keyed by a ceremony id
  cookie; a `serve` restart cancels in-flight ceremonies. Sign
  counter enforced (both non-zero and not advancing rejects with a
  `possible cloned passkey` warning); synced keys reporting 0 are
  fine. See `docs/auth.md`.
- Hardening from the review list: at most 2 concurrent argon2id
  verifications (extra sign-ins wait, bounded by the request
  context), and the per-IP failures map is bounded at 10,000
  addresses (oldest evicted). Password and passkey sign-ins share
  the limiter.
- e2e passkeys spec (CDP virtual authenticator, own `localhost`
  base URL): password sign-in, add a key, sign out, passkey sign-in,
  remove it, passkey sign-in fails, password still works. The e2e
  job starts the stack with
  `DEVCULES_PUBLIC_URL=http://localhost:7659`, and the visual suite
  runs at that name.
- The passkey UI hides itself on a page opened at an IP address
  (WebAuthn refuses those), and an autofill passkey the server
  refuses now says so instead of failing silently.

### Added (2026-10-28, M1.4)

- `internal/link/hub`: `Runners()` lists the live runner names
  (sorted) and `OnChange` reports link/unlink events in map order on
  their own delivery goroutine, so watchers never stall reads or
  handshakes (M1.4 relay watches membership through it).
- tmux-backed terminals: `internal/terminals` (frame payloads),
  `internal/terminals/pty` (runner side, `github.com/creack/pty`
  v1.1.24 MIT, private `tmux -L devcules` server, 16 terminals max,
  env scrubbed to `TERM`/`HOME`/`PATH`/`LANG`/`SHELL`), and
  `internal/terminals/relay` (serve side, never imports pty —
  enforced by `arch_test.go`). Reply ceiling 5 s (504), slow
  browsers cut at 1013, offline runners at 1012, no replay buffer
  (tmux redraws on reattach).
- Browser API: `GET`/`POST /api/terminals` (201 `{id}`),
  `DELETE /api/terminals/{id}` (204, idempotent), and
  `GET /api/terminals/{id}/stream` (binary raw bytes, text resize
  controls, session cookie plus Origin check).
- Web Terminals tab with xterm.js (`@xterm/xterm` 6.0.0,
  `@xterm/addon-fit` 0.11.0, MIT): list/new/close-confirm, phone key
  strip (Esc, Tab, sticky Ctrl, arrows, Enter, 44 px,
  `visualViewport`), reconnect on `visibilitychange`/`online`;
  active phone tab is text-plus-indicator (not accent); any `/api`
  401 returns to sign-in. CSP gains only `style-src 'self'
  'unsafe-inline'` for xterm's runtime styles; `script-src` stays
  `default-src 'self'`.
- Runner wires the pty manager into its link (links without
  terminals when tmux is missing); compose runs the runner with
  `init: true`; CI unit job installs tmux. See `docs/terminals.md`.

### Added (2026-10-28, M1.3)

- Link protocol between serve and runner at `GET /link`
  (`github.com/coder/websocket` v1.8.15, ISC). Strict frames
  (`{"v":1,"type":"…","id":"…","data":{…}}`, 1 MiB limit) shared by
  `internal/link`, with a serve-side hub (`internal/link/hub`) and a
  runner-side dialer with reconnect (`internal/link/dial`).
- Handshake (`hello`/`welcome`), close codes (1008 variants, 4000
  replace-on-duplicate, 1001 drain), token auth before upgrade (401),
  `Origin` rejection (403), 10 s hello timeout, 15 s ping with drop
  after 2 missed pongs, and backoff with full jitter (0.5–30 s).
- Runner token lifecycle: `DEVCULES_RUNNER_TOKEN_FILE` (default
  `<DataDir>/runner.token`), minted `0400` by serve and read by the
  runner; compose shares it over a `link` volume at `/link`
  (read-write serve, read-only runner, uid 10001).
- `/readyz` answers `{"status":"ok","runners_linked":N}` and stays
  200 with zero runners (deliberate deviation from PLAN §6, so the UI
  stays up to report "runner offline" and the healthcheck cannot
  deadlock); see `docs/link.md` and `docs/deploy.md`.
- `FuzzDecode` seed corpus plus a 30 s fuzz step in the CI unit job.

### Added (2026-10-28, M1.2)

- `web/`: React 18 PWA shell (theme tokens dark-first, bottom tab
  bar on phones, sidebar on desktop, calm Home/Terminals empty
  states, Settings with sign-out), sign-in screen posting to
  `/auth/sign-in` (204 now, not 303), plain-word errors, `/auth/me`
  bootstrap with return-to-sign-in on 401. Installable manifest
  plus from-scratch icons; no service worker yet.
- `internal/web`: serves the embedded build with SPA fallback,
  immutable hashed assets, no-cache entry points, and a 503 with
  the build command when unbuilt.
- Security headers on every response (strict CSP, nosniff,
  no-referrer). Sign-in success is 204; the no-JS form and its
  route are gone.
- Node build stage in the Dockerfile (pinned digest), real web
  steps in CI (eslint, tsc, vitest, audit, licence check), e2e
  sign-out/keyboard flows plus Linux screenshot baselines.
- `docs/web.md`; `docs/ci.md` and `docs/auth.md` updated.

### Added (2026-10-27, M1.1)

- `internal/auth`: admin bootstrap (one-time 24-char password on
  stderr), argon2id passwords, cookie sessions (SHA-256 stored, 12 h
  idle, 7 d absolute), sign-in rate limiting (429 past 5/min per IP).
- `internal/api`: `POST /auth/sign-in`, `POST /auth/sign-out`,
  `GET /auth/me`, a no-JS form at `/sign-in`, stdlib CSRF on
  state-changing routes (trusts `DEVCULES_PUBLIC_URL`); `/healthz`
  and `/readyz` stay public, everything else needs a session (401).
- Store `Users()` and `Sessions()` repos with migration `0002` and
  contract tests, per `docs/store.md`.
- `e2e/`: Playwright sign-in (success, wrong password) at 390 px and
  desktop on Chromium, plus the `e2e` CI job; screenshot baselines
  wait for M1.2 (WebKit drops `Secure` cookies on plain-http
  loopback, so it is out until HTTPS). `scripts/smoke.sh` now signs
  in, checks `/auth/me`, signs out, and checks again.
- `docs/auth.md`; `docs/store.md` and `docs/ci.md` updated.

### Added (2026-10-27, M0.5)

- `serve` really runs: SQLite store, memory bus, `/healthz` and
  `/readyz` via `internal/api`, slog JSON logs, SIGTERM/SIGINT drain up
  to 20 s. `runner` idles for the M1.3 link and refuses vault keys.
- `Dockerfile` (multi-stage, pinned digests, non-root uid 10001),
  `deploy/compose.yaml` (hardened serve + runner), `scripts/smoke.sh`.
- `docs/deploy.md`; `docs/ci.md` and README quick start updated.

### Added (2026-10-27, M0.4)

- `internal/bus`: the `Bus` interface (publish, subscribe, close) with
  `ErrClosed` and `ErrBadTopic` sentinels.
- `internal/bus/memory`: in-process backend, stdlib only, with bounded
  non-blocking fan-out.
- `internal/bus/bustest`: the shared contract suite, proven on an
  independent in-memory fake and against a bus that delivers nothing.
- `docs/bus.md`: the delivery rules every backend honors.

### Added (2026-10-27, M0.3)

- `internal/store`: the `Store` interface (lifecycle, schema version,
  `Meta` key/value repo) with a typed not-found error.
- `internal/store/sqlite`: pure-Go SQLite backend with embedded
  numbered migrations, checksums, WAL, and 0700/0600 file layout.
  Pragmas apply to every pooled connection, concurrent first opens
  take turns, and duplicate migration versions are rejected.
- `internal/store/storetest`: the shared contract suite, proven on an
  in-memory fake and against a deliberately broken store.
- `docs/store.md`: shape, migrations, and how later modules add repos.

### Added (2026-10-27, M0.2)

- `internal/config`: env loading with `*_FILE` secret variants, all
  validation at start as one aggregated error, and table tests for
  every variable.
- `cmd/devcules` with `serve`, `runner`, `machine`, `hook`, `version`,
  `health` and `help` subcommands (stubs except `version` and `help`), wired to
  `-ldflags` build identity in `.goreleaser.yaml`.
- The vault key is optional everywhere and refused outside `serve`, so
  the untrusted side can never hold it; URL values in errors never
  show a password.
- `docs/config.md`: the variable table and subcommand reference.

### Added (2026-10-26, M0.1)

- CI pipeline (`.github/workflows/ci.yml`): lint, arch, unit,
  integration and security jobs, following `PLAN.md` §8. Jobs with
  nothing to check yet pass and log why they skipped.
- CD pipeline (`.github/workflows/cd.yml`): multi-arch image build and
  push on merge to `main`, with SBOM and keyless signing once a
  `Dockerfile` exists.
- Release pipeline (`.github/workflows/release.yml`): GoReleaser
  binaries for macOS, Linux and Windows on `v*` tags, with checksums
  and notes from this changelog.
- Go module `github.com/devcules/devcules`, a root `doc.go`, and an
  architecture test enforcing the §8 import rules.
- Repo files: `NOTICE`, `THIRD_PARTY_NOTICES.md`, `README.md`,
  `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`,
  `.github/dependabot.yml`, a pull request template, `.gitignore` and
  `.editorconfig`.
- `docs/ci.md`: what each CI job checks and the branch protection to
  enable after merge.
