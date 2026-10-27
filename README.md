# Devcules

> **Agents lift heavy. You stay in control.**

Devcules is an open-source, self-hosted control room for AI agents. Run
Muse Code, Claude Code or Codex on your own server. Let them drive real
computers and browsers, and approve every risky step from your phone.
Security first: secrets are redacted at the source, and access goes over
Tailscale.

## Quick start

Prerequisites: Docker with the Compose plugin. From the repo root:

```sh
docker compose -f deploy/compose.yaml up -d --wait
./scripts/smoke.sh
```

That starts `serve` on `127.0.0.1:7659` and an idle `runner`, then
proves both are healthy. See [docs/deploy.md](docs/deploy.md) for
environment, hardening and limits. The full plan lives in
[PLAN.md](PLAN.md).

## How it is built

One module per pull request, each with tests, `go vet` and a dated
`CHANGELOG.md` entry. See [CONTRIBUTING.md](CONTRIBUTING.md) and the
CI setup in [docs/ci.md](docs/ci.md).

## Licence

Apache-2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

---

Built with Muse Code; planning and review with other AI tools.
