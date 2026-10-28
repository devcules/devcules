# Third-party notices

Devcules uses only permissive-licence dependencies (MIT, BSD, ISC or
Apache-2.0) from the official registries. Every runtime dependency is
listed here with its licence.

## Runtime dependencies

### Go

M0.3 adds the SQLite backend. Every package below is pulled in by
`modernc.org/sqlite` (pure Go, no cgo); all licences are permissive
(`go-licenses report ./...` reproduces this list):

| Package | Licence |
|---|---|
| `github.com/dustin/go-humanize` | MIT |
| `github.com/google/uuid` | BSD-3-Clause |
| `github.com/mattn/go-isatty` | MIT |
| `github.com/ncruces/go-strftime` | MIT |
| `github.com/remyoudompheng/bigfft` | BSD-3-Clause |
| `golang.org/x/sys` | BSD-3-Clause |
| `modernc.org/libc` | BSD-3-Clause |
| `modernc.org/mathutil` | BSD-3-Clause |
| `modernc.org/memory` | BSD-3-Clause |
| `modernc.org/sqlite` v1.61.0 | BSD-3-Clause |
| `golang.org/x/crypto` v0.58.0 | BSD-3-Clause |

M1.1 adds the argon2id password hashing above (only the `argon2`
package is imported; the unmaintained `openpgp` package the
vulnerability scanner flags is never used); nothing else new is a
runtime dependency. The Playwright suite (`e2e/`) is dev-only test
tooling and ships in no image: `@playwright/test` (Apache-2.0) with
its browser downloads happens at test time.

M1.3 adds the link WebSocket transport below (`go-licenses report
./...` reproduces this list):

| Package | Licence |
|---|---|
| `github.com/coder/websocket` v1.8.15 | ISC |

M1.4 adds the PTY driver below (MIT, verified from the module zip's
`LICENSE`):

| Package | Licence |
|---|---|
| `github.com/creack/pty` v1.1.24 | MIT |

### Web (M1.2)

The built PWA ships these production dependencies, all MIT
(`license-checker --production` reproduces this list):

| Package | Licence |
|---|---|
| `react` 18.3.1 | MIT |
| `react-dom` 18.3.1 | MIT |
| `scheduler` 0.23.2 | MIT |
| `loose-envify` 1.4.0 | MIT |
| `js-tokens` 4.0.0 | MIT |

M1.4 adds the terminal renderer below, both MIT
(`license-checker --production` reproduces this list):

| Package | Licence |
|---|---|
| `@xterm/xterm` 6.0.0 | MIT |
| `@xterm/addon-fit` 0.11.0 | MIT |

System fonts only: no remote fonts, CDNs or analytics. Dev-only
tooling (Vite, Vitest, Playwright, ESLint and friends) ships in no
image.

## Container image contents

The runtime image installs distro programs that are separate, unlinked
binaries, not Go dependencies (so `go-licenses` does not list them):
`tmux` (ISC), `git` (GPL-2.0) and `bash` (GPL-3.0) from Debian, plus
`ca-certificates`. They ship unmodified; their sources are the Debian
archives. Agent CLIs are never baked in.

## Standing rules (PLAN.md §1)

- Never add a GPL, AGPL or SSPL dependency.
- The Gitleaks detection rule data (MIT) is the one exception allowed to
  be converted into `internal/redact/rules/gitleaks.json` (module M2.2);
  its MIT notice will be recorded here when that happens.
