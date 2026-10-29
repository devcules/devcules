# Configuration

All configuration comes from env vars, loaded and validated at start by
`internal/config`. Unset variables fall back to the defaults below; set
variables must be valid. Every problem is reported at once as one error
listing them all. Secret values never appear in errors, and URLs are
quoted with any password masked (or hidden when they do not parse).

Secrets have a `*_FILE` variant holding a path to read instead: when both
forms are set, the direct value wins and the file is ignored entirely.

| Var | Default | Meaning |
|---|---|---|
| `DEVCULES_ROLE` | `serve` | `serve`, `runner` or `machine`. A subcommand wins over it; with no subcommand it decides the role. |
| `DEVCULES_LISTEN` | `127.0.0.1:7659` | HTTP listen address, `host:port` with port 1-65535. Empty host means all interfaces. |
| `DEVCULES_DATA_DIR` | `/data` in a container, else `~/.local/share/devcules` | Must be an absolute path. Container detection trusts only `/.dockerenv`, `/run/.containerenv`, `KUBERNETES_SERVICE_HOST` and container runtimes in PID 1's cgroup; images should also set the variable explicitly. |
| `DEVCULES_DATABASE_URL` | empty = SQLite | `postgres://…` switches the store and bus to Postgres (M0.3). |
| `DEVCULES_VAULT_KEY` / `_FILE` | none (optional) | Base64 of 32 raw bytes, validated when set. Unset is fine: serve generates `<DataDir>/vault.key` (`0600`) on first start; back it up with the database. Only serve may hold it: `runner`, `machine` and `hook` refuse to start when it is set. See `docs/vault.md`. |
| `DEVCULES_VAULT_NEW_KEY` / `_FILE` | empty | Read only by `devcules vault rotate` when `DEVCULES_VAULT_KEY` is set: the key to rotate to. Same format and same serve-only rule. |
| `DEVCULES_SERVE_URL` | `http://serve:7659` | What runner and machine dial. Must be an `http(s)` URL with a host. |
| `DEVCULES_RUNNER_TOKEN` / `_FILE` | empty, file defaults to `<DataDir>/runner.token` | Runner link token (a single whitespace-free string). Direct value wins; else the explicit file is read; else the default file is read when it exists. A missing file is not a config error (serve mints it on first start, and compose names the path before it exists); any other read failure is. Serve mints the file (`0400`) when nothing is set and logs the path, never the value; the runner only reads and fails with next steps when missing. An explicit `_FILE` must be absolute. See `docs/link.md`. |
| `DEVCULES_WORKSPACE` | `/workspace` | The code folder the runner may touch. Must be an absolute path. |
| `DEVCULES_PUBLIC_URL` | empty | External URL for push and links, e.g. the Tailscale HTTPS name. Must be an `http(s)` URL with a host when set. Also the WebAuthn relying party (M1.5): passkeys work on `http://localhost:7659` and https names, and stay off without it or on an IP host (see `docs/auth.md`). |
| `META_API_KEY` / `_FILE` | empty | Moved into the vault (`provider/meta/api_key`) on start; once logged as stored, it can leave the environment. A single whitespace-free string. Serve-only, like the vault keys. |
| `DEVCULES_TRACE_DAYS` | `30` | How many days model call traces are kept (0 to 3650; `0` keeps them forever). Serve prunes at start and once a day. The audit log is never pruned. See `docs/audit.md`. |
| `DEVCULES_META_BASE_URL` | `https://api.meta.ai` | Meta Model API origin the router forwards to, without `/v1`. Must be an `http(s)` URL with a host; a trailing slash is dropped. Point it at a compatible endpoint or a test fake. See `docs/router.md`. |
| `DEVCULES_PUSH_CONTACT` | the public URL when it is https, else the project page | The contact push services see in the VAPID token (RFC 8292 `sub`): a `mailto:` or `https:` URL. See `docs/approvals.md`. |
| `DEVCULES_LAUNCH_TOKEN` / `_FILE` | empty | The launch token `devcules hook` sends with each tool call. Devcules sets it when it launches an agent; only the hook reads it. See `docs/approvals.md`. |

## Subcommands

`devcules [serve|runner|machine|hook|version|health|vault rotate|audit export|audit verify|help]`. Exit codes: 0
done, 1 configuration or run failure, 2 wrong invocation.

- `version`, `help` (also `-h`, `--help`) and usage errors never read the
  configuration.
- `version` prints the build identity (`version`, `commit`, `date`
  injected via `-ldflags` at release; `dev`/`none`/`unknown` locally).
- `health` GETs `/healthz` on the configured listen address (5 s
  timeout) and exits 0 on a 2xx answer, 1 otherwise. The endpoint
  itself lands in M0.5.
- `serve` opens the store and bus, mints the runner token file when
  needed, and runs the HTTP server (with `/link` for runners) until
  SIGTERM drains it. `runner` links to serve at `/link` and holds the
  link across restarts until SIGTERM. `machine` and `hook` validate
  the config, print what they would start, and exit. They run nothing
  yet.
- `vault rotate` rewraps the vault's data key under a new vault key
  and prints both key ids (see `docs/vault.md`). It runs beside serve,
  with serve's configuration.
- `audit export [-after N]` writes the audit log as JSON lines to
  stdout (and records the export first); `audit verify` checks the
  database's hash chain, and `audit verify FILE` (or `-` for stdin)
  checks an exported file on its own. Both read serve's database and
  hold no secrets (see `docs/audit.md`).
