# Devcules

> **Agents lift heavy. You stay in control.**

Devcules is an open-source, self-hosted control room for AI agents. Run
Muse Code, Claude Code or Codex on your own server. Let them drive real
computers and browsers, and approve every risky step from your phone.
Security first: secrets are redacted at the source, and access goes over
Tailscale.

## Quick start (coming soon)

Devcules is being built module by module, starting with the CI/CD
pipeline (M0.1). There is no runnable binary yet.

The front door will be a single command:

```sh
docker compose -f deploy/compose.yaml up
```

Watch [CHANGELOG.md](CHANGELOG.md) and the
[milestone issues](https://github.com/devcules/devcules/milestones) for
progress. The full plan lives in [PLAN.md](PLAN.md).

## How it is built

One module per pull request, each with tests, `go vet` and a dated
`CHANGELOG.md` entry. See [CONTRIBUTING.md](CONTRIBUTING.md) and the
CI setup in [docs/ci.md](docs/ci.md).

## Licence

Apache-2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

---

Built with Muse Code; planning and review with other AI tools.
