# Architecture

Devcules is a control plane (`devcules serve`) and any number of
runners (`devcules runner`, or the `local` host desk which is the two
in one process), linked by a WebSocket the runner dials out on. The
control plane is the only side that holds provider keys, the vault
and the signing material; runners and machine helpers are the
untrusted zone, and every rule in `docs/security.md` bends that way.

```
          browser (PWA, phone or desktop)
             │  session cookie · WebAuthn · Web Push
             ▼
   ┌────────────────────── devcules serve ──────────────────────┐
   │ auth + passkeys    vault (AES-256-GCM)      redact          │
   │ router /llm/{p}    audit (append-only)      approvals + push │
   │ MCP  /mcp          assistants               store (SQLite)   │
   │ api /api           web (embedded PWA) bus (memory|pg)        │
   └────▲───────────────────────────▲──────────────────────▲─────┘
        │ /link WebSocket           │ /link                │ /link
   ┌────┴─────┐              ┌──────┴──────┐         ┌─────┴──────┐
   │  runner  │              │   runner    │         │   machine  │
   │ tmux +   │              │ tmux +      │         │  helper    │
   │ agent CLIs│             │ agent CLIs  │         │ screen+in  │
   └──────────┘              └─────────────┘         └────────────┘
        workspace/editor only, per-launch token, never a real key
```

Serve and a runner may be the same process (PLAN.md §10's one-command
desk: `devcules` on a computer is `runLocal`, the two halves in one
binary), or split on a compose stack; the security boundary between
them is identical either way because the halves only ever speak the
same protocols.

## Process and subsystem map

The package layout mirrors the subsystems each doc owns; the arch test
(`arch_test.go` in the repo root) mechanically enforces it.

| Subsystem | Owns | Talks to |
|---|---|---|
| `auth` | bootstrap, sessions, passkeys, API tokens | store Users/Sessions/Passkeys |
| `vault` | AES-256-GCM of secrets under one key | `store.Secrets` only |
| `redact` | detectors + placeholder round trip | settings; used by router, assistants |
| `router` | `/llm/{provider}`, redacted and traced | redact, trace, vault (via KeySource) |
| `audit` | append-only record, JSONL export | store.Audit |
| `approvals` + `push` | policy, pending approvals, Web Push | store.Approvals; `push` |
| `mcp` | the MCP server at `/mcp` | its own registry; tools register into it |
| `link` | the runner/machine WebSocket | `link/hub` (serve) ↔ `link/dial` (runner) |
| `terminals` | tmux on the runner, the browser relay | `link`, `terminals/pty` on serve only via relay |
| `machines` | enrolment, helper driver, grounding | `link`, `redact`, `vault` |
| `launch` | per-launch tokens | store.LaunchTokens |
| `assistants` | the conversational helper (M5) | `mcp`, `approvals`, `redact`, `assistantsopenai` |
| `agents` (plugins) | one CLI adapter each | `launch`, `router`, `mcp` for the launch spec |
| `api` | the `/api/` mux the PWA reads and writes | everything, via thin handlers |
| `web` | the embedded PWA | `/api/` over the same session |

The trusted/untrusted split is enforced by construction, not policy:
`internal/config.ForRole` refuses to let a runner, machine or hook
process start with `DEVCULES_VAULT_KEY` or `META_API_KEY` set, and
`arch_test.go`'s repo-ownership rule keeps `store.Secrets`,
`store.LaunchTokens`, `store.Users` and the like reachable only from
their owning package. The runner is handed per-launch tokens; the
router is on the serve side upstream of every model call it makes.

## Request paths that matter

- **A GUI write** (start a launch, forget a project):
  browser → `requireSession` cookie check → `csrf` origin check →
  handler → subsystem → audit record. Writes never pass secrets to the
  runner; they mint a launch token and the launch's files carry it.
- **A model call**:
  agent → Bearer launch token → `router` checks the launch and its
  provider's profile → `redact` masks the body → provider upstream →
  `redact` restores on the way back → `trace` records the scan by
  launch. The secret crossing the process boundary to the agent is
  only the masked conversation; the key stays a vault secret.
- **An assistant turn**:
  browser → `/api/assistants/{id}/chat` → `assistants.Loop.Chat` →
  redaction-in → model (the assistant's one upstream, on the shared
  no-redirect client) → for each `tool_calls`, `approvals.Ask` for the
  assistant's own target and, on an ask, wait → in-process MCP tool
  call under the assistant's launch token → events buffered and
  returned as one JSON. No streaming; the gate verdict and approver
  are on every tool event.
- **An answer from the phone**:
  push notice on the approval → `POST /api/approvals/{id}` →
  `approvals.Answer` wakes the waiting hook (agent) or gate
  (assistant) → audit records who answered.

## Storage and versioning

SQLite under the data dir by default (PLAN.md §5's store interface,
`internal/store`), `bus` memory on one process, `pg` LISTEN/NOTIFY
only when Postgres is in play. The vault key is a file under the data
dir serve creates and warns to back up; `META_API_KEY` is migrated
into the vault on first start and then the env var can be dropped
(`docs/config.md`).

## Where a new subsystem lands

- A package under `internal/<name>` with `doc.go`, its tests and (if
  it implements a shared interface) the contract suite call for that
  interface (`storetest.Run`, `assistanttest.Run`).
- The arch test: is it `plugins/`? is it internal? does store only
  come from `internal/store`? does its name collide with an owned
  repo (`arch_test.go`'s reserved map)? and no `plugins` import from
  `internal`.
- The CI matrices (`lint`, `arch`, `unit`, `integration`, `security`,
  `e2e`) pick it up by path; a new web surface adds its API calls in
  `web/src/api.ts` and its screen under `web/src/screens/`.
