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
| `DEVCULES_TAG` | Image tag, default `edge`. |

No vault key is needed: it stays optional, and the runner carries no
secrets at all — only `DEVCULES_SERVE_URL`.

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

## Known deviations and limits

- **`/readyz` reports the store only.** PLAN §6 also wants at least one
  linked runner when runners are configured, but the link protocol is
  M1.3: readiness is deliberately not faked, and this stays until then.
- **`runner-home` ownership.** The named volume starts root-owned, so
  the non-root runner cannot write it yet. The idle M0.5 runner writes
  nothing, so this is dormant; M3 (which installs agent CLIs there)
  must take ownership on first write.
- **No Tailscale or Postgres profiles yet** (M6.2 / stretch goal).
  `DEVCULES_DATABASE_URL` set makes serve exit with next steps instead
  of silently using SQLite.
