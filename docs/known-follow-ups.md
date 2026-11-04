# V5 merge review

## Review outcome

**Merged to `main` after manual testing at the existing tailnet instance.**
The supplied archive was based on `6ba67eb`, so it was compared against
the complete repository instead of replacing the latest checkout. Its
newer README site and Pages workflow are retained, as is the sidebar
resizing behavior.

Useful tested changes include bounded sign-in throttling, safer registry
process cleanup and audit redaction, slow-HTTP responses, terminal
heartbeat and control-message ordering, local Desk widgets, and theme
and shell improvements. New regression tests account for much of the
large diff.

Review correction: always reap a killed MCP child even if `exec` has
already reaped or marked it after a cancellation; Close itself stays
idempotent and kills its process group once.

## Verification

- Locally: Go tests and vet, frontend tests (46 files / 734 tests),
  lint/typecheck/build, full-history Gitleaks and the font licence check.
- CI: Linux unit, fuzz, coverage, vulnerability/scanner, static
  analysis, multi-arch Docker image, functional browser flows and Linux
  visual baselines. One mobile WebAuthn lifecycle case remains skipped;
  an external passkey ceremony still requires manual browser testing.
- One existing `TerminalView.tsx` hook-dependency warning remains.

## Merge considerations

- Some candidate tests target a different version of interfaces than
  `main`; ensure Git preserves all their matching package changes when
  reviewing/picking this change.
- CI full-race failures in machine/file/terminal routes exposed a shared
  tmux test server: `-L devcules` ignores TMUX_TMPDIR, so tests with
  different directories still killed one another's sessions. Test
  managers now get explicit private socket paths (`-S`); production
  retains the normal `devcules` socket namespace (`-L`). Attach/detach lifecycle races are
  separately tracked below and are not claimed fixed by this isolation.
  Stream-writer cleanup is asynchronous: its regression test waits for
  the browser close and guarded stream removal rather than assuming
  those happen against the same scheduling deadline.
- MCP shutdown is best-effort: a stuck child has a two-second wait
  ceiling before the caller cancels its context.
- An earlier sandboxed frontend run leaked fake timers between toast and
  App tests; after reinstalling the isolated dependencies, the merged
  frontend suite passed in both local and Linux CI runs.

## Supplied follow-ups

Open items left by the 2026-11-04 bug bounty on the `vibe-glowup`
branch. These are known gaps, not closed by this review. IDs refer to
the findings in `CHANGELOG.md`.

## Deferred findings

| ID | Sev | What | Why it waits |
|---|---|---|---|
| GOSEC-17 | P2 | MCP install secret handling: hardening deferred. | Deferred to a later release. |
| GORT-11 | P2 | Attach and detach can race on the runner's async handlers and leave orphan tmux attach clients. | Medium confidence, from reading the code. The fix needs runner-side tombstones or serialisation in the pty manager; the risk of a new terminal regression outweighed it. |
| GORT-12 | P2 | A runner input-queue overflow (more than 256 queued input frames) ends the attach with `term.exit`, so the browser shows "gone" instead of reattaching. | Rare trigger. A proper fix adds an exit reason to `term.exit` across runner, relay and web. |
| GORT-10 (part) | – | Non-numeric `cols`/`rows` in the stream URL close with 4404 ("unknown terminal") rather than a distinct bad-request code (4400). The size clamp itself shipped. | Waits with GORT-12's protocol change. |
| MOB-12 | P2 | No way to scroll back through terminal output on a phone. | Feature work (touch scrollback through tmux copy-mode) that needs a UX design, not a regression. |
| BUILD-07 | P3 | `web/package-lock.json` churn: `libc` constraints stripped from 20+ optional native packages. | Regenerating with npm@11 needs the network and risks unrelated churn; no runtime impact found. |

BUILD-08 (an unrelated `go.mod` tidy inside a web commit) was rejected:
the tidy is correct, and splitting it would rewrite merged history.

## Follow-ups noted during review

Web:

- The theme picker stays open when focus leaves it with Tab (Escape and
  an outside click close it).
- Arc under an OS light scheme renders its light palette, but
  `applyTheme()` still sets `color-scheme: dark` and
  `data-scheme="dark"` for it, so native controls and scrollbars stay
  dark.
- Activity (and the Desk's agent feed) has no friendly labels for the
  newer audit actions: `calendar.*`, `registry.*` and `machine.input`
  show as raw action names.
- The comment above the `html` font stack in `web/src/main.css` still
  says the system font stack keeps the app offline-capable; the vibe
  themes now use bundled fonts (still local files, no CDN). Comment
  only.
- The old five-key phone strip component is no longer used: the
  `TermKeyboard` strip replaced it, and only its own test imports it.
  It can be deleted. The architecture diagram
  (`docs/diagrams/devcules-architecture.drawio`) still shows the old
  strip.
- UI polish lists from the bounty (not findings): emoji that render as
  tofu on some systems, terminal names in pane headers, and xterm
  colours that follow the theme.

Server:

- When a launch token is scoped to no provider (self-auth agents, see
  GOSEC-15), the router's 403 names the provider as `"-"`; it should
  say the launch has no provider access.
- Per-IP limits (sign-in, enrol, passkeys) see the proxy's address when
  serve runs behind Docker's port mapping or Tailscale serve. A
  trusted-proxy setting would let them use the client's address.
- Recurring timed plans are exported to ICS with `DTSTART` in UTC and
  the rule as stored, with no `TZID`/`VTIMEZONE`. A subscribing
  calendar expands the rule in UTC, so instances drift by an hour
  across DST, and a `BYDAY` rule near midnight can land on the wrong
  day (Monday 00:30 Amsterdam exports as Sunday 23:30Z with
  `BYDAY=MO`). An all-day rule's `UNTIL` is exported as a DATE-TIME
  beside a DATE `DTSTART`. All of this predates the bounty; the fix is
  `DTSTART;TZID=` plus a `VTIMEZONE` block. (Noted while reviewing B8,
  which was not merged when this was written; recheck after it lands.)
- Further network hardening of outbound fetches is planned.
