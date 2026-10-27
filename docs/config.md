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
| `DEVCULES_VAULT_KEY` / `_FILE` | none (optional) | Base64 of 32 raw bytes, validated when set. Unset is fine: serve generates a key on first start (M2.1). Only serve may hold it: `runner`, `machine` and `hook` refuse to start when it is set. |
| `DEVCULES_SERVE_URL` | `http://serve:7659` | What runner and machine dial. Must be an `http(s)` URL with a host. |
| `DEVCULES_RUNNER_TOKEN` / `_FILE` | empty | Runner enrolment token (a single whitespace-free string). Issued by serve in a later module. |
| `DEVCULES_WORKSPACE` | `/workspace` | The code folder the runner may touch. Must be an absolute path. |
| `DEVCULES_PUBLIC_URL` | empty | External URL for push and links, e.g. the Tailscale HTTPS name. Must be an `http(s)` URL with a host when set. |
| `META_API_KEY` / `_FILE` | empty | Stored into the vault on first start (a later module). A single whitespace-free string. |

## Subcommands

`devcules [serve|runner|machine|hook|version|health|help]`. Exit codes: 0
done, 1 configuration or run failure, 2 wrong invocation.

- `version`, `help` (also `-h`, `--help`) and usage errors never read the
  configuration.
- `version` prints the build identity (`version`, `commit`, `date`
  injected via `-ldflags` at release; `dev`/`none`/`unknown` locally).
- `health` GETs `/healthz` on the configured listen address (5 s
  timeout) and exits 0 on a 2xx answer, 1 otherwise. The endpoint
  itself lands in M0.5.
- `serve`, `runner`, `machine` and `hook` validate the config, print
  what they would start, and exit. They run nothing yet.
