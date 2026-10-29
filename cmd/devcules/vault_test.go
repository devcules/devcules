package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

func TestRunServeVaultKeyFile(t *testing.T) {
	dir := t.TempDir()
	cfg := serveConfig(dir)
	cfg.VaultKey = ""
	code, stdout, stderr, _, _ := serveFixture(t, cfg, nil)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	path := filepath.Join(dir, "vault.key")
	k, err := vault.ReadKeyFile(path)
	if err != nil {
		t.Fatalf("generated key file: %v", err)
	}
	if !strings.Contains(stdout, "back it up") || strings.Contains(stdout, k.Encode()) {
		t.Fatalf("logs = %q, want the backup warning and never the key", stdout)
	}

	// The provider key landed in the vault under the generated key.
	s, err := sqlite.Open(context.Background(), filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := vault.Open(context.Background(), s, k)
	if err != nil {
		t.Fatalf("vault.Open with the file key: %v", err)
	}
	got, err := v.Get(context.Background(), metaKeySecret)
	if err != nil || string(got) != testMetaKey {
		t.Fatalf("META_API_KEY in the vault = %q, %v", got, err)
	}
}

func TestRunServeWrongVaultKey(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr, _, _ := serveFixture(t, serveConfig(dir), nil); code != exitOK {
		t.Fatalf("first start: %s", stderr)
	}
	cfg := serveConfig(dir)
	other, _ := vault.NewKey()
	cfg.VaultKey = other.Encode()
	code, _, stderr, _, _ := serveFixture(t, cfg, nil)
	if code != exitError || !strings.Contains(stderr, "DEVCULES_VAULT_KEY") || !strings.Contains(stderr, other.ID()) {
		t.Fatalf("exit = %d, stderr = %q, want the wrong-key error with next steps", code, stderr)
	}
	if strings.Contains(stderr, other.Encode()) || strings.Contains(stderr, testVaultKey) {
		t.Fatal("stderr leaks a vault key")
	}
}

func TestRunVault(t *testing.T) {
	var out, errOut bytes.Buffer
	reset := func() { out.Reset(); errOut.Reset() }

	t.Run("usage", func(t *testing.T) {
		reset()
		if code := runVault(serveConfig(t.TempDir()), []string{"spin"}, &out, &errOut, openSQLite); code != exitUsage {
			t.Fatalf("exit = %d, want usage", code)
		}
		if !strings.Contains(errOut.String(), "devcules vault rotate") {
			t.Fatalf("stderr = %q, want the usage", errOut.String())
		}
	})

	t.Run("postgres refused", func(t *testing.T) {
		reset()
		cfg := serveConfig(t.TempDir())
		cfg.DatabaseURL = "postgres://db/x"
		if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitError {
			t.Fatalf("exit = %d, want error", code)
		}
	})

	t.Run("key file", func(t *testing.T) {
		reset()
		dir := t.TempDir()
		cfg := serveConfig(dir)
		cfg.VaultKey = ""
		if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitError || !strings.Contains(errOut.String(), "start serve once") {
			t.Fatalf("rotate before any start: exit = %d, stderr = %q", code, errOut.String())
		}
		if code, _, stderr, _, _ := serveFixture(t, cfg, nil); code != exitOK {
			t.Fatalf("serve: %s", stderr)
		}
		before, _ := vault.ReadKeyFile(filepath.Join(dir, "vault.key"))
		reset()
		if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitOK {
			t.Fatalf("rotate: exit = %d, stderr = %q", code, errOut.String())
		}
		after, _ := vault.ReadKeyFile(filepath.Join(dir, "vault.key"))
		if before == after || !strings.Contains(out.String(), before.ID()+" -> "+after.ID()) {
			t.Fatalf("stdout = %q, want the key replaced and both ids shown", out.String())
		}
		if code, _, stderr, _, _ := serveFixture(t, cfg, nil); code != exitOK {
			t.Fatalf("serve after rotation: %s", stderr)
		}
		reset()
		cfg.VaultNewKey = testVaultKey
		if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitError || !strings.Contains(errOut.String(), "unset DEVCULES_VAULT_NEW_KEY") {
			t.Fatalf("new key with a key file: exit = %d, stderr = %q", code, errOut.String())
		}
	})

	t.Run("env key", func(t *testing.T) {
		dir := t.TempDir()
		cfg := serveConfig(dir)
		if code, _, stderr, _, _ := serveFixture(t, cfg, nil); code != exitOK {
			t.Fatalf("serve: %s", stderr)
		}
		reset()
		if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitError || !strings.Contains(errOut.String(), "DEVCULES_VAULT_NEW_KEY missing") {
			t.Fatalf("no new key: exit = %d, stderr = %q", code, errOut.String())
		}
		next, _ := vault.NewKey()
		cfg.VaultNewKey = next.Encode()
		reset()
		if code := runVault(cfg, []string{"rotate"}, &out, &errOut, openSQLite); code != exitOK {
			t.Fatalf("rotate: exit = %d, stderr = %q", code, errOut.String())
		}
		if strings.Contains(out.String(), next.Encode()) || !strings.Contains(out.String(), next.ID()) {
			t.Fatalf("stdout = %q, want the new id and never the key", out.String())
		}
		// The old key no longer opens the vault; the new one does.
		if code, _, _, _, _ := serveFixture(t, serveConfig(dir), nil); code != exitError {
			t.Fatal("serve with the retired key started, want the wrong-key error")
		}
		cfg.VaultKey, cfg.VaultNewKey = next.Encode(), ""
		if code, _, stderr, _, _ := serveFixture(t, cfg, nil); code != exitOK {
			t.Fatalf("serve with the new key: %s", stderr)
		}
	})

	t.Run("dispatch", func(t *testing.T) {
		dir := t.TempDir()
		env := map[string]string{"DEVCULES_DATA_DIR": dir}
		code, _, stderr := invoke(t, env, "vault", "rotate")
		if code != exitError || !strings.Contains(stderr, "start serve once") {
			t.Fatalf("exit = %d, stderr = %q, want the rotate command to run", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "vault.key")); !os.IsNotExist(err) {
			t.Fatal("rotate created a key file, want it to leave first-start to serve")
		}
	})
}
