# Link protocol (M1.3)

Serve and runner hold one WebSocket each at `GET /link`. The link
carries runner presence today and terminal frames in M1.4; machine
links arrive in M4.1. There is no request/response layer: frames are
one-way messages routed by type.

Packages: `internal/link` (frames and codec, shared), `internal/link/hub`
(serve side), `internal/link/dial` (runner side, later machines too),
`internal/link/linktest` (in-process pipe harness for tests).
`internal/link/dial` is runner-side code: the arch test forbids it
from depending on the vault.

## Frames

One WebSocket text message is one frame:

```json
{"v":1,"type":"hello","id":"…","data":{"role":"runner","name":"…","version":"…"}}
```

- `v` must be `1`. Anything else closes with 1008.
- `type` matches `^[a-z][a-z0-9.]{0,31}$` (e.g. `term.write`).
  Handlers register by type (`hub.On`, `dial.On`); M1.4 adds types
  without changing the protocol.
- `id` is 1–64 characters. Callers correlate with it; the link does not.
- `data` stays opaque to the codec — each handler decodes its own
  shape. It must be present (never missing or `null`).
- Unknown top-level fields are rejected, as is trailing data after the
  JSON value. One frame is exactly one value.
- Read limit is 1 MiB per frame on both ends (the library default of
  32 KiB is raised on the runner side too). Oversize senders are
  dropped with 1009.

`FuzzDecode` fuzzes the codec (30 s in CI) with a committed seed
corpus under `internal/link/testdata/fuzz`.

## Handshake

1. The runner dials with `Authorization: Bearer <token>` (see below) and no
   `Origin` header.
2. The runner sends `hello` with `{role, name, version}`.
3. Serve answers `welcome` with `{session}`. The session identifies
   this link generation; a reconnect gets a new one. `welcome` is
   always the first frame serve sends: the link registers only after
   it goes out, so nothing can overtake it.

Close codes and reasons:

| Condition | Code | Reason |
|---|---|---|
| unsupported `v` | 1008 | `unsupported link version` |
| unknown `role` | 1008 | `unknown role` |
| no `hello` within 10 s (or first frame unusable) | 1008 | `no hello` |
| duplicate runner name | 4000 | `replaced` (old link retires) |
| serve or runner shutting down | 1001 | `serve shutdown` / `runner shutdown` |

A second runner with the same name replaces the old link: the elder
gets 4000 and exits (`Run` returns `ErrReplaced`, the runner process
exits 1) instead of redialling, which would evict the newer runner and
flap forever. Runner names
default to the hostname (`runner` fallback); containers and machines
have distinct hostnames, and a restarted runner reclaims its name.

## Auth

`GET /link` takes the runner token as a bearer credential, checked
**before** the upgrade (401 otherwise) with a constant-time compare
against a stored SHA-256 hash. The plaintext lives only in config
and the token file.

Any `Origin` header is rejected with 403: browsers always send one
and the runner never does, so this blocks cross-site socket hijacking
even with a leaked token. `/link` never reads the session cookie.

## Machines

Machines enrol via a one-time code that serve mints (`machines.Issuer.Mint`, code shown once,
only its hash stored). A machine redeems it with its ed25519 pubkey; thereafter it
authenticates on `GET /link` with `hello{role:"machine", name:"…"}`, plus an ephemeral
nonce challenge: serve issues a server nonce, the machine signs it with its key (verified in
`machines.Verify`). Nonces are memory-only and single-use. See `internal/machines`.

## Token lifecycle

One mechanism for compose and single-binary deploys:

- `DEVCULES_RUNNER_TOKEN_FILE` names the file for both roles;
  default `<DataDir>/runner.token` (compose sets `/link/runner.token`).
- Serve: direct env value wins; else the file is read when it exists;
  else 32 random bytes are generated, written `0400`, and the **path**
  is logged, never the value. Only the hash is kept in memory.
- Runner: reads the env value or the file, like any secret. It never
  mints: a missing token exits with next steps.
- Compose mounts a `link` volume at `/link` (read-write on serve,
  read-only on runner). `/link` is owned by uid 10001 in the image so
  a fresh volume inherits that ownership.

Rotation is delete-the-file plus restart both services. The runner
treats a 401 as "token rejected: check `DEVCULES_RUNNER_TOKEN_FILE`"
and keeps backing off; it never hammers serve.

## Liveness and reconnect

- Heartbeat ping every 15 s; a link is dead after 2 missed pongs.
- The runner reconnects with exponential backoff and full jitter
  (0.5 s up to 30 s), reset after each welcome. Jitter spreads a fleet
  of restarting runners so serve never takes a thundering herd.
- On SIGTERM serve closes every link with 1001 inside the 20 s drain
  (HTTP shutdown cannot reach hijacked sockets); the runner closes
  with 1001 and exits 0.
- Logs never contain the token.

## Readiness

`/readyz` answers `{"status":"ok","runners_linked":N}` and is 503
only when the store is down. Readiness deliberately does **not** wait
for a runner: the UI must stay reachable to show "runner offline",
and the compose healthcheck would deadlock (the runner waits for
serve to be healthy). See `docs/deploy.md`.
