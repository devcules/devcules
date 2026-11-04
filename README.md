# Devcules

> **Agents lift heavy. You stay in control.**

Devcules is an open-source, self-hosted control room for AI agents. Run
Muse Code, Claude Code or Codex on your own server. Let them drive real
computers and browsers, and approve every risky step from your phone.
Security first: secrets are redacted at the source, and access goes over
Tailscale.

## Quick start

One command on your computer:

```sh
go install github.com/devcules/devcules/cmd/devcules@latest
devcules
```

That runs serve and its own host runner in one process, on
http://127.0.0.1:7659. It sets no environment variable and asks nothing:
the Projects picker sees your own folders, named **host (this
computer)**, and the first start prints a bootstrap admin password.

**In the first two minutes** you have: the web desk on the loopback,
a **Projects** tree of the machine's folders, a **Terminals** tab to
run an agent in one, and an **Assistant** tab that chats with the
setup helper (it asks before it changes anything; your phone answers).
The three-line walkthrough:

1. Open http://127.0.0.1:7659 and sign in (the bootstrap password the
   first start printed; you add a passkey once, and the phone's
   TouchID / FaceID is the sign-in after).
2. In **Providers** save your Meta key; set `META_API_KEY` once and it
   is in the vault forever (you can unset the variable).
3. Ask the Assistant "which providers do I have and which runner is
   live" — it reads the live setup through the same MCP tools agents
   use answers with the answer, and any change it proposes waits for
   your approval first.

Prefer the sandbox over the host? The full stack (serve, a runner
rooted at your `~/code`, the vault all in one volume) is one compose
up:

```sh
docker compose -f deploy/compose.yaml up -d --wait
```

And the phone reaches the desk over your tailnet, never a public port:

```sh
echo "TS_AUTHKEY=tskey-auth-…" > .env
docker compose -f deploy/compose.yaml --profile tailscale up -d --wait
# the desk is https://devcules.<your-tailnet>.ts.net
```

Prefer a sandbox, or a second machine? Both are machines you add from
the GUI: **Machines → Add a computer** — the compose stack is the
container option, and it shows up as **container (…)** so the desk
never confuses it for your own filesystem. See
[docs/deploy.md](docs/deploy.md) for environment, hardening and
limits. The full plan lives in [PLAN.md](PLAN.md).

## What is new in the Meta hackathon window

Devcules was entered in the Meta Global AI Developer Hackathon, hosted
by Meta and Cerebral Valley (remote). The event's published window ran from
2026-10-26 to 2026-11-04, with submissions due 2026-11-05 03:00 CET
(2026-11-04 9:00 PM ET); see
[dev.meta.ai/events/global-hackathon](https://dev.meta.ai/events/global-hackathon).
For judging, the work splits along one git boundary:

- **Devcules base: `main` up to 6ba67eb** (2026-11-04 01:14 CET). This
  is everything the README above describes: serve, runners, machines,
  terminals, agents, providers, approvals, redaction, the vault, the
  Desk and the rest. Its `CHANGELOG.md` entries are dated from
  2026-10-26 (M0.1) to 2026-11-04, and its git history starts on
  2026-10-30 with an initial import (13a796e), so the base was also
  built inside the window.
- **New on the `vibe-glowup` branch** (forked from 6ba67eb; all commits
  dated 2026-11-04): the vibe and four showcase themes with bundled
  fonts, the command palette and keyboard shortcuts, presentation mode,
  the Desk showcase tiles, the mobile terminal key strip and phone
  fullscreen, a handful of server bug fixes (7996276), and the fixes
  from an internal bug bounty against that work, merged batch by batch.
  The `CHANGELOG.md` entries dated 2026-11-04 headed "vibe-glowup" list
  all of it, with finding IDs; open items are in
  [docs/known-follow-ups.md](docs/known-follow-ups.md).

Commits on the branch carry the git author `Hark`, the AI assistant
used for this work. `git log main..vibe-glowup` shows exactly what the
branch adds.

## How it is built

One module per pull request, each with tests, `go vet` and a dated
`CHANGELOG.md` entry. See [CONTRIBUTING.md](CONTRIBUTING.md) and the
CI setup in [docs/ci.md](docs/ci.md).

## Licence

Apache-2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

---

Built with Muse Code; planning and review with other AI tools.
