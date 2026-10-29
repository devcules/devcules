# Deploy

## Local stack

Prerequisites: Docker with the Compose plugin.

```sh
docker compose -f deploy/compose.yaml up -d --wait
./scripts/smoke.sh
```

That builds the image, starts `serve` on
[http://127.0.0.1:7659](http://127.0.0.1:7659) and an idle `runner`,
then proves health, hardening and shutdown. Tear down with
`docker compose -f deploy/compose.yaml down -v`.

Both services build the same local `Dockerfile`: the `:latest` tag
does not exist until the first release, so there is nothing to pull
yet. `DEVCULES_TAG` selects another tag once releases exist.

## Environment

| Var | Effect |
|---|---|
| `DEVCULES_CODE_DIR` | Host code folder mounted at `/workspace`. Defaults to `~/code` (Compose expands the tilde). Create it yourself first: if it is missing, Docker creates it root-owned. On Linux it must also be writable by uid 10001. |
| `DEVCULES_PUBLIC_URL` | Passed through to serve; empty by default. |
| `META_API_KEY` | Passed through to serve, which moves it into the vault on start; once stored it can be removed. Empty by default. |
| `DEVCULES_META_BASE_URL` | Passed through to serve; empty means `https://api.meta.ai`. |
| `DEVCULES_TRACE_DAYS` | Passed through to serve; empty means 30 days. |
| `DEVCULES_TAG` | Image tag, default `edge`. |

No vault key is needed: serve generates `/data/vault.key` on first
start, in the `devcules-data` volume beside the database. Back the two
up together: without the key, the vault's secrets are lost. The runner
carries no secrets at all — only `DEVCULES_SERVE_URL` — and never
mounts `/data`.

The health check is declared on `serve` only (in `deploy/compose.yaml`,
using `devcules health`). The image has no `HEALTHCHECK`, because it would
apply to the runner too, which serves nothing.

## Hardening

| Control | serve | runner |
|---|---|---|
| Published ports | `127.0.0.1:7659` only | none |
| Read-only root filesystem (`tmpfs` on `/tmp`, `/run`) | yes | yes |
| `cap_drop: [ALL]` | yes | yes |
| `no-new-privileges` | yes | yes |
| Runs as uid 10001 (`devcules`) | yes | yes |
| Mounts | data volume only | `/workspace` + runner-home volume |
| `init: true` (tini reaps children) | no | yes — the tmux server's zombies (M1.4) |

## Known deviations and limits

- **`/readyz` answers `{"status":"ok","runners_linked":N}` and is 503
  only when the store is down.** A deliberate deviation from PLAN §6
  (which wants readiness to require a linked runner when runners are
  configured): the UI must stay reachable to show "runner offline",
  and the compose healthcheck would deadlock, because the runner
  waits for serve to be healthy. See `docs/link.md`.
- **Runner token rotation** is delete `/link/runner.token` (or
  `<DataDir>/runner.token` outside compose) plus restart both
  services. Serve mints a fresh file `0400`; the runner picks it up
  on start. `scripts/smoke.sh` checks the mode, the uid, and relink
  after a serve restart.
- **Terminals live in the runner.** Shells start in `/workspace`
  under the private `tmux -L devcules` server (see
  `docs/terminals.md`); tmux sessions die with the runner container,
  and the runner links without terminals when tmux is missing.
- **`runner-home` ownership.** The named volume starts root-owned, so
  the non-root runner cannot write it yet. The idle M0.5 runner writes
  nothing, so this is dormant; M3 (which installs agent CLIs there)
  must take ownership on first write.
- **No Tailscale or Postgres profiles yet** (M6.2 / stretch goal).
  `DEVCULES_DATABASE_URL` set makes serve exit with next steps instead
  of silently using SQLite.
