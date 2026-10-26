# Devcules: build plan

> **Agents lift heavy. You stay in control.**
> An open-source, self-hosted control room for AI agents. Run Muse Code, Claude Code or Codex on your own server. Let them drive real computers and browsers, and approve every risky step from your phone. Security first: secrets are redacted at the source, and access goes over Tailscale.

This file is the brief for the coding agent building Devcules. Read all of it before you write code. Then work **one milestone at a time**, and stop at the end of each one for review.

- **Licence:** Apache-2.0.
- **Repo:** `github.com/devcules/devcules`. The Go module path is `github.com/devcules/devcules`.

---

## 1. Ground rules (read first)

1. **Write everything from scratch.** Do not copy code from any other project, not even with attribution. Ideas are fine, code is not. The one exception is the Gitleaks detection *rule data* (MIT): it may be converted into `internal/redact/rules/gitleaks.json`, as long as its MIT notice goes into `THIRD_PARTY_NOTICES.md`.
2. **Use normal dependencies only.** Use well-known permissive libraries (MIT, BSD, ISC or Apache-2.0) from the official registries. Never add a GPL, AGPL or SSPL dependency. List every runtime dependency's licence in `THIRD_PARTY_NOTICES.md`.
3. **Never bundle or redistribute agent CLIs** (Muse Code, Claude Code, Codex, Hermes, OpenClaw). Devcules detects or installs them on the user's machine at runtime.
4. **No secrets in the repo.** That covers keys, tokens, real hostnames, personal emails and screenshots of real machines. CI runs Gitleaks on every push.
5. **Security beats convenience.** When in doubt, pick the safer default and document it.
6. **One module per pull request.** Each one gets tests, `go vet`, and a dated entry in `CHANGELOG.md` (newest first). Section 9 says how this works in practice.
7. **Don't guess at outside APIs.** When a fact about one is uncertain (Meta Model API endpoints, SAM 3.1 access, the Muse Code CLI name or its hook format), check the official docs first. Then record what you found in `docs/integrations.md`, with the link.

---

## 1a. Design principles: make it artful

Devcules should feel crafted, in the code and on screen. These rules decide close calls.

**Code**
- **Small, honest interfaces.** A package exposes the fewest types it can. If an interface needs a paragraph to explain, split it.
- **The core knows nothing about plug-ins.** Dependencies point inward: `plugins/` imports `internal/`, never the other way round. CI checks this (see §8).
- **Names read like the product.** Use `approvals.Ask`, `router.Forward`, `link.Dial`, and never `utils`, `helpers` or `manager`.
- **Errors say what to do next.** `vault key missing: set DEVCULES_VAULT_KEY or DEVCULES_VAULT_KEY_FILE`, not `invalid config`.
- **No dead code and no speculative options.** Build the thing the milestone needs, cleanly, and nothing more.
- **Comments say why, not what.** Every package has a `doc.go` with one paragraph on its job and its boundaries.

**Interface (UI)**
- **One visual system.** It's defined once in `web/src/theme/`: colour tokens (dark first, light too), a 4 px spacing scale, two type sizes for body text, and one accent colour used only for "needs you" and primary actions.
- **Phone first.** Every screen works one-handed at 390 px width before it gets a desktop layout. Touch targets are at least 44 px.
- **Calm by default.** Show only what needs attention. Idle agents fade back, and a pending approval is the loudest thing on screen.
- **Motion with meaning.** Short transitions (≤150 ms) show where something came from, and there's no decorative animation. Respect `prefers-reduced-motion`.
- **Words are part of the design.** Labels are plain and short ("Allow once", "Deny", "Take control"), and the UI never shows raw error dumps.
- **Accessible.** Colour contrast meets WCAG AA, everything works by keyboard, and every control has a screen-reader label.

---

## 2. Architecture: a modular monolith with one trust split

Devcules has one codebase, one binary and one container image. The binary runs in different **roles**, and processes are split only where trust differs.

```
            phone / browser (PWA)                          your other computers
                    │  HTTPS over Tailscale                          │
                    ▼                                                │ WebSocket, dials out
 ┌──────────────────────────────────────────┐                        │
 │ devcules serve          (control plane)  │◄───────────────────────┘
 │  auth · vault · real API keys            │        devcules machine (helper)
 │  redacting model router · audit          │
 │  approvals · push · MCP server · API/UI  │
 │  store (SQLite | Postgres) · event bus   │
 └──────────────▲───────────────────────────┘
                │ WebSocket "link", dials out, runner token
 ┌──────────────┴───────────────────────────┐
 │ devcules runner         (untrusted zone) │
 │  tmux terminals · agent CLIs · workspace │
 │  per-launch tokens only, never real keys │
 └──────────────────────────────────────────┘
```

| Role | Command | Holds | Scales as |
|---|---|---|---|
| Control plane | `devcules serve` | vault, real provider keys, users, audit, router | A stateless Deployment once it runs on Postgres |
| Runner | `devcules runner` | terminals, agent processes, mounted code | One per user or workspace (a StatefulSet with its own disk) |
| Machine helper | `devcules machine` | its own ed25519 key; local policy | One per computer |

**One protocol, "link".** The runner and the machine helper both *dial out* to `serve` over a single authenticated WebSocket, and nothing listens on their side. Messages are typed JSON frames, versioned from day one (`{"v":1,"type":"…","id":"…","data":{…}}`).

**Why this shape.** Agents run untrusted code, so they never share a process or a container with the keys. Everything else stays in one process, because more services would add network hops and ops work without adding safety.

---

## 3. Modules and the plug-in contract

Each module lives in its own package under `internal/`. The core never imports a plug-in package directly: plug-ins register themselves through interfaces.

```
cmd/devcules/            main: subcommands serve | runner | machine | hook | version
internal/
  config/                env + file config, validated at start
  store/                 Store interface; sqlite/ (default, modernc, no cgo), postgres/ (pgx)
  bus/                   Bus interface; memory/ (default), pg/ (LISTEN/NOTIFY when on Postgres)
  auth/                  admin bootstrap, sessions, passkeys (WebAuthn), API tokens
  vault/                 AES-256-GCM; key from env or file; never logs values
  redact/                detectors (Gitleaks rules + PII), deterministic placeholders, restore
  router/                /llm/{provider}: OpenAI-style + Anthropic-style passthrough with redaction
  audit/                 append-only log; JSON-lines export
  approvals/             pending decisions, policy (critical rules), push fan-out
  push/                  Web Push (VAPID)
  mcp/                   the Devcules MCP server (tools for agents and assistants)
  link/                  runner + machine WebSocket protocol, both ends
  terminals/             tmux sessions + PTY streaming (runner side) and relay (serve side)
  api/                   REST + WebSocket handlers
  web/                   embedded built PWA (go:embed)
plugins/
  agents/                Agent interface; musecode/, claudecode/, codex/
  assistants/            Assistant interface; builtin/ (Muse Spark), hermes/, openclaw/
  providers/             Provider interface; meta/ (Meta Model API), openai/ (any compatible), ollama/
  machines/              screen, input, accessibility, OCR, SAM 3.1 grounding
web/                     React 18 + TypeScript + Vite + Tailwind PWA source
deploy/compose.yaml      docker compose
deploy/helm/             (stretch) Helm chart
docs/
```

The interfaces below are a starting sketch. Refine them, but keep them small.

```go
type Store interface { /* typed repos: Users(), Sessions(), Agents(), Approvals(), Audit(), Machines(), Traces() */ }

type Bus interface {
    Publish(ctx context.Context, topic string, msg []byte) error
    Subscribe(ctx context.Context, topic string) (<-chan []byte, error)
}

type Provider interface {          // a model vendor reached through the router
    ID() string
    BaseURL() string                // e.g. https://api.meta.ai/v1
    Auth(req *http.Request, key string)
    Models(ctx context.Context) ([]Model, error)
}

type Agent interface {             // a coding agent CLI launched in a runner terminal
    ID() string                     // "musecode", "claudecode", "codex"
    Detect(ctx context.Context) (version string, ok bool)
    Install(ctx context.Context) error           // optional; documented command
    LaunchSpec(l Launch) (cmd []string, env map[string]string, files map[string][]byte)
    // LaunchSpec points the agent at the router with a per-launch token, registers the
    // Devcules MCP server, and installs approval hooks that call `devcules hook`.
}

type Assistant interface {         // a conversational helper that improves the setup
    ID() string                     // "builtin", "hermes", "openclaw"
    Chat(ctx context.Context, sess string, in Message) (<-chan Event, error)
}

type MachineDriver interface {     // per-OS screen/input on the helper side
    Screenshot(ctx context.Context, region *Rect) (Image, error)
    Input(ctx context.Context, a InputAction) error
    Elements(ctx context.Context) ([]Element, error)   // accessibility tree
}
```

**Adding an agent, assistant or provider means adding one package.** The core does not change.

---

## 4. Security model (non-negotiable)

1. **Keys live only in `serve`.** Provider keys sit in the vault. A runner and its agents get a **per-launch token** that works only against `serve`'s router, expires, and can be revoked.
2. **Every model call is redacted at the source.** It goes `agent → router → provider`. The router replaces secrets and optional PII with deterministic placeholders (an HMAC under a per-install key), then restores them in the reply, streaming included. Modes per type: off, log, mask or block. A test endpoint and counters are included, and values are never logged.
3. **Approvals.** Every tool call passes through a policy:
   - **Access modes per runner and machine:** Agents off, Ask on critical, Ask for everything, Full access. There is no implicit default; the user picks one.
   - **Critical rules (editable):** recursive delete, disk format, `dd of=`, shutdown or reboot, `sudo`/`runas`, force push, `git reset --hard`, `curl … | sh`, recursive chmod or chown, `DROP TABLE`, `crontab -r`, plus any call the agent marks `irreversible`.
   - A pending approval becomes a Web Push notification. You answer it from the phone (Allow once, Deny), and the answer is recorded in audit.
4. **The audit log is append-only.** It records who did what, approvals, logins, config changes and trace access, and has a JSON-lines export.
5. **Network.** `serve` binds to localhost by default. Phone access goes through the Tailscale sidecar (HTTPS on the tailnet). Never expose a public port by default.
6. **Auth.** A random admin password is printed once on first start. Passkeys (Face ID or Touch ID) come next. Sessions are HttpOnly, Secure and SameSite=Strict cookies, with CSRF protection on state-changing requests.
7. **Machines.**
   - **Enrollment:** a one-time 12-character code that expires in 15 minutes and is stored hashed, with rate limits. Enrolling creates an ed25519 key on the computer, the private key never leaves it, and every connect signs a fresh challenge.
   - **Local control:** the computer's own local `policy.yaml` and a local `pause` always win over the server.
   - **Untrusted screen content:** text read from the screen (accessibility trees, OCR) reaches the model fenced as untrusted data.
8. **Containers.** Images are non-root with a read-only root filesystem where possible, and drop all capabilities. The runner gets only the mounted workspace.

---

## 5. Meta integrations (defaults)

| Slot | Default | Notes |
|---|---|---|
| Models | **Meta Model API** (OpenAI-compatible, `https://api.meta.ai/v1`, `muse-spark-*`) | Supports Chat Completions and the Responses API with function tools and hosted `web_search`. Verify the current model names. Also support any OpenAI-compatible endpoint and Ollama or vLLM for Meta open weights (a fully local mode). |
| Coding agent | **Muse Code** | Launched in a runner terminal, pointed at the router, with the Devcules MCP server and approval hooks. Verify the CLI binary name, its config, its MCP registration and its hook or approval mechanism, and document them. Claude Code and Codex are the second and third adapters. |
| Assistant | **Built-in assistant on Muse Spark** | Uses the Devcules MCP tools to inspect and improve the setup: add an agent, fix a provider, explain a trace. Hermes and OpenClaw adapters come later (they talk OpenAI-style APIs and MCP). |
| Machines | **Muse Spark plans; SAM 3.1 grounds** | When the accessibility tree is empty (canvas apps, remote desktops, locked UIs), send the screenshot plus a text prompt ("the Pay button") to SAM 3.1 and click the mask's centre. Verify how SAM 3.1 is served (Meta Model API, or a local PyTorch sidecar) before building. |

---

## 6. Configuration

All configuration comes from env vars, with optional file equivalents for secrets (`*_FILE`). Every setting is validated at start, and Devcules exits with a clear message if one is wrong.

| Var | Default | Meaning |
|---|---|---|
| `DEVCULES_ROLE` | `serve` | `serve`, `runner` or `machine` (a subcommand wins) |
| `DEVCULES_LISTEN` | `127.0.0.1:7659` | HTTP listen address for serve |
| `DEVCULES_DATA_DIR` | `/data` (container) or `~/.local/share/devcules` | SQLite, vault, uploads |
| `DEVCULES_DATABASE_URL` | empty = SQLite | `postgres://…` switches the store and bus to Postgres |
| `DEVCULES_VAULT_KEY` / `_FILE` | generated on first start | 32-byte key, base64 |
| `DEVCULES_SERVE_URL` | `http://serve:7659` | for runner and machine to dial |
| `DEVCULES_RUNNER_TOKEN` / `_FILE` | generated by serve | runner enrolment |
| `DEVCULES_WORKSPACE` | `/workspace` | the code folder the runner may touch |
| `DEVCULES_PUBLIC_URL` | empty | external URL (for push and links), e.g. the Tailscale HTTPS name |
| `META_API_KEY` / `_FILE` | empty | stored into the vault on first start, then not needed in env |

Health endpoints: `/healthz` (the process is up) and `/readyz` (the store is reachable, migrations are done, and at least one runner is linked when runners are configured). Logs go to stdout as JSON. On SIGTERM, Devcules stops accepting work, drains for up to 20 seconds, and exits.

---

## 7. Install and run

**docker compose (the front door):**

```yaml
# deploy/compose.yaml — sketch
services:
  serve:
    image: ghcr.io/devcules/devcules:latest
    command: ["serve"]
    environment: { DEVCULES_LISTEN: "0.0.0.0:7659", DEVCULES_PUBLIC_URL: "${DEVCULES_PUBLIC_URL:-}" }
    volumes: [ "devcules-data:/data" ]
    ports: [ "127.0.0.1:7659:7659" ]
    healthcheck: { test: ["CMD", "devcules", "health"], interval: 10s }
  runner:
    image: ghcr.io/devcules/devcules:latest
    command: ["runner"]
    environment: { DEVCULES_SERVE_URL: "http://serve:7659" }
    volumes: [ "${DEVCULES_CODE_DIR:-~/code}:/workspace", "runner-home:/home/devcules" ]
    depends_on: { serve: { condition: service_healthy } }
  tailscale:                       # docker compose --profile tailscale up
    profiles: ["tailscale"]
    image: tailscale/tailscale:stable
    # TS_AUTHKEY from .env; `tailscale serve` HTTPS -> serve:7659
  postgres:                        # docker compose --profile postgres up
    profiles: ["postgres"]
    image: postgres:17
volumes: { devcules-data: {}, runner-home: {} }
```

- **One `Dockerfile`, multi-stage:** a web build, then a Go build (`CGO_ENABLED=0`), then a slim final image with `tmux`, `git`, `bash`, `ca-certificates` and a non-root `devcules` user. Agent CLIs are installed on first run into `runner-home`, not baked into the image.
- **Single binary** for people who want no Docker: `devcules serve` and `devcules runner` on the host, with a systemd user unit on Linux or a LaunchAgent on macOS.
- **Machine helper:** a one-line install script served by `serve` (`/install/machine.sh`, `/install/machine.ps1`), with SHA-256 checks.
- **Kubernetes (stretch):** a Helm chart with `serve` as a Deployment and `runner` as a StatefulSet, built from the same image.

---

## 8. CI/CD from the first commit

CI/CD exists before any feature code: it is the first module (M0.1). After that, nothing merges to `main` unless the pipeline is green.

**On every pull request (`.github/workflows/ci.yml`):**

| Job | What it runs | Fails when |
|---|---|---|
| lint | `gofmt -l`, `go vet`, `staticcheck`, `eslint`, `tsc --noEmit` | any finding |
| arch | a small Go test that parses imports and enforces the module boundaries (`internal/` never imports `plugins/`; the runner role never imports `vault`) | a forbidden import |
| unit | `go test -race -cover ./...` and `vitest run` | a failing test, or coverage of a touched package below 70% |
| integration | `docker compose up` in CI, then `scripts/smoke.sh` (health, sign-in, terminal round-trip, router redaction against a mock provider) | any smoke step |
| e2e | Playwright against the compose stack, at phone (390 px) and desktop sizes, plus a screenshot diff of key screens | a failing flow or an unexpected visual change |
| security | Gitleaks, `govulncheck`, `npm audit --omit=dev`, a licence check (go-licenses or license-checker; MIT, BSD, ISC and Apache-2.0 only) | a secret, a known vulnerability or a disallowed licence |

**On merge to `main` (`.github/workflows/cd.yml`):**
- It builds a multi-arch image (amd64 and arm64) and pushes `ghcr.io/devcules/devcules:edge` plus a `sha-<commit>` tag.
- It generates an SBOM (Syft), signs the image keylessly (cosign, GitHub OIDC), and attaches both to the image.

**On a `v*` tag (`.github/workflows/release.yml`):**
- GoReleaser builds the `devcules` binaries for macOS, Linux and Windows, with checksums and release notes taken from `CHANGELOG.md`.
- It publishes the `:vX.Y.Z` and `:latest` images, and the install scripts verify the checksums.

**Repo settings:**
- Branch protection on `main` requires the `lint`, `arch`, `unit`, `integration` and `security` jobs.
- History is linear (squash merges), commits carry DCO sign-off, and Dependabot handles Go modules, npm and Actions.

---

## 9. How we build: part by part, module by module

The plan is split into **modules**, each a single pull request. A module is done only when it passes its own **test gate**. The next module starts only after the previous one is merged with CI green. Never build two modules in one PR.

**Every module PR contains:**
1. The package with its `doc.go`, which says what the module does and doesn't do.
2. Unit tests for its behaviour. If it implements an interface, it also includes the shared **contract test** for that interface (e.g. `storetest.Run(t, newStore)`), so every implementation passes the same suite.
3. Any integration or smoke steps it adds to `scripts/smoke.sh`.
4. Docs: a section in the right `docs/*.md`, and a `CHANGELOG.md` entry.
5. A PR description holding the module's **Verify** commands and their pasted output.

**Order (each line is one PR; ✓ = test gate):**

| # | Module | Test gate |
|---|---|---|
| M0.1 | CI/CD pipeline, repo files, licence | the workflows run green on an empty `main` |
| M0.2 | `config` and `cmd/devcules` subcommands | table tests for every env var, plus bad-config error messages |
| M0.3 | `store` interface, SQLite and migrations | `storetest` contract suite |
| M0.4 | `bus` interface, memory | `bustest` contract suite |
| M0.5 | Dockerfile, compose, health endpoints | compose up in CI, `/healthz` and `/readyz` |
| M1.1 | `auth`: bootstrap, sessions, CSRF | unit tests, plus a Playwright sign-in |
| M1.2 | `web` shell and theme system | Playwright at 390 px and desktop, plus a screenshot baseline |
| M1.3 | `link` protocol, both ends | a fuzz test on frame decode, plus reconnect tests |
| M1.4 | `terminals`: runner PTY and serve relay | a round-trip test through tmux, plus a disconnect and resume test |
| M1.5 | passkeys | WebAuthn tests with a virtual authenticator |
| M2.1 | `vault` | encrypt and decrypt, wrong key, rotation |
| M2.2 | `redact` | golden tests per detector, plus a streaming restore fuzz test |
| M2.3 | `router` and the Meta provider | a mock provider test; one live test behind `META_API_KEY` |
| M2.4 | `audit` and traces | append-only enforcement, plus export |
| M3.1 | `mcp` server | MCP protocol conformance tests |
| M3.2 | `approvals` and `push` | a policy table test, plus a push to a test subscriber |
| M3.3 | `agents/musecode` | `agenttest` contract suite, plus a smoke launch |
| M3.4 | `agents/claudecode`, `agents/codex` | `agenttest` contract suite |
| M4.1 | `machine` enrolment and link | an enrolment code lifecycle test, plus a signature challenge test |
| M4.2 | screen and input drivers (Linux X11 first, then macOS and Windows) | an Xvfb-based test in CI |
| M4.3 | accessibility tree and OCR | fixture trees per OS |
| M4.4 | SAM 3.1 grounding | a fixture screenshot that resolves to a known click point |
| M4.5 | task view and take control | Playwright with a fake machine |
| M5.1 | `assistants/builtin` | a scripted conversation against a mock model, with approval enforced |
| M5.2 | `assistants/hermes`, `assistants/openclaw` (stretch) | `assistanttest` contract suite |

When a module turns out to need a change to an interface it doesn't own, stop. Make that interface change as its own small PR first.

---

## 10. Milestones

The milestones group the modules above into stages. Each milestone ends with a **Verify** list. Run every item and paste the output into the PR description. Mark anything you could not verify, and say why.

### M0: Skeleton
- Repo files: `LICENSE` (Apache-2.0), `NOTICE`, `THIRD_PARTY_NOTICES.md`, `README.md` (pitch plus quick start), `CONTRIBUTING.md` (DCO sign-off), `SECURITY.md`, `CODE_OF_CONDUCT.md` and `CHANGELOG.md`.
- `cmd/devcules` with subcommands, plus `config` and `/healthz`, `/readyz` and `devcules health`.
- The `store` interface with SQLite and migrations, and the in-memory `bus`.
- The Dockerfile and `deploy/compose.yaml` (serve and runner).
- CI/CD as specified in §8, and it comes first.
- **Verify:** `docker compose -f deploy/compose.yaml up` → `curl localhost:7659/healthz` returns ok, and the runner shows as linked in `/readyz`. CI is green.

### M1: Sign-in, PWA shell and terminals
- Admin bootstrap, sessions and passkeys.
- The PWA shell: installable, with a dark theme, a bottom tab bar on phones, and a sidebar on desktop.
- The runner link and tmux terminals streamed to xterm.js. Terminals survive a browser disconnect. A phone key strip: Esc, Tab, Ctrl, arrows, Enter.
- **Verify:** sign in from a phone over Tailscale, open a terminal, run `vim`, lock the phone, reopen it, and the session is still there.

### M2: Router, redaction, providers and traces
- `/llm/{provider}` with the Meta Model API provider first, then generic OpenAI-compatible, then Ollama.
- Per-launch tokens.
- Redaction with the modes off, log, mask and block, a test box, and a startup self-test.
- A trace per session: question, tool calls, reply, tokens, and what was redacted.
- An audit log.
- **Verify:** a request containing a fake AWS key reaches a mock provider masked and returns restored. Unit tests cover streaming restore.

### M3: Coding agents, MCP and approvals
- The `Agent` interface with **Muse Code**, then Claude Code and Codex.
- Each launches in a runner terminal with its router base URL, per-launch token, the Devcules MCP server and approval hooks (`devcules hook`).
- An agent board (Needs you, Working, Idle).
- Web Push approvals answered from the phone.
- **Verify:** start Muse Code on a sample repo, ask for a change that runs `git push --force`, approve it on the phone, and see the approval and the push in audit.

### M4: Machines
- `devcules machine` covering enrolment, shell and files, screenshots, input, the accessibility tree (macOS AX, Windows UIA, Linux AT-SPI) and access modes.
- Live screen view and take control from the phone.
- **SAM 3.1 grounding** when the tree is empty.
- An action budget per task: 40 screen actions.
- A done-verifier: a fresh screen check before a task may report success.
- **Verify:** from the phone, ask the agent to open a browser on a second computer and click a canvas-drawn button that has no accessibility node. SAM finds it, and the click lands.

### M5: Assistant
- The built-in assistant on Muse Spark, using the Devcules MCP tools (list agents, add a provider, read traces, open terminals).
- Hermes and OpenClaw adapters, if time allows.
- **Verify:** ask the assistant "my Codex can't reach its model, fix it". It finds the bad provider setting and proposes the fix, which waits for your approval.

### M6: Polish and docs
- The README quick start in under 2 minutes, `docs/` (architecture, security, integrations), the Tailscale profile and a sample demo app.
- A security pass: dependency audit, Gitleaks, `govulncheck`.
- **Verify:** someone else follows the README on a clean machine and has it running in under 5 minutes.

### M7: Demo
- A recorded run of the demo script in §11, and screenshots for the README.

**Stretch goals, only if everything above is done:** the Postgres store and bus, a Helm chart, saving a finished run as a replayable procedure, site-locked secrets for browsers, and a "Devcules Cloud" waitlist link.

**Out of scope for now:** multi-user teams, SSO and SCIM, per-user VMs and the hosted Hub (the future org edition), A2A, and PR review boards.

---

## 11. Demo script (90 seconds)

1. On a phone, ask Muse Code to fix a checkout bug in the sample app.
2. Muse Code pushes a fix, then uses Machines to check it on a real laptop's browser.
3. Muse Spark plans the steps. SAM 3.1 finds the Pay button on a canvas-drawn page that the accessibility tree can't see.
4. The agent wants to run a database migration, which is a critical step. The phone buzzes, and you approve with Face ID.
5. The verifier confirms the order went through. The trace shows every model call, with secrets masked.

---

## 12. Definition of done

- [ ] `docker compose up` gives a working Devcules from a clean machine in under 5 minutes.
- [ ] Muse Code, Claude Code and Codex launch from the UI, and every model call is redacted and traced.
- [ ] Critical steps reach the phone as push notifications, and the answer is audited.
- [ ] A second computer is enrolled and driven, including one SAM 3.1-grounded click.
- [ ] The built-in Muse Spark assistant can inspect and fix a setting, with approval.
- [ ] CI is green, Gitleaks is clean, and every dependency is permissive and listed.
- [ ] The README says how it was built: "Built with Muse Code; planning and review with other AI tools."
