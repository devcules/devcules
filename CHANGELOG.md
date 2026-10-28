# Changelog

All notable changes to Devcules, newest first.

## Unreleased

### Added (2026-10-28, M1.4)

- `internal/link/hub`: `Runners()` lists the live runner names
  (sorted) and `OnChange` reports link/unlink events without stalling
  reads (M1.4 relay watches membership through it).

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
