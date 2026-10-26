# Devcules: rules for coding agents

Devcules is an open-source, self-hosted control room for AI agents. `PLAN.md` is the source of truth for architecture, security, design principles and build order. Read it before any task.

## How work is organised

- **One GitHub issue = one module = one pull request.** Issues #1 to #28 follow the build order in `PLAN.md` §9. Work on one issue at a time, and only when the issue before it is merged.
- Read the issue first: `gh issue view <number>`. Its test gate is the definition of done.
- Write everything from scratch. Never copy code from other projects (see `PLAN.md` §1).

## Git workflow (always)

- **Never commit to `main`, never push to `main`, never merge, never force-push, and never rewrite pushed history.** A human reviews and merges every PR.
- **Branch names:** `users/kiran/<module>-<short-slug>`, lowercase, words joined by hyphens. Examples: `users/kiran/m0.1-ci-cd`, `users/kiran/m2.2-redact`.
- Always branch from an up-to-date `main`: `git fetch origin && git switch -c <branch> origin/main`.
- **Commits:** Conventional Commits with the module as scope and a DCO sign-off, e.g. `git commit -s -m "feat(m0.3): add store interface and sqlite migrations"`. Types: feat, fix, test, docs, ci, refactor, chore.
- Stage files explicitly (`git add <paths>`), never `git add -A` blindly. Check `git status` and `git diff --staged` before every commit.
- Never commit secrets, `.env` files, databases, build output, screenshots of real machines, or anything over 1 MB.
- Before the first commit in a session, check `git config user.email` is a personal or GitHub noreply address. If it looks like a work address, stop and ask.

## Before opening a PR

Run every check that applies, and fix failures before pushing:

- Go: `gofmt -l .` (must print nothing), `go vet ./...`, `go test -race ./...`
- Web (once `web/` exists): `npm --prefix web run lint`, `npm --prefix web test -- --run`, `npm --prefix web run build`
- Workflows changed: `actionlint`
- Secrets: `gitleaks detect --no-banner` if installed

## Pull requests

- Open with `gh pr create --base main`. Title: `<module>: <what it adds>`, e.g. `M0.3: store interface, SQLite and migrations`.
- The body uses this template:

  ```
  Closes #<issue>

  ## What
  <one paragraph: what this module adds and what it deliberately leaves out>

  ## Test gate
  - [x] <each item from the issue, ticked only if verified>

  ## Verify
  <the exact commands run and their output>

  ## Notes
  <anything not verified and why; follow-ups for later modules>

  Built with Muse Code.
  ```
- Add the same labels as the issue, and stop after the PR is open. Report the PR URL and wait for review.

## Skills in this repo

- `/start-issue <number>` reads the issue and creates the branch from a fresh `main`.
- `/ship-pr` runs the checks, commits, pushes and opens the PR, then stops.
