# Changelog

All notable changes to Devcules, newest first.

## Unreleased

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
