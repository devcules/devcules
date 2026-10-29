-- 0004: the vault's sealed secrets. sealed is version || nonce ||
-- AES-256-GCM ciphertext under the vault's data key, bound to name as
-- associated data; the store never sees plaintext. The data key itself
-- is wrapped by the vault key and kept in meta (vault.dek). Times are
-- UTC RFC 3339 strings.
CREATE TABLE secrets (
  name TEXT PRIMARY KEY,
  sealed BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
