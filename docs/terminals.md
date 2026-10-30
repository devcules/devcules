# Terminals (M1.4)

A browser tab with a real shell on the runner. tmux is the source of
truth; serve relays frames between the browser and the runner over
the link (`docs/link.md`); nothing is stubbed.

## Layout

- `internal/terminals`: the shared frame payloads and validation
  (ids, sizes, limits). No transport, no processes.
- `internal/terminals/pty`: the runner side. It drives one private
  tmux server (`tmux -L devcules`, config written by the runner:
  status off, history-limit 10000, escape-time 0,
  default-terminal tmux-256color, window-size latest) and pumps
  attach clients through `github.com/creack/pty` (MIT).
- `internal/terminals/relay`: the serve side. It never imports the
  pty package (`arch_test.go` enforces this); it only sends link
  frames and correlates replies.

## Frames over the link

serve → runner: `term.open {term}`, `term.attach {term, stream,
cols, rows}`, `term.input {stream, b64}`, `term.resize {stream,
cols, rows}`, `term.detach {stream}`, `term.close {term}`,
`term.list`, `term.agents`. runner → serve: `term.ok` /
`term.error {reason}` (same id as the request), `term.output
{stream, b64}`, `term.exit {stream}`, `term.listed {terms:[{term,
created, agent, project, dir, name}]}`, `term.agentlist {agents:[{id,
name, provider, providers, installed, version, install}]}`.

`term.open`, `term.attach`, `term.close` and `term.detach` answer
`term.ok` or `term.error`; `term.list` answers `term.listed` and
`term.agents` answers `term.agentlist`. `term.open` may carry
`agent: {id, token, model, provider}` to start a coding agent in the
new terminal on that router provider (`docs/agents.md`), and a place:

- `dir`: start in this directory, absolute or relative to the
  runner's workspace; clean paths only, never `..`. A missing
  directory answers `term.error "directory not found"`.
- `project`: a project id (`p_…`), stored as the tmux user option
  `@devcules_project`, so `term.listed` reports it (with the session's
  `dir`) and a serve restart loses nothing.
- `clone`: an `https://`, `ssh://` or `git@host:path` URL. With a
  `dir` that does not exist yet, the terminal runs `git clone -- <url>
  <dir>` from the parent directory and then changes into the checkout,
  so progress and any credential prompt are visible. An existing
  `dir` skips the clone. Agents never clone.
- `name`: a label for lists (1-40 characters, no control characters),
  stored as the tmux user option `@devcules_name` and reported as
  `name` in `term.listed`.

`term.browse {path}` answers `term.browsed {path, parent, workspace,
home, git, entries:[{name, git, hidden}], truncated}`: the
subdirectories of one directory (absolute, or relative to the
workspace; empty is the workspace), sorted, at most 500, symlinks to
folders followed. `git` marks a folder holding `.git`. Names only:
no file is read. It backs the folder picker that adds a project.

Machines answer the same frames: `devcules machine` serves tmux
terminals over its own link (unless `DEVCULES_MACHINE_SHELL=off`), so
a machine's shell needs no inbound port.
`term.input` is one-way (only a failure answers) because it is the
busiest frame; the relay sends `term.resize` and `term.detach`
one-way too and drops their answers.

Link handlers run inline in the read loop, so neither side may block
there or write to the link from it:

- runner: requests that run tmux move to their own goroutine (at most
  4 tmux calls at once), and input goes to a per-stream queue of 256
  frames drained by that stream's writer. A full queue ends the
  attach; the browser reattaches.
- serve: output goes to the browser's bounded queue, and the detach
  for a dropped browser is sent from its own goroutine.

The relay waits at most 5 s for an answer (504 after that) and drops
late replies. It registers a browser stream before asking for the
attach, because tmux's first redraw can arrive before `term.ok`.

## Limits

- Terminal id: 16 hex chars minted by serve, validated both ends;
  the tmux session is `dvc-<id>`.
- At most 16 terminals per runner; cols/rows 1–500; input frames up
  to 64 KiB.
- Output batches: flush at 32 KiB or after 10 ms, whichever first.
- Browser queue: 256 frames or 4 MiB; a slower browser is closed
  with 1013 "too slow, reattach" (tmux redraws, nothing is lost). A
  browser that stopped reading entirely may just see its socket
  drop, because the close frame queues behind the stuck write; the
  UI reattaches either way.
- Runner offline: browser streams close with 1012 "runner offline",
  also when no runner is linked at attach time; the UI waits and
  reattaches.
- Terminal gone: 1000 (the shell exited, or another device closed
  it) and 4404 (unknown terminal) stop the UI's reattach loop.

## Resume model

There is no replay buffer of our own. Each browser gets its own
attach client (`tmux attach` in a PTY), so phone and desktop share
one terminal; a new attach makes tmux redraw the screen, which is
why reloads and relinks resume. Closing the last browser stream
detaches the client; the tmux session (and shell) keeps running
until DELETE closes it. When the link drops, the runner stops every
attach client (serve forgot its streams); tmux sessions survive for
reattach. tmux sessions die with the runner container (compose runs
the runner with `init: true` so tini reaps the server's zombies).

## Environment scrub

tmux and shells get exactly five variables: `TERM=xterm-256color`,
`HOME`, `PATH`, `LANG=C.UTF-8`, `SHELL` — plus `TMUX_TMPDIR`
pointing at the private socket dir. No `DEVCULES_*` variable ever
reaches a shell.

Known limit: a shell runs as the same uid as the runner, so it can
read `/link/runner.token`. Separate uids come later; the file stays
`0400` so only that uid (and root) can read it.

## Browser API (serve, session required)

- `GET /api/terminals`: the list from the runner (a serve restart
  loses nothing). No runner linked → 503; several linked without
  `?runner=<name>` → 400; unknown name → 404. Every terminal route
  takes `?machine=<name>` instead to reach a machine's shell; a
  machine is never the default, and an unlinked one is 503.
- `POST /api/terminals [{name}]` → `201 {"id"}` (CSRF-checked).
- `GET /api/browse?path=` (and `?runner=` or `?machine=`): the
  `term.browsed` listing, for the project folder picker.
- `DELETE /api/terminals/{id}` → 204, idempotent (CSRF-checked).
- `GET` and `POST /api/agents`: the runner's coding agents, and
  starting one (`docs/agents.md`).
- `GET /api/terminals/{id}/stream`: the terminal WebSocket.
  Session cookie plus an Origin check (same host or
  `DEVCULES_PUBLIC_URL`) against cross-site hijacking. Binary
  messages carry raw terminal bytes both ways; text messages carry
  resize controls (`{"type":"resize","cols","rows"}`).

Terminal input and output are never logged: they can hold secrets.

## Web tab

The Terminals tab lists shells, opens one per view, and closes with
a confirm. xterm.js (`@xterm/xterm`, `@xterm/addon-fit`, MIT)
renders; the phone key strip holds Esc, Tab, sticky Ctrl (a
one-shot modifier for the next key), arrows and Enter at 44 px
targets, riding above the software keyboard through
`visualViewport`. Visibility and online events reattach, so locking
and unlocking the phone resumes the session; any `/api` 401 returns
to sign-in. CSP keeps `script-src` at `default-src 'self'` and adds
only `style-src 'self' 'unsafe-inline'`, which xterm.js needs for
its runtime styles (proven by the Playwright CSP-violation check in
`e2e/tests/terminals.spec.js`).

## Projects

A project names one directory on one runner or machine
(`store.Project`, migration 0009). The Projects tab lists them; each
opens terminals and agents in its directory, tagged with its id.

- `GET /api/projects` → `{projects, runners, machines}`: the projects
  and the live link names the add form offers.
- `POST /api/projects {name?, kind: "runner"|"machine", target, dir?,
  repo?}` (CSRF, audited `project.add`). With `repo`, the clone runs at
  once in a new terminal of the project (the target must be linked;
  nothing is recorded if the open fails) and `dir` defaults to the
  repository's name inside the workspace. Without it, `dir` names an
  existing directory. `name` defaults to the directory's base name and
  must be unique (409).
- `POST /api/projects/{id}/terminals [{name}]` → `201 {"id"}`: a
  shell in the project (a missing checkout of a cloned project clones
  again).
- `POST /api/agents {agent, model?, project?}`: with a project, the
  agent starts in its directory on its target.
- `DELETE /api/projects/{id}` → 204 (audited `project.remove`). Files
  and terminals stay where they are.
