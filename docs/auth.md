# Auth

M1.1 owns password sign-in: admin bootstrap, cookie sessions and
CSRF. Passkeys (M1.5), API tokens and audit events (M2.4) are later
modules and are not stubbed anywhere here.

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
`password`) and answers 303 to `/` with the session cookie. A wrong
password and an unknown user share one 401 (`invalid username or
password`), compared in constant time after one argon2id
verification, so logins cannot be enumerated by message or timing.

Past 5 failures per minute per client IP (the connection's remote
address; no proxy headers are trusted) sign-in answers 429 with a
`Retry-After: 60`. A success clears the IP's count, so mistyping
never locks anyone out.

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
origin by scheme plus host, so the later browser UI can post from its
own origin. Requests without `Origin` or `Sec-Fetch-Site` (curl,
non-browser clients) are allowed: there is nothing to compare.

## Routes

| Route | Auth | Behaviour |
|---|---|---|
| `GET /healthz`, `GET /readyz` | public | as before |
| `GET /sign-in` | public | no-JS login form (replaced by the M1.2 React UI) |
| `POST /auth/sign-in` | public | 303 + cookie, or 401 / 429 JSON |
| `POST /auth/sign-out` | session | deletes the session, clears the cookie |
| `GET /auth/me` | session | `{"username":"admin"}` |
| anything else | session (401) | 404 with a session (the shell is not here yet) |

## Secure cookies on plain-http loopback

The cookie is `Secure` even though serve speaks plain HTTP on
`127.0.0.1:7659`. Browsers treat loopback as a trustworthy origin,
so they should keep and send `Secure` cookies there; the Playwright
suite checks it by asserting the cookie survives sign-in on Chromium
and WebKit. If a browser drops it, that browser's project is removed
from `e2e/playwright.config.js` and noted here: no
insecure-cookie option will be added.

## Tests and smoke

- Unit: `internal/auth` (bootstrap, failures, limiter window,
  both timeouts to the second, store-down paths) and `internal/api`
  (routes, cookies, CSRF matrix, trusted origins).
- e2e (`e2e/`): sign-in success and wrong password at 390 px and
  desktop, on Chromium and WebKit, against the compose stack.
- `scripts/smoke.sh` signs in with curl and a cookie jar (`/auth/me`
  200), signs out (`/auth/me` 401). Both it and the e2e suite read
  the bootstrap password from the serve logs via
  `scripts/get-admin-password.sh`, which only works on a fresh
  volume: reset with `docker compose -f deploy/compose.yaml down -v`.
