package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/vault"
)

// metaKeySecret is where META_API_KEY lands in the vault. The router
// reads it from here; after the first start the env var can go.
const metaKeySecret = "provider/meta/api_key"

// vaultKeyPath is the generated key file used when DEVCULES_VAULT_KEY is
// unset. It sits beside the database, so a backup must take both.
func vaultKeyPath(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "vault.key")
}

// openVault resolves the vault key (DEVCULES_VAULT_KEY, or the key file,
// created on first start) and opens the vault. It then moves a set
// META_API_KEY into the vault. Logs carry key ids and paths, never keys
// or secret values.
func openVault(ctx context.Context, cfg config.Config, s store.Store, log *slog.Logger) (*vault.Vault, error) {
	var k vault.Key
	if cfg.VaultKey != "" {
		parsed, err := vault.ParseKey(cfg.VaultKey)
		if err != nil {
			return nil, err
		}
		k = parsed
	} else {
		path := vaultKeyPath(cfg)
		if err := vault.FinishPendingRotation(ctx, s, path); err != nil {
			return nil, err
		}
		loaded, created, err := vault.LoadOrCreateKeyFile(path)
		if err != nil {
			return nil, err
		}
		if created {
			log.Warn("vault key file created: back it up with the database, without it the vault's secrets are lost", "path", path)
		}
		k = loaded
	}
	v, err := vault.Open(ctx, s, k)
	if err != nil {
		return nil, err
	}
	log.Info("vault open", "key_id", k.ID())
	if cfg.MetaAPIKey != "" {
		stored, err := v.Get(ctx, metaKeySecret)
		if err != nil && !errors.Is(err, vault.ErrNotFound) {
			return nil, err
		}
		if string(stored) != cfg.MetaAPIKey {
			if err := v.Put(ctx, metaKeySecret, []byte(cfg.MetaAPIKey)); err != nil {
				return nil, err
			}
			log.Info("META_API_KEY stored in the vault: you can remove it from the environment", "secret", metaKeySecret)
		}
	}
	return v, nil
}

// runVault runs `devcules vault <action>`. Only rotate exists: it
// rewraps the vault's data key under a new vault key. With
// DEVCULES_VAULT_KEY set, the new key comes from DEVCULES_VAULT_NEW_KEY
// and the operator moves it into place; with the generated key file,
// the file rotates itself.
func runVault(cfg config.Config, args []string, stdout, stderr io.Writer, open storeOpener) int {
	if len(args) != 1 || args[0] != "rotate" {
		return usage(stderr, "usage: devcules vault rotate")
	}
	if cfg.DatabaseURL != "" {
		fmt.Fprintln(stderr, "DEVCULES_DATABASE_URL is set: the Postgres store does not exist yet (stretch goal); unset it to use SQLite")
		return exitError
	}
	s, err := open(filepath.Join(cfg.DataDir, "devcules.db"))
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	defer s.Close()
	ctx := context.Background()

	if cfg.VaultKey == "" {
		if cfg.VaultNewKey != "" {
			fmt.Fprintln(stderr, "DEVCULES_VAULT_NEW_KEY is set, but the vault uses its generated key file, which rotates itself: unset DEVCULES_VAULT_NEW_KEY and run again")
			return exitError
		}
		oldID, newID, err := vault.RotateKeyFile(ctx, s, vaultKeyPath(cfg))
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		recordRotation(ctx, s, oldID, newID)
		fmt.Fprintf(stdout, "Vault key rotated (%s -> %s) in %s. Restart serve to use it.\n", oldID, newID, vaultKeyPath(cfg))
		return exitOK
	}

	if cfg.VaultNewKey == "" {
		fmt.Fprintln(stderr, "DEVCULES_VAULT_NEW_KEY missing: set it to the key to rotate to (generate one with: openssl rand -base64 32)")
		return exitError
	}
	oldKey, err := vault.ParseKey(cfg.VaultKey)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	newKey, err := vault.ParseKey(cfg.VaultNewKey)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	if err := vault.Rotate(ctx, s, oldKey, newKey); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	recordRotation(ctx, s, oldKey.ID(), newKey.ID())
	fmt.Fprintf(stdout, "Vault key rotated (%s -> %s). Now set DEVCULES_VAULT_KEY to the value of DEVCULES_VAULT_NEW_KEY, unset DEVCULES_VAULT_NEW_KEY, and restart serve.\n", oldKey.ID(), newKey.ID())
	return exitOK
}
