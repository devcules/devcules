# Store

`internal/store` defines the storage surface; `internal/store/sqlite`
implements it on SQLite; `internal/store/storetest` holds the shared
contract suite every implementation runs.

## Shape

`Store` is lifecycle plus repos: `Ping`, `Close`, `SchemaVersion`, and
`Meta()`, a tiny string key/value repo for installer-level state. M0.3
ships only Meta. Repos are deliberately small interfaces so each module
can own exactly one. M1.1 adds auth's two repos by exactly the
procedure below: `Users()` (one argon2id hash per username; passwords
never reach the store, plus the `webauthn_handle` from M1.5) and
`Sessions()` (sessions by token SHA-256;
values never reach the store), with migration `0002`. M1.5 adds
auth's third repo the same way: `Passkeys()` (one row per credential:
id, COSE public key, sign count, AAGUID, transports, backup flags,
name, both timestamps), with migration `0003`. M2.1 adds the vault's
repo, `Secrets()` (sealed bytes by name; the vault encrypts before the
store sees anything), with migration `0004`. The vault's wrapped data
key sits in `Meta()` under `vault.dek`. M2.2 adds no repo: the
redaction policy is one `Meta()` row, `redact.policy`, and its
placeholder key is the vault secret `redact/hmac_key`. M2.3 adds
the launch tokens' repo, `LaunchTokens()` (one row per token: id,
SHA-256 of the token, label, provider, created, expiry, revoked and
last-used times; the token itself never reaches the store), with
migration `0005`.

## How a later module adds a repo

1. Extend the `Store` interface with one accessor (e.g. `Users()`),
   together with the repo interface. An interface change owned by
   another module goes in as its own small PR first (PLAN.md §9).
2. Add one numbered migration creating only that repo's tables.
3. Extend the `storetest` suite with that repo's contract tests, and
   implement them in every backend.

## Migrations

Numbered `.sql` files embedded with `go:embed`, applied in order, each
in its own transaction and recorded in `schema_migrations` with a
SHA-256 checksum. Reopening is idempotent. An edited already-applied
migration is a startup error naming the file; a database newer than the
binary refuses with an upgrade prompt and never attempts to run.

## SQLite behaviour

- File at `<DataDir>/devcules.db`; the directory is created `0700`,
  the file `0600`.
- One connection (`SetMaxOpenConns(1)`) with `journal_mode=WAL`,
  `foreign_keys=ON` and a 5 s `busy_timeout`, passed in the connection
  string so every pooled connection gets them. Concurrent first opens
  take turns (write lock plus a bounded retry on SQLITE_BUSY). No pool
  tuning yet: M0.3 serializes writers instead of configuring a pool it
  cannot fill.
- Pure-Go driver (`modernc.org/sqlite`), `database/sql` only, no ORM.
- The Postgres URL is not handled here; that wiring (and the Postgres
  backend, a stretch goal) belongs to a later module.

## Contract suite

`storetest.Run(t, newStore)` covers: fresh open migrates, reopen is a
no-op that keeps data, Meta get/set/delete with a typed not-found,
Users create/duplicate/lookup with typed exists and not-found errors,
Sessions create/get/touch/delete with the same, Ping before and after
Close, concurrent use under `-race`, and honored context
cancellation. Backend internals (edited migrations, too-new
databases, pragmas, file layout) are tested in the backend's own
package. `storetest` proves itself with an in-memory fake: the suite
passes on the fake and a child-process probe proves it fails a broken
one. Times cross the boundary as UTC RFC 3339 (second precision);
fixtures use `Truncate(time.Second)` and compare by instant.
