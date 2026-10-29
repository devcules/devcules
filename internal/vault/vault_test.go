package vault_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

func newStore(t *testing.T) (store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := sqlite.Open(context.Background(), filepath.Join(dir, "devcules.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func mustKey(t *testing.T) vault.Key {
	t.Helper()
	k, err := vault.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func mustOpen(t *testing.T, s store.Store, k vault.Key) *vault.Vault {
	t.Helper()
	v, err := vault.Open(context.Background(), s, k)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return v
}

func TestEncryptDecrypt(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	v := mustOpen(t, s, mustKey(t))

	secret := []byte("sk-test-not-a-real-key-0123456789")
	if err := v.Put(ctx, "provider/meta/api_key", secret); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := v.Get(ctx, "provider/meta/api_key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("Get = %q, want the stored value", got)
	}

	// At rest it is sealed: the store never holds the plaintext, and
	// sealing twice gives different bytes (a fresh nonce each time).
	first, err := s.Secrets().Get(ctx, "provider/meta/api_key")
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if bytes.Contains(first, secret) {
		t.Fatal("the store holds the plaintext, want only sealed bytes")
	}
	if err := v.Put(ctx, "provider/meta/api_key", secret); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	second, _ := s.Secrets().Get(ctx, "provider/meta/api_key")
	if bytes.Equal(first, second) {
		t.Fatal("two seals of one value are identical, want a fresh nonce per seal")
	}

	if err := v.Put(ctx, "provider/other", []byte("x")); err != nil {
		t.Fatalf("Put other: %v", err)
	}
	names, err := v.Names(ctx)
	if err != nil || strings.Join(names, ",") != "provider/meta/api_key,provider/other" {
		t.Fatalf("Names = %v, %v; want both names sorted", names, err)
	}
	if err := v.Delete(ctx, "provider/other"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := v.Get(ctx, "provider/other"); !errors.Is(err, vault.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := v.Delete(ctx, "provider/other"); err != nil {
		t.Fatalf("Delete missing = %v, want nil", err)
	}
	if err := v.Put(ctx, "", []byte("x")); err == nil {
		t.Fatal("Put with an empty name succeeded")
	}
	if err := v.Put(ctx, "empty", nil); err == nil {
		t.Fatal("Put with an empty value succeeded")
	}
}

func TestReopenKeepsSecrets(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	k := mustKey(t)
	if err := mustOpen(t, s, k).Put(ctx, "a", []byte("value")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := mustOpen(t, s, k).Get(ctx, "a")
	if err != nil || string(got) != "value" {
		t.Fatalf("Get after reopen = %q, %v; want the stored value", got, err)
	}
}

func TestWrongKey(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	right, wrong := mustKey(t), mustKey(t)
	if err := mustOpen(t, s, right).Put(ctx, "a", []byte("value")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_, err := vault.Open(ctx, s, wrong)
	if !errors.Is(err, vault.ErrWrongKey) {
		t.Fatalf("Open with the wrong key = %v, want ErrWrongKey", err)
	}
	msg := err.Error()
	for _, want := range []string{right.ID(), wrong.ID(), "DEVCULES_VAULT_KEY"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %q", msg, want)
		}
	}
	for _, leak := range []string{right.Encode(), wrong.Encode()} {
		if strings.Contains(msg, leak) {
			t.Fatal("error leaks a key")
		}
	}
}

func TestSealedValueIsBoundToItsName(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	v := mustOpen(t, s, mustKey(t))
	if err := v.Put(ctx, "provider/a", []byte("a-secret")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	sealed, _ := s.Secrets().Get(ctx, "provider/a")

	// Copied under another name, the value refuses to open.
	if err := s.Secrets().Put(ctx, "provider/b", sealed, time.Now()); err != nil {
		t.Fatalf("store Put: %v", err)
	}
	if _, err := v.Get(ctx, "provider/b"); err == nil || !strings.Contains(err.Error(), `"provider/b"`) {
		t.Fatalf("Get of a moved value = %v, want an error naming it", err)
	}

	// One flipped bit, and the value refuses to open too.
	sealed[len(sealed)-1] ^= 1
	if err := s.Secrets().Put(ctx, "provider/a", sealed, time.Now()); err != nil {
		t.Fatalf("store Put: %v", err)
	}
	if _, err := v.Get(ctx, "provider/a"); err == nil {
		t.Fatal("Get of a tampered value succeeded")
	}
	if err := s.Secrets().Put(ctx, "provider/a", []byte{9}, time.Now()); err != nil {
		t.Fatalf("store Put: %v", err)
	}
	if _, err := v.Get(ctx, "provider/a"); err == nil {
		t.Fatal("Get of a truncated value succeeded")
	}
}

func TestRotation(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	oldKey, newKey := mustKey(t), mustKey(t)
	if err := mustOpen(t, s, oldKey).Put(ctx, "a", []byte("value")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := vault.Rotate(ctx, s, oldKey, oldKey); err == nil {
		t.Fatal("Rotate to the same key succeeded")
	}
	if err := vault.Rotate(ctx, s, mustKey(t), newKey); !errors.Is(err, vault.ErrWrongKey) {
		t.Fatalf("Rotate from a wrong key = %v, want ErrWrongKey", err)
	}
	if err := vault.Rotate(ctx, s, oldKey, newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	got, err := mustOpen(t, s, newKey).Get(ctx, "a")
	if err != nil || string(got) != "value" {
		t.Fatalf("Get under the new key = %q, %v; want the stored value", got, err)
	}
	if _, err := vault.Open(ctx, s, oldKey); !errors.Is(err, vault.ErrWrongKey) {
		t.Fatalf("Open with the retired key = %v, want ErrWrongKey", err)
	}
}

func TestCorruptDataKey(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{"garbage", "v2:id:AAAA", "v1:id:!!!"} {
		s, _ := newStore(t)
		if err := s.Meta().Set(ctx, "vault.dek", raw); err != nil {
			t.Fatalf("meta Set: %v", err)
		}
		_, err := vault.Open(ctx, s, mustKey(t))
		if err == nil || !strings.Contains(err.Error(), "backup") {
			t.Errorf("Open over %q = %v, want an error saying to restore a backup", raw, err)
		}
	}
}

func TestParseKey(t *testing.T) {
	k := mustKey(t)
	got, err := vault.ParseKey(" " + k.Encode() + "\n")
	if err != nil || got != k {
		t.Fatalf("ParseKey round trip = %v, %v", got, err)
	}
	if _, err := vault.ParseKey("!!!"); err == nil || !strings.Contains(err.Error(), "not base64") {
		t.Errorf("ParseKey(!!!) = %v, want not base64", err)
	}
	if _, err := vault.ParseKey("YQ=="); err == nil || !strings.Contains(err.Error(), "1 bytes") {
		t.Errorf("ParseKey(short) = %v, want a length error", err)
	}
	if k.ID() == mustKey(t).ID() || len(k.ID()) != 12 {
		t.Errorf("ID = %q: want 12 hex chars, distinct per key", k.ID())
	}
	if strings.Contains(k.Encode(), k.ID()) {
		t.Error("the key id appears inside the key")
	}
}

func TestKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.key")
	k, created, err := vault.LoadOrCreateKeyFile(path)
	if err != nil || !created {
		t.Fatalf("first LoadOrCreateKeyFile = %v, created %v", err, created)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	again, created, err := vault.LoadOrCreateKeyFile(path)
	if err != nil || created || again != k {
		t.Fatalf("second LoadOrCreateKeyFile = %v, created %v, same %v", err, created, again == k)
	}

	bad := filepath.Join(t.TempDir(), "vault.key")
	if err := os.WriteFile(bad, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.LoadOrCreateKeyFile(bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("bad key file = %v, want an error naming the file", err)
	}
	if _, _, err := vault.LoadOrCreateKeyFile(filepath.Join(t.TempDir(), "missing-dir", "vault.key")); err == nil || !strings.Contains(err.Error(), "DEVCULES_DATA_DIR") {
		t.Fatalf("unwritable key file = %v, want next steps", err)
	}
}

func TestRotateKeyFile(t *testing.T) {
	ctx := context.Background()
	s, dir := newStore(t)
	path := filepath.Join(dir, "vault.key")
	if _, _, err := vault.RotateKeyFile(ctx, s, path); err == nil || !strings.Contains(err.Error(), "start serve once") {
		t.Fatalf("rotating a missing key file = %v, want next steps", err)
	}
	k, _, err := vault.LoadOrCreateKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := mustOpen(t, s, k).Put(ctx, "a", []byte("value")); err != nil {
		t.Fatal(err)
	}
	oldID, newID, err := vault.RotateKeyFile(ctx, s, path)
	if err != nil || oldID != k.ID() || newID == oldID {
		t.Fatalf("RotateKeyFile = %q, %q, %v", oldID, newID, err)
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Fatalf("pending key file left behind: %v", err)
	}
	rotated, err := vault.ReadKeyFile(path)
	if err != nil || rotated.ID() != newID {
		t.Fatalf("key file after rotation = %v, %v; want the new key", rotated.ID(), err)
	}
	got, err := mustOpen(t, s, rotated).Get(ctx, "a")
	if err != nil || string(got) != "value" {
		t.Fatalf("Get after RotateKeyFile = %q, %v", got, err)
	}
}

func TestFinishPendingRotation(t *testing.T) {
	ctx := context.Background()

	t.Run("NoPending", func(t *testing.T) {
		s, dir := newStore(t)
		if err := vault.FinishPendingRotation(ctx, s, filepath.Join(dir, "vault.key")); err != nil {
			t.Fatalf("FinishPendingRotation with nothing pending = %v", err)
		}
	})

	t.Run("CrashBeforeRewrap", func(t *testing.T) {
		s, dir := newStore(t)
		path := filepath.Join(dir, "vault.key")
		k, _, _ := vault.LoadOrCreateKeyFile(path)
		mustOpen(t, s, k)
		// The new key was written, but the data key never rewrapped.
		if err := os.WriteFile(path+".new", []byte(mustKey(t).Encode()), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := vault.FinishPendingRotation(ctx, s, path); err != nil {
			t.Fatalf("FinishPendingRotation = %v", err)
		}
		if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
			t.Fatal("the unused pending key survived")
		}
		if still, _ := vault.ReadKeyFile(path); still != k {
			t.Fatal("the key file changed, want the old key kept")
		}
	})

	t.Run("CrashBeforeRename", func(t *testing.T) {
		s, dir := newStore(t)
		path := filepath.Join(dir, "vault.key")
		k, _, _ := vault.LoadOrCreateKeyFile(path)
		mustOpen(t, s, k)
		next := mustKey(t)
		if err := os.WriteFile(path+".new", []byte(next.Encode()), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := vault.Rotate(ctx, s, k, next); err != nil {
			t.Fatal(err)
		}
		if err := vault.FinishPendingRotation(ctx, s, path); err != nil {
			t.Fatalf("FinishPendingRotation = %v", err)
		}
		if now, _ := vault.ReadKeyFile(path); now != next {
			t.Fatal("the committed key did not replace the key file")
		}
	})
}
