# Contributing to Devcules

## One module per pull request

Devcules is built module by module (see `PLAN.md` §9). Each PR builds
exactly one module and contains:

1. The package with its `doc.go`, saying what the module does and
   doesn't do.
2. Unit tests for its behaviour (plus the shared contract test when the
   module implements an interface).
3. Any integration or smoke steps it adds to `scripts/smoke.sh`.
4. Docs and a `CHANGELOG.md` entry.
5. A PR description holding the module's **Verify** commands and their
   pasted output.

## DCO sign-off

Every commit carries a Developer Certificate of Origin sign-off. Write
commits with `git commit -s` so each one ends with a
`Signed-off-by: Name <email>` line. History is linear: PRs are squash
merged.

## Ground rules

- Write everything from scratch. Ideas are fine, code is not.
- Use well-known permissive libraries only (MIT, BSD, ISC or
  Apache-2.0). Never add a GPL, AGPL or SSPL dependency, and list every
  runtime dependency's licence in `THIRD_PARTY_NOTICES.md`.
- No secrets in the repo: no keys, tokens, real hostnames, personal
  emails or screenshots of real machines. CI runs Gitleaks on every
  push.
- Nothing merges to `main` unless the pipeline is green
  (see `docs/ci.md`).
