# Vault

`internal/vault` keeps serve's secrets, such as provider API keys,
encrypted at rest. It lives only in serve: the runner and machine roles
can never import it (`arch_test.go`), and they refuse to start with a
vault key or `META_API_KEY` in their environment.

## Two keys

| Key | Where it lives | What it does |
|---|---|---|
| Vault key | `DEVCULES_VAULT_KEY` / `_FILE`, or the generated `<DataDir>/vault.key` (`0600`) | Wraps the data key. Never stored in the database. |
| Data key | `meta` row `vault.dek`, wrapped by the vault key | Encrypts every secret. Never leaves serve's memory unwrapped. |

Both use AES-256-GCM with a random 12-byte nonce per seal. A sealed
value is `version (1) || nonce || ciphertext`. Each secret is bound to
its name as associated data, so a value copied under another name, or
altered by one bit, refuses to open.

Every key has a short, non-secret **key id** (12 hex chars). Logs and
errors show ids and paths, never keys or secret values.

## First start

With no `DEVCULES_VAULT_KEY`, serve generates `vault.key` beside the
database and logs a warning to back it up. It mints a data key, wraps
it, and stores the wrap. A set `META_API_KEY` is then stored as
`provider/meta/api_key`; after serve logs that, the variable can leave
the environment. Redaction (M2.2) then mints its 32-byte placeholder
key as `redact/hmac_key` (see `docs/redact.md`).

## Provider keys from Settings

Settings → Providers stores each model vendor's API key under
`provider/<id>/api_key` (`router.KeySecret`): `PUT
/api/providers/{id}/key {"key"}` and `DELETE /api/providers/{id}/key`,
session plus CSRF, for the providers the router serves (`meta`,
`anthropic`, `openai`). The key is sealed like every other secret and
is never sent back, not even in part: `GET /api/providers` only says
`ready`. The router reads the key on every call, so a change applies at
once; it also drops that provider's cached model list. Audit records
`provider.key.set` and `provider.key.remove` with the provider id only.
`META_API_KEY` in serve's environment still seeds the Meta key on start.

## Wrong key

If the configured key does not open the stored data key, serve stops
before listening:

```
vault: wrong vault key: the vault was sealed with key id 3f…, but the
configured key has id 9a…; set DEVCULES_VAULT_KEY (or
DEVCULES_VAULT_KEY_FILE) to the original key, or restore its vault.key file
```

## Rotation

Rotation rewraps the data key in one write; the secrets themselves are
never re-encrypted, so a crash leaves the old wrap or the new one,
never a mix. A running serve keeps working; restart it afterwards.

**Generated key file** (no `DEVCULES_VAULT_KEY`):

```
docker compose -f deploy/compose.yaml exec serve devcules vault rotate
docker compose -f deploy/compose.yaml restart serve
```

It writes the new key to `vault.key.new`, rewraps, then renames it over
`vault.key`. If it stops midway, the next start settles it: a pending
key that opens the data key replaces the file, one that does not is
removed.

**Key from the environment:** set `DEVCULES_VAULT_NEW_KEY` (for example
`openssl rand -base64 32`) beside the current `DEVCULES_VAULT_KEY`, run
`devcules vault rotate`, then make the new key the `DEVCULES_VAULT_KEY`,
unset `DEVCULES_VAULT_NEW_KEY`, and restart serve.

## What it does not do

The vault does not decide which secrets exist or who may read them; the
router and providers do (M2.3). It has no HTTP API: nothing outside
serve's process can read a secret.
