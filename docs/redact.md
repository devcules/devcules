# Redaction

`internal/redact` finds secrets and personal data in text bound for a
model, swaps each for a placeholder, and puts the values back in the
reply, streamed replies included. M2.2 ships the engine, its settings
and test box; the router (M2.3) calls it on every model request.

## Types and modes

| Type | Finds | Default |
|---|---|---|
| `SECRET` | API keys and tokens: the 221 Gitleaks v8.30.1 rules (regex, keywords, entropy, allowlists) | `mask` |
| `EMAIL` | Email addresses | `off` |
| `PHONE` | `+` international numbers (8 to 15 digits) and 10-digit national numbers in `(415) 555-0132`, `415-555-0132` or `415.555.0132` form | `off` |
| `CARD` | 13 to 19 digit numbers, optionally grouped with spaces or dashes, that pass the Luhn check | `off` |
| `IPV4` | Public IPv4 addresses (private, loopback, link-local, CGNAT, multicast and documentation ranges are left alone, and so are longer dotted runs such as versions) | `off` |

Each type has one mode:

- `off`: not scanned.
- `log`: counted, the text goes through unchanged.
- `mask`: replaced with a placeholder, restored in the reply.
- `block`: the whole request is refused, naming the types (never the
  values).

Personal data is opt-in; secrets are masked from the first start.

## Placeholders

A placeholder is `REDACTED_<TYPE>_<10 hex>`, the first 5 bytes of
HMAC-SHA256 over the type and the value, under a 32-byte per-install
key. The same value always gets the same placeholder, so a model can
still tell "the same key twice" from "two keys" without seeing either.
The key is minted into the vault on first start (`redact/hmac_key`)
and never leaves serve.

Restore is per exchange: a session restores only the placeholders it
issued. A look-alike the model invents stays as written. The streaming
restorer holds back at most the 36-byte tail that could still grow
into a placeholder; everything else passes straight through.

## Where values never go

Findings carry the type, the rule id and the placeholder, never the
value. The package does not log. The block error names types only.
The counters count; they hold no text.

## Settings and test box

The policy lives in the store's meta repo (`redact.policy`). A saved
policy that does not validate stops serve with the key named; a type
missing from it (added by a later release) takes its default mode.

| Route | Does |
|---|---|
| `GET /api/redact` | `{policy, types, modes, stats, rules}`; stats are per-type `detected`, `masked`, `blocked` since serve started |
| `PUT /api/redact/policy` | The whole policy, a mode per type. `204`, or `400` naming the problem |
| `POST /api/redact/test` | `{"text": "..."}` (64 KiB max) under the live policy. Answers `{text, findings, blocked}` and never touches the counters |

All three need a session; the two writes also pass the CSRF check.
Settings has a Redaction section with a mode per type, the counters
and the test box.

## Startup self-test

Before serve listens, `redact.SelfTest` runs one known value per type
through mask, whole-reply restore, byte-by-byte streamed restore and
block. Any failure stops serve: an unredacted model call is worse
than none. Success logs `redaction self-test passed` with the rule
count and the live modes.

## Rule data

`rules/gitleaks.json` is converted from Gitleaks v8.30.1
`config/gitleaks.toml` (MIT; notice in `THIRD_PARTY_NOTICES.md`). The
matcher is our own: a keyword prefilter, the rule's regex, its secret
group (or the first group that matched, or the whole match), the
entropy threshold, then the global and per-rule allowlists
(`secret`, `match` and `line` targets, and stopwords). File-path
filters are dropped, since redaction scans text, not files.

To update the rules, convert the new `gitleaks.toml` the same way and
run `go test ./internal/redact/`: `TestEveryRuleDetects` generates a
sample from every rule's regex and proves the redactor masks it.

## Tests

- `TestGolden`: one input file per type in `testdata/`, matches and
  near misses, against a reviewed `.golden` output. Regenerate with
  `go test ./internal/redact/ -run TestGolden -update` and review the
  diff.
- `TestEveryRuleDetects`: every Gitleaks rule, from a generated sample.
- `FuzzStreamRestore`: any reply, cut into any chunks, restores to
  exactly what a whole-reply restore gives. Run longer locally with
  `go test ./internal/redact/ -run '^$' -fuzz FuzzStreamRestore -fuzztime 60s`.
- Modes, counters, overlaps, the self-test and the settings store.

No file holds a whole key shape, so neither the Gitleaks scan nor
GitHub push protection trips on fixtures: `testdata/secret.txt` splits
each value with `%%` (the test deletes it before scanning), Go sources
build theirs from parts, and documented example keys in the rule
allowlists are stored as equivalent regexes (`AIz[a]…`). The testdata
and rule files are still allowlisted in `.gitleaks.toml` as a backstop.
