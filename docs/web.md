# Web

The PWA shell (`web/`, React 18 + TypeScript strict + Vite +
Tailwind v4) embeds into the binary via `internal/web` (go:embed).
M1.2 ships the theme system, the app shell, sign-in, and
installability. Terminals (M1.4), passkeys (M1.5), the service worker
and push (M3.2), and the agent board (M3) are later modules and are
not stubbed here.

## Tokens

`web/src/theme/tokens.css` is the one visual system (PLAN.md §1a):
colour tokens as CSS variables that Tailwind reads through `@theme`,
a 4 px spacing scale (Tailwind's default), two body text sizes
(`text-body`, `text-lead`), and one amber accent reserved for primary
actions and "needs you". Dark is the default; light follows
`prefers-color-scheme` (no toggle yet). Motion is ≤150 ms and dies
under `prefers-reduced-motion`; touch targets are ≥44 px
(`min-h-11`); the phone tab bar clears the home indicator with
`env(safe-area-inset-bottom)`.

`web/src/theme/contrast.test.ts` computes WCAG contrast for every
text/background pair in both themes from this file and fails below
AA (4.5:1). Change a colour and the test judges it.

## Adding a screen

1. Add the component under `web/src/screens/` (plain words, short
   labels, every control labelled, keyboard-reachable).
2. Register its tab in `web/src/shell/Shell.tsx`, or link it from an
   existing screen. New data goes through `web/src/api.ts`, which
   returns the app to sign-in on any 401.
3. Cover it in Vitest (behaviour, not snapshots) and, if it is a key
   screen, in the visual suite below.

CSP constraints (the server sends
`default-src 'self'; frame-ancestors 'none'`): no inline scripts or
styles, no `data:` URIs — `assetsInlineLimit` is 0 so the build
emits files only. If a feature needs a new source, change the policy
in `internal/api` first and say why in the PR.

## Building

```sh
npm --prefix web ci
npm --prefix web run build   # → internal/web/dist (gitignored)
```

Serve reads the embedded build; without one it answers 503 with the
command above. The Docker image builds the app in its Node stage, so
compose and CI always test the real bundle. Regenerate icons with
`go run web/icons/generate.go` (same geometry as
`web/public/icons/icon.svg`).

## Baseline workflow

Screenshot baselines live next to the visual spec
(`e2e/tests/visual.spec.js-snapshots/`, `-linux` files only).
Linux renders the fonts CI compares against, so baselines must never
come from a Mac:

1. Push the branch, then run the `e2e-baselines` workflow
   (Actions → e2e-baselines → Run workflow → your branch). It boots
   compose, runs the visual suite with `--update-snapshots`, and
   uploads the PNGs as the `e2e-baselines` artifact.
2. Download the artifact, copy the PNGs into
   `e2e/tests/visual.spec.js-snapshots/`, commit them on the branch.
3. The CI `e2e` job compares from then on: an unexpected pixel
   fails the run.

To re-shoot after an intentional visual change, repeat from step 1.
