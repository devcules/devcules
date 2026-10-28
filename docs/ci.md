# CI/CD

Pipelines follow `PLAN.md` §8. Every action is pinned to a full commit
SHA (the tag sits in a trailing comment); Dependabot keeps the pins
fresh. Tools installed with `go install` or `go run` are version-pinned
instead (gitleaks v8.30.1, go-licenses v2.0.1, govulncheck v1.9.0,
staticcheck 2026.2.2): the Go module proxy makes those builds
reproducible. Workflows cancel superseded runs via a per-branch
concurrency group, except `cd` and `release`, which must never cancel
halfway through a publish. Every job has a 15-minute timeout. Checkouts
use `persist-credentials: false` (no job pushes via git) and run with
`contents: read` except where noted.

## ci.yml — every pull request (and every push to `main`)

| Job | What it checks | Fails when |
|---|---|---|
| `lint` | `gofmt -l`, `go vet`, `staticcheck`, plus `eslint` and `tsc --noEmit` once `web/package.json` exists | any finding |
| `arch` | `TestModuleBoundaries`: `internal/` never imports `plugins/`; the runner role never depends on `vault`, directly or transitively | a forbidden import or dependency |
| `unit` | `go test -race -cover ./...` with a 70%-per-package coverage gate, plus `vitest run` once `web/package.json` exists, plus a 30 s `FuzzDecode` run over `internal/link` (M1.3) | a failing test, a package below 70%, or a fuzz crash |
| `integration` | `docker compose up` plus `scripts/smoke.sh`, once `deploy/compose.yaml` exists | any smoke step |
| `e2e` | Playwright sign-in (success and wrong password) at phone (390 px) and desktop sizes on Chromium, against the compose stack | a failing flow or a dropped session cookie |
| `security` | Gitleaks, `govulncheck`, `npm audit --omit=dev` (once `web/` exists), and a `go-licenses check` allow-list (MIT, BSD, ISC and Apache-2.0 only) | a secret, a known vulnerability or a disallowed licence |

Note: the coverage gate checks every package, which is stricter than
§8's "touched package" wording — on an empty tree it logs a skip until
the first package with coverable statements lands.

Since M1.2 the web checks run for real: `eslint`, `tsc --noEmit`
and `vitest run` (with a 70% coverage threshold) in `lint`/`unit`,
`npm audit --omit=dev` plus a `license-checker` production allowlist
in `security`.

Since M0.5 the integration job really runs: `docker compose up -d
--wait` followed by `scripts/smoke.sh` (health endpoints, non-root
user, read-only rootfs, loopback-only port, SIGTERM drain), then
`down -v` on every exit path. The CD image build and the release
binaries run too (`Dockerfile` and `cmd/devcules` both exist).
Since M1.3 smoke also checks the link: `/readyz` reaches
`runners_linked` 1, the token file is `0400`/uid 10001, and readyz
returns to 1 after a serve restart (runner relink).

Since M1.1 the e2e job really runs: `docker compose up -d --wait`,
then the Playwright suite with the bootstrap password read from the
serve logs (`scripts/get-admin-password.sh`), then `down -v`.
Since M1.2 it runs the flow suite plus the visual suite, which
compares screenshot baselines (sign-in screen and Home shell, dark
and light, at both sizes). Baselines come from the manual
`e2e-baselines` workflow (Linux only — Mac fonts differ — via
`--update-snapshots` plus an artifact upload); see `docs/web.md`.
WebKit dropped the `Secure` cookie on plain-http loopback
(CI-proven), so flow projects are Chromium-only, with the finding
noted in `docs/auth.md`. There is no insecure-cookie option.

## cd.yml — merge to `main`

Builds a multi-arch image (amd64, arm64), pushes
`ghcr.io/devcules/devcules:edge` plus a `sha-<commit>` tag, generates an
SBOM (Syft), signs the image digest keylessly (cosign, GitHub OIDC) and
attaches the SBOM as an `spdxjson` attestation. Needs `packages: write`
and `id-token: write` on top of the default read access.

## release.yml — a `v*` tag

GoReleaser builds the `devcules` binaries for macOS, Linux and Windows,
with checksums; the workflow publishes the `:vX.Y.Z` and `:latest`
images and creates the GitHub release with notes taken from the matching
`CHANGELOG.md` section (placeholder text until per-version sections
exist). Needs `contents: write` and `packages: write` on the job only.
The install scripts will verify the checksums once they land with the
install work.

## Branch protection to turn on after merge

On `main`:

- Require status checks `lint`, `arch`, `unit`, `integration`,
  `e2e` and `security`, with "require branches to be up to date"
  enabled.
- Squash merging only (linear history); block force pushes and branch
  deletion.
- Require a pull request before merging (including for admins).
- Enforce DCO sign-off (via the DCO GitHub App).
- Optionally require conversation resolution before merging.

Also outstanding repo settings (not code): enable private
vulnerability reporting (Settings → Code security), and consider
requiring actions pinned to full-length commit SHAs.

## Local verify

```sh
gofmt -l . && go vet ./... && go test -race ./...
npm --prefix web run lint && npm --prefix web run typecheck
npm --prefix web test
actionlint
gitleaks detect --source . --verbose
```
