# e2e

Playwright sign-in flows (PLAN.md §8). No screenshot baselines yet:
those wait for the M1.2 React UI. Until then the suite asserts flows
and cookies at phone (390 px) and desktop sizes, on Chromium
(WebKit drops `Secure` cookies on plain-http loopback — see
`docs/auth.md` — so it is out until HTTPS).

## Run

Against an already-up stack (compose, or a local `devcules serve`
on 127.0.0.1:7659):

```sh
npm --prefix e2e ci
npx --prefix e2e playwright install chromium webkit
E2E_ADMIN_PASSWORD="$(./scripts/get-admin-password.sh)" npm --prefix e2e test
```

`get-admin-password.sh` reads the bootstrap password from the serve
logs. It only exists on a fresh volume: reset a local stack with
`docker compose -f deploy/compose.yaml down -v` first.
