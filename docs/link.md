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
- Read limit is 4 MiB per frame on both ends, sized for a machine's
  2 MiB screen capture sent base64 in JSON. The library default of
  32 KiB is raised on the runner side too. Oversize senders are
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

A machine enrols once and then signs a fresh challenge on every
connect. It never holds the runner token.

1. The owner mints a one-time code in the Machines tab
   (`POST /api/machines/enrol-code`, session and CSRF). It is 12
   Crockford base32 characters shown as `XXXX-XXXX-XXXX`, valid for
   15 minutes, single use, and only its hash is stored.
2. On the computer, `devcules machine enrol <code> [--name <name>]`
   makes an ed25519 key (`<DataDir>/machine.key`, `0600`; the private
   key never leaves the machine) and posts `{code, name, pubkey, sig}`
   to `POST /api/machines/enrol`. `sig` signs
   `"devcules-enrol-v1\n" + code`, so serve knows the key is really
   held. No session (a machine is not a browser); any `Origin` header
   is 403 and attempts are limited to 10 per IP per 10 minutes. The
   code is claimed before the machine row is written, so two racing
   enrols never both win; a taken name is 409.
3. `devcules machine` dials `GET /link` with `Authorization: Machine`
   (no token) and sends `hello{role:"machine", name}`. Serve answers
   `challenge {nonce}` (32 random bytes, base64url, memory only), the
   machine answers `challenge.answer {sig}` (base64 ed25519 over the
   nonce bytes) inside the hello timeout, and serve checks it against
   the enrolled key before the `welcome`. A wrong key, an unknown
   name or silence closes with 1008 "machine auth failed"; a link that
   came in on the runner token may not claim the machine role (1008).

The hub keys machine links as `machine/<name>` (`hub.MachineAddr`), so
a runner and a machine on one computer can share the hostname without
replacing each other; names never contain `/`. Removing a machine in
the tab (`DELETE /api/machines/{name}`) deletes its key and closes its
link with 1008 "machine removed".

## Machine frames (M4.5)

Machine helpers and serve exchange the `mach.*` frame types over the
same link (`internal/machframes` holds the payload shapes; serve
requests end in a verb, the machine answers under the request's
frame id):

- `mach.screen` → `mach.frame` — one PNG capture, capped by the
  request's `maxBytes`
- `mach.tree` → `mach.treeout` — the accessibility tree
- `mach.words` → `mach.wordsout` — OCR words off the live screen
- `mach.input` → `mach.ok` — one injected input event (move, tap,
  key, text); the machine spends one budget unit here and answers
  `mach.error "budget exhausted"` at zero
- `mach.ground` → `mach.groundout` — SAM grounding of a noun phrase
  against the live screen; the machine holds the router session, so
  no key crosses the link
- `mach.budget` → `mach.ok` — serve resets the task's remaining
  actions (0-40)
- `mach.verify` → `mach.verifyout` — the done-verifier: one fresh
  capture plus one tree read with a note, answered together

A refused request answers `mach.error` with the machine's fixed
reason. Serve's side of the protocol lives in
`internal/machines/relay`; the machine's in
`internal/machines/helper`.

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
