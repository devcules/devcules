# Third-party notices

Devcules uses only permissive-licence dependencies (MIT, BSD, ISC or
Apache-2.0) from the official registries. Every runtime dependency is
listed here with its licence.

## Runtime dependencies

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
