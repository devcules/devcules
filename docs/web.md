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
(`min-h-11`). The installed app draws under the iPhone status bar
(`black-translucent`), so screens start below
`env(safe-area-inset-top)`, and the phone tab bar clears the home
indicator with `env(safe-area-inset-bottom)`.

`web/src/theme/contrast.test.ts` computes WCAG contrast for every
text/background pair in both themes from this file and fails below
AA (4.5:1). Change a colour and the test judges it.

## Adding a screen

1. Add the component under `web/src/screens/` (plain words, short
   labels, every control labelled, keyboard-reachable).
2. Register its tab in `web/src/shell/Shell.tsx`, or link it from an
   existing screen. New data goes through `web/src/api.ts`; route its
   401s back to sign-in the way `me()` does in `app.tsx`.
3. Cover it in Vitest (behaviour, not snapshots) and, if it is a key
   screen, in the visual suite below.

CSP constraints (the server sends
`default-src 'self'; style-src 'self' 'unsafe-inline'; base-uri
'none'; form-action 'self'; frame-ancestors 'none'`): no inline
scripts, no `data:` URIs — `assetsInlineLimit` is 0 so the build
emits files only. The single `style-src` exception is xterm.js
(M1.4): the terminal renderer sets style attributes and a `<style>`
element at runtime, proven by `grep setAttribute("style")` over its
bundle and watched at runtime by the CSP assertion in
`e2e/tests/terminals.spec.js`. `script-src` stays `default-src
'self'`: no inline script, ever. If a feature needs a new source,
change the policy in `internal/api` first and say why in the PR.

The Terminals tab (`web/src/screens/Terminals.tsx`) lists the
runner's shells, opens one per `TerminalView` (xterm.js plus the
`FitAddon`), and closes with a confirm. `TerminalView` forwards
keystrokes as binary frames (never text: the server reads text as
resize controls), applies the sticky Ctrl to the next letter, and
reattaches on close with backoff — 1012 waits for the runner, 1013
reattaches at once, 4404 stops. The key strip (`KeyStrip.tsx`)
rides above the software keyboard through `visualViewport`. Every
`/api` 401 calls `onSessionExpired`, which unmounts the shell back
to sign-in.

## Building

```sh
npm --prefix web ci
npm --prefix web run build   # → internal/web/dist (gitignored)
```

The build keeps the committed `internal/web/dist/.gitkeep`: Vite
empties the folder, and the `keep-placeholder` plugin in
`web/vite.config.ts` writes the file back, so a local build never
stages its deletion. Without that file, `go build` fails on a fresh
clone.

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
