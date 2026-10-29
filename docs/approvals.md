# Approvals and push

Agents ask before risky tool calls, and the owner answers from the
phone. `internal/approvals` holds the policy, the critical command
rules and the pending approvals; `internal/push` sends Web Push
notices; `devcules hook` is what an agent's adapter runs before each
tool call.

## The flow

1. An agent wants to run a tool. Its adapter (M3.3 onwards) pipes the
   call to `devcules hook`, which posts it to serve with the agent's
   launch token.
2. Serve looks up the access mode of the launch's runner and checks
   the command against the critical rules. The answer is allow, deny,
   or ask.
3. On ask, serve stores a pending approval (secrets in the command
   masked, at most 4 KiB kept), audits `approval.ask`, and sends a
   push notice to every subscribed device. The hook prints one line on
   stderr ("... needs approval; answer it in Devcules on your phone")
   and waits.
4. The owner taps the notice, which opens Devcules on the approval
   (`/?approval=<id>`), and taps **Allow once** or **Deny**. The answer
   is audited as `approval.allow` or `approval.deny` with who answered
   and from where.
5. The hook gets the answer and exits. An approval nobody answers in
   10 minutes is denied and audited as `approval.expire`.

Waiting approvals show on Home, with a count on the Home tab, whether
or not a notice was sent; Activity lists the answered ones.

## Access modes

Each runner has a mode; launches with no runner (minted without one)
share the `unbound` target. Settings, Access modes changes them, and
every change is audited as `approval.mode` with the old and new mode.

| Mode | Critical call | Other call |
|---|---|---|
| not set | deny | deny |
| `off` | deny | deny |
| `critical` | ask | allow |
| `all` | ask | ask |
| `full` | allow | allow |

A runner starts with no mode, so nothing runs until the owner picks
one: a fresh install never lets an agent run unasked by default. A
deny from the policy is audited as `approval.blocked`, and its reason
("no access mode is set for this runner yet: pick one in Devcules,
Settings, Access modes") goes back to the agent.

A call is critical when an enabled rule matches its command, or when
the agent marks it irreversible.

## Critical rules

Built-in rules, all on by default:

| Id | Catches |
|---|---|
| `recursive-delete` | `rm -r`, `rm -rf`, `rm --recursive`, `find … -delete`, PowerShell `Remove-Item -Recurse`, `rd /s` |
| `disk-format` | `mkfs`, `wipefs`, `diskutil erase…`, `sgdisk --zap`, `format C:`, `Format-Volume`, `Clear-Disk` |
| `dd-of` | `dd … of=` |
| `shutdown` | `shutdown`, `reboot`, `halt`, `poweroff`, `init 0/6`, `systemctl reboot`, `Restart-Computer` |
| `force-push` | `git push --force`, `-f`, `--force-with-lease`, a `+ref` |
| `git-reset-hard` | `git reset --hard` |
| `pipe-to-shell` | `curl … \| sh`, `wget … \| bash`, `irm … \| iex` and the like |
| `recursive-chmod` | `chmod -R`, `chown -R`, `chgrp -R`, `icacls /t`, `takeown /r` |
| `drop-table` | `DROP TABLE`, `DROP DATABASE`, `DROP SCHEMA`, `TRUNCATE TABLE` |
| `crontab-remove` | `crontab -r` |
| `elevate` | `sudo`, `doas`, `pkexec`, `runas`, `su -`, `su root` |

Rules match a command word where a command starts (the line start,
after `;`, `&`, `|`, `(`, a backtick, or after `sudo`, `env`, `xargs`
and the like), so `echo shutdown later` is not critical but
`make && shutdown -h now` is. The owner may turn built-ins off, and
add up to 50 rules of their own (a name, and a regular expression of
at most 500 characters in Go RE2 syntax). Changes are audited as
`approval.rules`. Settings has a box that checks a command against the
live rules.

**These rules are a tripwire, not a sandbox.** They catch an agent
that means well but is about to do something it can't undo. A
determined program can always hide a command from a pattern (an
alias, a script file, base64). Isolation comes from the runner's
container and its access mode, not from these rules.

## The hook

`devcules hook` reads one JSON object on stdin:

```json
{"agent": "Claude Code", "tool": "Bash", "command": "git push -f", "irreversible": false}
```

It needs `DEVCULES_LAUNCH_TOKEN` (or `DEVCULES_LAUNCH_TOKEN_FILE`) and
`DEVCULES_SERVE_URL`, which Devcules sets when it launches an agent.
It prints `{"decision", "reason", "id"}` on stdout and exits:

| Exit | Meaning |
|---|---|
| `0` | allow |
| `2` | deny (the reason says why) |
| `1` | the hook failed; adapters treat it as deny |

Transport errors and `5xx` answers are retried with backoff (1 s,
doubling to 15 s) for up to 15 minutes, so a serve restart does not
fail a waiting call. A `4xx` answer is final.

Behind the hook, two routes take the launch token (no session, no
CSRF check):

| Route | Does |
|---|---|
| `POST /hook/approvals?wait=N` | Reports a call. Answers `{"decision": "allow" \| "deny" \| "ask", "reason", "approval"}`; on ask it first waits up to `N` seconds (at most 25) for the owner. |
| `GET /hook/approvals/{id}?wait=N` | Waits again. Only the launch that asked may poll its approval. |

## The owner's API

All take the session; writes carry the CSRF check.

| Route | Does |
|---|---|
| `GET /api/approvals?status=&limit=` | Approvals newest first, and the pending count. |
| `POST /api/approvals/{id}` | `{"decision": "allow" \| "deny"}`. `404` unknown, `409` already answered. |
| `GET /api/approvals/settings` | Targets with their modes, the modes, the rules and the timeout. |
| `PUT /api/approvals/modes` | `{"target": "runner:<name>" \| "unbound", "mode": "critical"}` |
| `PUT /api/approvals/rules` | `{"rules": [...]}`: the whole list. |
| `POST /api/approvals/check` | `{"command": "..."}` answers `{"critical", "rule"}`. |

## Push

Serve makes a VAPID key pair on first start (a P-256 key, kept in the
vault as `push/vapid_key`) and sends notices to the browser push
services itself: no third-party push account is needed. The steps
follow RFC 8030, RFC 8291 and RFC 8292 (see `docs/integrations.md`).

- Settings, Notifications, **Turn on notifications here** asks the
  browser for permission, subscribes with the server's key, and
  registers the device with a name. On iPhone, Web Push only works in
  Devcules added to the Home Screen (Share, Add to Home Screen).
- A notice says who wants to run what kind of thing ("Claude Code on
  laptop wants to run Force push.") and never carries the command, so
  nothing sensitive reaches Apple's, Google's or Mozilla's servers.
  It is encrypted end to end to the device anyway.
- At most 20 devices. A push service that answers `404` or `410`
  means the device unsubscribed, and serve forgets it. Failures are
  logged without the endpoint.
- `DEVCULES_PUSH_CONTACT` (a `mailto:` or `https:` URL) is the contact
  push services see in the VAPID token. It defaults to the public URL
  when that is https.

| Route | Does |
|---|---|
| `GET /api/push` | The server's public key and the devices (id, name, push service host, dates; never the endpoint). |
| `POST /api/push/devices` | The browser's `PushSubscription` JSON plus `"label"`. Audited as `push.subscribe`. |
| `DELETE /api/push/devices/{id}` | Audited as `push.remove`. |
| `POST /api/push/test` | Sends a test notice; answers `{"sent", "failed", "removed"}`. |

The service worker (`web/public/sw.js`) only shows notices and routes
taps to the approval; it caches nothing.
