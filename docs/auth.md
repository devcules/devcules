# Auth

M1.1 owns password sign-in: admin bootstrap, cookie sessions and
CSRF. M1.5 adds WebAuthn passkeys (Face ID, Touch ID, security keys)
beside the password, which stays as the fallback and can never be
removed. API tokens and audit events (M2.4) are later modules and
are not stubbed anywhere here.

## Bootstrap

The first start with no `admin` user generates a random 24-character
password (`crypto/rand`) from an unambiguous alphabet (no `0/O`,
`1/l/I`, `5/S`, `2/Z`, lowercase `o`) and prints it once to stderr
as a plain line:

```text
Admin password (shown once): <password>
```

It never goes through the JSON logger, and later starts never print
it again. Only an argon2id hash (`golang.org/x/crypto`, parameters
in `internal/auth/password.go`) reaches the store. There is
deliberately no password env var: environment secrets leak to child
processes and logs.

## Sign-in

`POST /auth/sign-in` takes a urlencoded form (`username`,
`password`) and answers 204 with the session cookie. A wrong
password and an unknown user share one 401 (`invalid username or
password`), compared in constant time after one argon2id
verification, so logins cannot be enumerated by message or timing.

Past 5 failures per minute per client IP (the connection's remote
address; no proxy headers are trusted) sign-in answers 429 with a
`Retry-After: 60`. A success clears the IP's count, so mistyping
never locks anyone out. Password and passkey sign-ins share this
limiter and its map: the map holds at most 10,000 addresses, and a
new one past the bound evicts the address with the oldest failure.

At most 2 argon2id verifications run at once (one hash holds 64 MiB
briefly); extra sign-ins wait their turn, bounded by the request
context, so a login flood cannot exhaust memory.

## Sessions

The session value is 32 random bytes, hex-encoded, in the
`devcules_session` cookie: `HttpOnly`, `Secure`, `SameSite=Strict`,
`Path=/`, `MaxAge` set to the absolute timeout. The store keeps only
the SHA-256 of the value, so a database read cannot replay a
session.

- Idle timeout 12 h, absolute timeout 7 d (`auth.IdleTimeout`,
  `auth.AbsoluteTimeout`). Every use refreshes the idle marker, so an
  active session lives until the absolute deadline, which is exact to
  the second.
- Timeouts are enforced when the session is used. There is no
  background reaper in M1.1: expired rows are deleted on sight and
  otherwise wait for a later cleanup.
- `POST /auth/sign-out` deletes the session and clears the cookie;
  signing out twice is not an error.

`GET /auth/me` reports `{"username":"admin"}` for a live session.

## CSRF

Every state-changing route sits behind the stdlib
`http.CrossOriginProtection`: cross-site POSTs are rejected with
403, same-origin POSTs pass, and GETs are untouched (safe methods are
never checked). `DEVCULES_PUBLIC_URL`, when set, is trusted as an
origin by scheme plus host, so the browser UI can post from its own
origin. Requests without `Origin` or `Sec-Fetch-Site` (curl,
non-browser clients) are allowed: there is nothing to compare. All
passkey POSTs carry the check.

## Passkeys

Registration happens while signed in (Settings), sign-in is public
and needs no username (discoverable credentials). Both ceremonies run
through `internal/auth`; `internal/api` only maps HTTP. The server
side uses `github.com/go-webauthn/webauthn` (BSD-3-Clause); the
browser uses `navigator.credentials` directly with small base64url
helpers of our own (`web/src/webauthn.ts`).

### Relying party: when passkeys work

The RP id is the host of `DEVCULES_PUBLIC_URL` (no port) and the only
allowed origin is its `scheme://host[:port]`. WebAuthn refuses IP
addresses as an RP id and needs a secure context (https, or
`http://localhost`), so the feature switches on the public URL:

- `http://localhost:7659`: passkeys work locally.
- An https name (for example via `tailscale serve`): passkeys work on
  the phone.
- Unset, an IP host (`127.0.0.1`, a `100.x` Tailscale address), or
  plain http on any other name: passkeys stay off.

When off, `GET /auth/passkeys/status` answers
`{"enabled":false,"reason":"…"}` with next steps in plain words, the
sign-in screen hides the passkey button, and Settings shows the
reason. The password always keeps working. Serve logs the RP id once
at start (or the disabled reason); challenges are never logged.

### Ceremonies

Registration requires a resident key with user verification,
attestation `none`, ES256 or EdDSA, and excludes the user's existing
keys. Sign-in opens a session exactly like a password sign-in: the
same cookie, the same timeouts, 204.

Challenges are 32 random bytes, single use, expiring after 5 minutes.
They live in an in-memory map of at most 1,000 entries, keyed by a
random ceremony id carried in a short-lived `HttpOnly`,
`SameSite=Strict` cookie scoped to `/auth/passkeys` (deliberately not
`Secure`: the id alone completes nothing, only the passkey's private
key signs). A `serve` restart cancels in-flight ceremonies; the
client simply begins again.

The user handle is 32 random bytes minted on first registration
(`users.webauthn_handle`); it is never the username. Stored per key:
the credential id, COSE public key, sign count, AAGUID, transports,
backup flags, name and both timestamps (`passkeys` table, migration
`0003`).

If a sign-in's counter and the stored counter are both non-zero and
the new one is not larger, the sign-in is rejected and a `possible
cloned passkey` warning is logged with a credential id prefix only.
Synced keys that always report 0 are unaffected.

Errors are fixed, plain strings and never echo client data. Register
failures are 400, never 401 (the session is fine); failed assertions
are 401 and count against the shared per-IP limiter.

### Routes

| Route | Auth | Behaviour |
|---|---|---|
| `GET /auth/passkeys/status` | public | `{"enabled":true}`, or `{"enabled":false,"reason":"…"}` |
| `POST /auth/passkeys/register/begin` | session + CSRF | 200 creation options + ceremony cookie |
| `POST /auth/passkeys/register/finish` | session + CSRF | credential JSON with optional `{name}` → 201 |
| `POST /auth/passkeys/login/begin` | public + CSRF | 200 request options + ceremony cookie |
| `POST /auth/passkeys/login/finish` | public + CSRF | 204 + session cookie, or 401 / 429 |
| `GET /api/passkeys` | session | `{"passkeys":[{id,name,created,lastUsed,backedUp}]}` |
| `PATCH /api/passkeys/{id}` | session + CSRF | `{name}` → 204 (404 unknown) |
| `DELETE /api/passkeys/{id}` | session + CSRF | 204, idempotent |

The sign-in screen shows `Sign in with a passkey` only when the
status says enabled, the browser speaks WebAuthn, and the page is not
open at an IP address (WebAuthn refuses those even when serve has a
name; Settings says so instead of offering Add). Conditional
mediation (`autocomplete="username webauthn"`) runs where supported
and stays quiet until the user picks a passkey. The accent stays on
the one primary action. A cancel shows nothing; a failure says
`That passkey didn't work. Try again or use your password.`

## Routes

| Route | Auth | Behaviour |
|---|---|---|
| `GET /healthz`, `GET /readyz` | public | as before |
| `POST /auth/sign-in` | public | 204 + cookie, or 401 / 429 JSON (the React sign-in screen calls it) |
| `POST /auth/sign-out` | session | deletes the session, clears the cookie |
| `GET /auth/me` | session | `{"username":"admin"}` |
| `/auth/passkeys/*`, `/api/passkeys*` | see above | passkey ceremonies and management |
| anything else | session (401) | 404 with a session |

## Secure cookies on plain-http loopback

The cookie is `Secure` even though serve speaks plain HTTP on
`127.0.0.1:7659`. Chromium keeps `Secure` cookies on loopback, but
WebKit drops them: CI proved it (both WebKit sign-in projects failed
with the cookie absent while Chromium held it), so the suite runs
Chromium-only until real HTTPS or the M1.2 UI revisits it. There is
no insecure-cookie option.

## Tests and smoke

- Unit: `internal/auth` (bootstrap, failures, limiter window,
  both timeouts to the second, store-down paths; passkey ceremonies
  against the `authtest` software authenticator, the argon cap and
  the failures-map bound) and `internal/api` (routes, cookies, CSRF
  matrix, trusted origins, passkey round-trips).
- e2e (`e2e/`): sign-in success and wrong password at 390 px and
  desktop on Chromium, against the compose stack; the passkeys spec
  registers, signs in and removes a key through a CDP virtual
  authenticator, with `DEVCULES_PUBLIC_URL=http://localhost:7659`
  and its own `localhost` base URL.
- `scripts/smoke.sh` signs in with curl and a cookie jar (`/auth/me`
  200), signs out (`/auth/me` 401), and checks the passkey status
  reports disabled without a public URL and enabled with a localhost
  one. Both it and the e2e suite read the bootstrap password from
  the serve logs via `scripts/get-admin-password.sh`, which only
  works on a fresh volume: reset with
  `docker compose -f deploy/compose.yaml down -v`.
