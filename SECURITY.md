# Security policy

## Reporting a vulnerability

Do not open a public issue. Use GitHub's private vulnerability
reporting on this repository so the report stays confidential until a
fix is ready. Say what you found, how to reproduce it, and what you
think the impact is.

## What to expect

- An acknowledgement of your report.
- A fix as a priority, with credit if you want it.
- A `CHANGELOG.md` entry describing the fix once it ships.

## Supported versions

Before 1.0 only the latest `main` is supported. After 1.0 the latest
minor release line is supported.

## Built-in protections

- No secrets in the repo: CI runs Gitleaks on every push.
- `govulncheck` runs in CI on every pull request.
- Dependencies are permissive-licence only and listed in
  `THIRD_PARTY_NOTICES.md`.
