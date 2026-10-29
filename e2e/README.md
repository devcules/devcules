# e2e

Playwright flows against a running stack (compose in CI, or a local
`devcules serve`): sign-in, terminals, passkeys and the Activity
tab's audit log, at 390 px and
desktop on Chromium. The visual suite (`test:visual`, Linux only)
compares screenshot baselines, which the `e2e-baselines` workflow
refreshes — never generate them on a Mac.

## Run it

```sh
DEVCULES_PUBLIC_URL=http://localhost:7659 docker compose -f ../deploy/compose.yaml up -d --wait
E2E_ADMIN_PASSWORD="$(../scripts/get-admin-password.sh)" npm --prefix . test
```

The public URL matters: the passkeys spec carries its own
`http://localhost:7659` base URL because WebAuthn needs https or
`http://localhost`, never `127.0.0.1` (a CDP virtual authenticator
stands in for a phone). Headless Chromium answers the autofill
(conditional) request without a user pick, so the spec turns it off
and drives every ceremony through the button. The other flow specs
keep the default `http://127.0.0.1:7659` base URL (`E2E_BASE_URL`
overrides it) and pass with the public URL set. The visual suite
defaults to `http://localhost:7659`, so its baselines show the
passkey UI.

## Layout

- `playwright.config.js`: the flow suite (`signin`, `terminals`,
  `passkeys`, `activity`), Chromium desktop and mobile projects.
- `playwright.visual.config.js`: the screenshot suite (dark and
  light, both sizes).
- `tests/`: one spec per area, plus the committed Linux baselines
  under `tests/visual.spec.js-snapshots/`.
