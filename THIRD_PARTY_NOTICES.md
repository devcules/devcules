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

M1.5 adds the WebAuthn server below with its linked transitive
dependencies (verified from each module zip's `LICENSE`; only the
`webauthn` and `protocol` packages are imported, the `metadata`
package is not). The browser side uses no library. (`go-licenses
report ./...` reproduces this list):

| Package | Licence |
|---|---|
| `github.com/go-webauthn/webauthn` v0.18.2 | BSD-3-Clause |
| `github.com/fxamacker/cbor/v2` v2.9.4 | MIT |
| `github.com/go-viper/mapstructure/v2` v2.5.0 | MIT |
| `github.com/go-webauthn/x` v0.3.1 | BSD-3-Clause, ISC, BSD-2-Clause |
| `github.com/golang-jwt/jwt/v5` v5.3.1 | MIT |
| `github.com/google/go-tpm` v0.9.8 | Apache-2.0 |
| `github.com/philhofer/fwd` v1.2.0 | MIT |
| `github.com/tinylib/msgp` v1.6.4 | MIT |
| `github.com/x448/float16` v0.8.4 | MIT |

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
  its notice is below.

## Converted rule data

M2.2 converts the detection rules of Gitleaks v8.30.1
(`config/gitleaks.toml`, https://github.com/gitleaks/gitleaks) into
`internal/redact/rules/gitleaks.json`: ids, descriptions, regexes,
keywords, entropy thresholds, secret groups and the text allowlists
(file-path filters are dropped). This is rule data only; no Gitleaks
code is used, and the matcher in `internal/redact` is written from
scratch. It is used under the MIT licence:

```
MIT License

Copyright (c) 2019 Zachary Rice

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
