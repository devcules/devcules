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
(`text-body`, `text-lead`), and one accent per theme reserved for
primary actions and "needs you". The default theme is **vibe** (dark),
whatever the OS colour scheme says; see Themes below. Motion is ≤150 ms and dies
under `prefers-reduced-motion`; touch targets are ≥44 px
(`min-h-11`). The installed app draws under the iPhone status bar
(`black-translucent`), so screens start below
`env(safe-area-inset-top)`, and the phone tab bar clears the home
indicator with `env(safe-area-inset-bottom)`.

`web/src/theme/contrast.test.ts` computes WCAG contrast for every
text/background pair in every theme from this file and fails below
AA (4.5:1). Change a colour and the test judges it.

## Themes

The theme picker sits in the header (top right): a button showing the
current theme's accent swatch that opens a list of every theme. The
list opens on the current theme; ↑/↓, Home and End move, Enter picks,
Escape or a click outside closes it and returns focus to the button.
The choice is stored per browser in `localStorage` under
`devcules.theme` and applied before the first paint (`initTheme()` in
`web/src/main.tsx`). The command palette offers the same list as
"Theme: …" commands.

`web/src/theme/themes.ts` is the registry (id, label, scheme, three
swatches); each theme's colour tokens are one `:root[data-theme=…]`
block in `tokens.css` (arc, the old default, is the plain `:root`
block and its OS-light pair). The five showcase themes from the vibe-glowup
branch are listed first:

| Id | Label | Scheme | Extra styling |
|---|---|---|---|
| `vibe` | vibe ✦ | dark | the default; glow, gradients and type in `theme/vibe.css` |
| `tron` | gridrunner ◈ | dark | `theme/future.css` |
| `ember` | ember 2049 ▲ | dark | `theme/future.css` |
| `phosphor` | phosphor ▮ | dark | `theme/future.css` |
| `holo` | holo ◎ | light | `theme/future.css` |

The older palettes follow: arc, slate, midnight, IASF
(`meta-hackathon`), nord, dracula, gruvbox, tokyo night, solarized,
catppuccin, rosé pine, everforest and monokai (dark), and paper,
flexoki light and solarized light (light).

`applyTheme()` sets `data-theme`, `data-scheme` (`dark` or `light`)
and `color-scheme` on `<html>`, and the `theme-color` meta tag. Under
`data-scheme="light"`, `tokens.css` repoints the dark-tuned status
utilities (`red-300`/`red-400`, `amber-400`/`amber-500` and the vibe
hot pink) at each light theme's AA `--c-danger`, `--c-warn` and
`--c-hot` tokens.

**Default and OS light mode (BUILD-04, documented, not changed).**
Before the vibe-glowup branch the default was arc, whose colours follow
`prefers-color-scheme` (dark, or its light pair on an OS in light
mode). The default is now `vibe`, which is dark on every OS: a browser
with no stored choice gets the dark vibe UI even when the OS is in
light mode. A light UI is one pick away (holo, paper, flexoki light,
solarized light), or choose arc to follow the OS again. This is
deliberate; there is no automatic light default. It also means the
`desktop-light` and `mobile-light` visual projects (which emulate an
OS light scheme) render the dark vibe theme too.

Fonts: the vibe and showcase themes use six bundled fonts (Space
Grotesk, Unbounded, JetBrains Mono, Orbitron, Chakra Petch, VT323)
from `@fontsource` packages imported in `web/src/main.tsx`. They are
files in the build (no font CDN; CSP `font-src 'self'` covers them),
under the SIL Open Font License 1.1; see `THIRD_PARTY_NOTICES.md` and
`/licenses/OFL-1.1.txt` in the running app.

## Command palette and shortcuts

`web/src/shell/CommandPalette.tsx`, mounted in the header. Ctrl+K
(⌘K on a Mac) or `/` opens it; type to filter (substring, then
in-order letters, on the label), ↑/↓ to select (the selected row is
kept scrolled into view), Enter to run, Escape to close. It is a modal
dialog (`shell/Modal.tsx`): Tab stays inside it, a backdrop click
closes it, and focus returns to where it was. The input is a combobox
wired to the result list (`aria-controls`, `aria-activedescendant`), so
a screen reader follows the selection. The header button and the
shortcut sheet show ⌘K on Apple devices and Ctrl+K everywhere else.
Commands:

- Navigate: "Go to <tab>" for every tab.
- Demo: start or exit presentation mode; "Celebrate 🎉" (confetti).
- Desk: refresh every tile; add a widget; arrange tiles; sources
  (calendar, news, tickets). Each one switches to the Desk first. Run
  from another screen, the command is held until the Desk mounts and
  runs then, once, if that is within 2 s; "add a widget" waits until
  the Desk has loaded, and is dropped if the load fails.
- Theme: one command per theme.
- Help: the keyboard shortcut sheet.

Global single-key shortcuts (ignored while typing in a field or a
terminal, while the palette or the shortcut sheet is open, or with
Ctrl, ⌘ or Alt held). While any other dialog is open (anything marked
`aria-modal`, such as the Desk's add, configure, sources and event
sheets) or a header popup is expanded (the theme picker, the user
menu), every global key is left alone, Ctrl+K and Escape included:
that dialog's own Escape closes only it, and presentation mode stays
on behind it.

| Key | Does |
|---|---|
| Ctrl+K / ⌘K, `/` | open the command palette |
| `?` | show or hide the shortcut sheet |
| `p` | toggle presentation mode |
| `r` | refresh the Desk (every tile, the calendar included) |
| `g` then a letter within 1.2 s | go to a tab: `d` Desk, `h` Home, `j` Projects, `a` Assistant, `m` Machines, `e` Extensions, `v` Activity, `s` Settings. The key after `g` is always taken by the chord (`g p` never toggles presentation); another `g` re-arms it |
| Escape | close the palette, then the sheet, then leave presentation mode |

The shortcut sheet is a modal dialog with a ✕ close button; it takes
focus when it opens, and Escape or ✕ closes it. Ctrl+K swaps it for
the palette.

## Presentation mode

`web/src/shell/present.ts`. The `p` key, the header's **▶ present**
button (desktop) or the palette toggles it. It adds the
`devcules-present` class to `<html>`: the header, sidebar, tab bar and
status bar fold away, the current screen takes the whole viewport, and
a small LIVE badge shows. Turning it on also asks the browser for real
fullscreen (best effort); Escape or the palette turns it off.

Presentation mode remembers whether it entered that fullscreen
itself. Leaving it with the browser's own Escape or exit control then
also leaves presentation mode; a fullscreen something else started
(a video, say) does not end the demo when it closes.

When the pending-approvals count drops from some to zero (inbox
zero), a confetti burst plays (`shell/Confetti.tsx`, a click-through
canvas).

## Desk showcase tiles

The vibe-glowup branch adds four Desk tiles
(`web/src/screens/desk/showcaseTiles.tsx`). Three read the same APIs as
the rest of the app, with nothing mocked: **vibe check** (terminals
live, machines online, approvals waiting, models ready), **agent feed**
(the audit log as a timeline) and **pulse · 24h** (audit events per hour
over the last 24 hours). With the server unreachable these three keep
their last good values and show an error state, not zeros.

**ship clock** calls no API. It is a countdown ring computed in the
browser, ticking once a second, from a start to a deadline. With
nothing set, it runs over the hackathon's window, from
`2026-10-26T18:00:00+01:00` to `2026-11-05T03:00:00+01:00` (Mon 18:00
CET to Thu 03:00 CET). The tile's settings set a new deadline, which is
saved in the widget's config with the Desk layout. If the deadline in
its config cannot be read as a date, the clock shows `--:--:--` with
"set a deadline" and "pick one in settings" instead of a countdown. A
start that cannot be read leaves the countdown running, with the ring
at 0%. Its LED reads live, or off while no valid deadline is set. In
the settings form, an empty value closes it without saving; a value
the browser's date field accepts but JavaScript cannot hold as a date
(such as year 275760 west of UTC) keeps the form open with "that date
can't be used", and nothing is saved.

## Phone chrome

The phone layout follows one JS flag, `useMobile()`
(`web/src/screens/useMobile.ts`): a coarse pointer, at least one touch
point, and a phone-sized edge (width ≤ 820 px or height ≤ 500 px). It
is mirrored as the `devcules-mobile` class on `<html>` and `<body>`.
Tailwind's `md` breakpoint does not decide it, so a phone in landscape
(844×390) still gets the phone chrome:

- No desktop sidebar or status bar; a bottom tab bar instead, which
  hides while the software keyboard is up or a terminal is fullscreen.
- The shell is a fixed box sized to `visualViewport` height. The
  browser's own pan to reveal a field is undone; instead, when the
  visible height shrinks, the focused input, textarea, select or
  editable element in the page is scrolled into view (the terminal's
  own textarea excepted: its pane refits itself).
- Safe areas: the side insets (landscape notch) always; the top inset
  under the header, or on the shell in terminal fullscreen; the bottom
  inset (home indicator) under the tab bar, or on the shell in
  fullscreen.
- Terminal fullscreen: opening a terminal pane on a phone folds the
  header and tab bar so the terminal takes the visible screen. The
  strip's ☰ (or leaving the pane) brings them back. See
  `docs/terminals.md` for the key strip.

An `ErrorBoundary` (`web/src/shell/ErrorBoundary.tsx`) wraps the app
and each Desk tile, so a render error shows a fallback instead of a
blank page: a **reload** button for the app, and "this
tile crashed · reload" for a tile while the rest of the Desk keeps
working.

## Adding a screen

1. Add the component under `web/src/screens/` (plain words, short
   labels, every control labelled, keyboard-reachable).
2. Register its tab in `web/src/shell/Shell.tsx`, or link it from an
   existing screen. New data goes through `web/src/api.ts`; route its
   401s back to sign-in the way `me()` does in `app.tsx`.
3. Cover it in Vitest (behaviour, not snapshots) and, if it is a key
   screen, in the visual suite below.

CSP constraints (the server sends
`default-src 'self'; style-src 'self' 'unsafe-inline'; font-src
'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors
'none'`): no inline scripts, no `data:` URIs in our own build —
`assetsInlineLimit` is 0 so it emits files only. `font-src` allows
`data:` for one library: FullCalendar (the Desk calendar) declares
its icon font inline, and the browser decodes that font, and refuses
it, on every page even when nothing draws an icon. A font cannot run
script. The single `style-src` exception is xterm.js
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
reattaches at once, 1000 and 4404 stop. On a phone the custom key strip
(`TermKeyboard.tsx`, bound to the live pane through `keybus.ts`) docks
at the top of the open pane; see `docs/terminals.md`. Every
`/api` 401 calls `onSessionExpired`, which unmounts the shell back
to sign-in.

Passkeys (M1.5) need no library: `web/src/webauthn.ts` holds the
base64url helpers and the conversions between the server's JSON and
the `navigator.credentials` shapes. The sign-in screen offers `Sign
in with a passkey` only when `/auth/passkeys/status` says enabled
and the browser speaks WebAuthn, and never at an IP address
(`passkeysUsableHere`), with conditional mediation
(`autocomplete="username webauthn"`) where supported; the accent
stays on the one primary action. Settings lists the keys (rename,
remove with a confirm), and says why Add is missing when passkeys
are off or the page is at an IP address. CSP is unchanged.

Redaction (M2.2) is its own section in Settings
(`web/src/screens/Redaction.tsx`): a native select per type (saving
the whole policy on change and reverting if the save fails), the
counters since serve started, and a test box that shows the masked
text, or why it was blocked. Values never come back from the server.

The Activity tab (M2.4, `web/src/screens/Activity.tsx`) has two
sections. Model calls lists one row per agent launch (label, calls,
tokens, redactions); opening a row loads that launch's calls with the
question, the reply and each tool call as the provider saw them, so
masked values show as placeholders. The audit log lists entries
newest first, 50 at a time, with "Check the log" (the server walks the
hash chain) and an export link for the JSON-lines file. The flow is
covered by `e2e/tests/activity.spec.js`; it has no visual baseline,
because its rows are timestamps and addresses.

Approvals (M3.2) live on Home (`web/src/screens/Approvals.tsx`): the
shell polls `/api/approvals?status=pending` every 5 seconds while the
page is visible, and one poll feeds both the inbox and the count on
the Home tab, the one place the nav uses the accent. Each card shows
the rule, the agent and runner, the masked command and when it will
be denied, with **Deny** and **Allow once**; the calm "Nothing needs
you." stays when nothing waits, so the visual baselines hold.
Activity lists answered approvals. Settings gains Notifications
(`Notifications.tsx`: subscribe this device, list, remove, send a
test) and Access modes (`AccessModes.tsx`: a mode per runner, the
critical rules, and a box to check a command). `web/public/sw.js` is
the service worker: it shows push notices and routes a tap to
`/?approval=<id>` (or posts the id to an open window), and caches
nothing. `main.tsx` registers it in secure contexts. CSP is unchanged:
`default-src 'self'` covers the worker. Covered by
`e2e/tests/approvals.spec.js` (hook asks, Home allows, the hook gets
the answer); the flow specs wait for the nav, not Home's text, so a
waiting approval from a parallel spec can't break them.

Settings → Providers (`web/src/screens/Providers.tsx`) lists the
provider profiles as cards: the name, a format chip, whether a key is
set, the base URL and default model, with Make default, Edit and ✕
(with a confirm). Below them the add/edit form: presets for Meta,
Anthropic, OpenAI and any OpenAI-compatible endpoint (vLLM, Nebius…),
name, format, base URL, Authentication (API key only for now), which
OpenAI API agents use, "also serves Anthropic's /v1/messages", the key
(a password field, blank on an edit to keep the saved one), and the
default model with **Load models**, which asks serve to probe the
unsaved form (`docs/providers.md`). The new-terminal form shows each
profile an agent can use by name, with its format, the default marked,
and the profile's default model as the blank choice.

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
