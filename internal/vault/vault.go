package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// dekMetaKey is the meta row holding the wrapped data key, as
// "v1:<vault key id>:<base64 sealed data key>". The id is only for
// error messages; GCM authentication decides whether a key fits.
const dekMetaKey = "vault.dek"

// sealVersion prefixes every sealed value, so a later format can live
// beside this one.
const sealVersion = 1

const (
	dekAAD    = "devcules/vault/v1/dek"
	secretAAD = "devcules/vault/v1/secret/"
)

// ErrNotFound reports a secret that was never stored (or was deleted).
var ErrNotFound = errors.New("vault: secret not found")

// ErrWrongKey reports a vault key that does not open the stored data
// key: the store was sealed under another key.
var ErrWrongKey = errors.New("vault: wrong vault key")

// Vault seals and opens secrets. It is safe for concurrent use.
type Vault struct {
	secrets store.SecretsRepo
	dek     cipher.AEAD
	now     func() time.Time
}

// Open unwraps the data key with k, minting and storing a new data key
// on the very first open. A key that does not fit returns an error
// wrapping ErrWrongKey, with both key ids and what to do next.
func Open(ctx context.Context, s store.Store, k Key) (*Vault, error) {
	dek, err := loadDEK(ctx, s.Meta(), k)
	if errors.Is(err, store.ErrNotFound) {
		dek, err = mintDEK(ctx, s.Meta(), k)
	}
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(dek[:])
	if err != nil {
		return nil, err
	}
	return &Vault{secrets: s.Secrets(), dek: aead, now: time.Now}, nil
}

// Put seals value under name, replacing any earlier value. Names are
// path-like ("provider/meta/api_key"); an empty name or value is an
// error.
func (v *Vault) Put(ctx context.Context, name string, value []byte) error {
	if name == "" {
		return errors.New("vault: secret name must not be empty")
	}
	if len(value) == 0 {
		return fmt.Errorf("vault: secret %q must not be empty", name)
	}
	sealed, err := seal(v.dek, value, secretAAD+name)
	if err != nil {
		return fmt.Errorf("vault: cannot seal %q: %w", name, err)
	}
	if err := v.secrets.Put(ctx, name, sealed, v.now().UTC()); err != nil {
		return fmt.Errorf("vault: cannot store %q: %w", name, err)
	}
	return nil
}

// Get opens the secret stored under name. A missing name wraps
// ErrNotFound; a value that fails to open (tampered, or copied from
// another name) is an error that names the secret.
func (v *Vault) Get(ctx context.Context, name string) ([]byte, error) {
	sealed, err := v.secrets.Get(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("vault: secret %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("vault: cannot read %q: %w", name, err)
	}
	value, err := open(v.dek, sealed, secretAAD+name)
	if err != nil {
		return nil, fmt.Errorf("vault: secret %q does not open: it was altered or stored under another name; store it again", name)
	}
	return value, nil
}

// Names lists the stored secret names, sorted. Values stay sealed.
func (v *Vault) Names(ctx context.Context) ([]string, error) {
	names, err := v.secrets.Names(ctx)
	if err != nil {
		return nil, fmt.Errorf("vault: cannot list secrets: %w", err)
	}
	return names, nil
}

// Delete removes name. A missing name is not an error.
func (v *Vault) Delete(ctx context.Context, name string) error {
	if err := v.secrets.Delete(ctx, name); err != nil {
		return fmt.Errorf("vault: cannot delete %q: %w", name, err)
	}
	return nil
}

// Rotate rewraps the data key from oldKey to newKey in one meta write.
// Secrets are untouched, so a crash leaves either the old wrap or the
// new one, never a mix. A serve already running keeps working until
// restart; it then needs newKey.
func Rotate(ctx context.Context, s store.Store, oldKey, newKey Key) error {
	if oldKey == newKey {
		return errors.New("vault: the new vault key equals the current one: generate a different key")
	}
	dek, err := loadDEK(ctx, s.Meta(), oldKey)
	if err != nil {
		return err
	}
	return storeDEK(ctx, s.Meta(), newKey, dek)
}

func loadDEK(ctx context.Context, m store.MetaRepo, k Key) ([32]byte, error) {
	raw, err := m.Get(ctx, dekMetaKey)
	if err != nil {
		return [32]byte{}, err
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != "v1" {
		return [32]byte{}, fmt.Errorf("vault: stored data key has an unknown format (meta %s); restore the store from a backup", dekMetaKey)
	}
	sealed, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return [32]byte{}, fmt.Errorf("vault: stored data key is corrupt (meta %s); restore the store from a backup", dekMetaKey)
	}
	kek, err := newAEAD(k[:])
	if err != nil {
		return [32]byte{}, err
	}
	dek, err := open(kek, sealed, dekAAD)
	if err != nil || len(dek) != 32 {
		return [32]byte{}, fmt.Errorf("%w: the vault was sealed with key id %s, but the configured key has id %s; set DEVCULES_VAULT_KEY (or DEVCULES_VAULT_KEY_FILE) to the original key, or restore its vault.key file", ErrWrongKey, parts[1], k.ID())
	}
	var out [32]byte
	copy(out[:], dek)
	return out, nil
}

func mintDEK(ctx context.Context, m store.MetaRepo, k Key) ([32]byte, error) {
	var dek [32]byte
	if _, err := rand.Read(dek[:]); err != nil {
		return [32]byte{}, fmt.Errorf("vault: cannot generate a data key: %w", err)
	}
	return dek, storeDEK(ctx, m, k, dek)
}

func storeDEK(ctx context.Context, m store.MetaRepo, k Key, dek [32]byte) error {
	kek, err := newAEAD(k[:])
	if err != nil {
		return err
	}
	sealed, err := seal(kek, dek[:], dekAAD)
	if err != nil {
		return fmt.Errorf("vault: cannot wrap the data key: %w", err)
	}
	value := "v1:" + k.ID() + ":" + base64.StdEncoding.EncodeToString(sealed)
	if err := m.Set(ctx, dekMetaKey, value); err != nil {
		return fmt.Errorf("vault: cannot store the data key: %w", err)
	}
	return nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	return aead, nil
}

// seal returns version || nonce || ciphertext, with aad bound in.
func seal(aead cipher.AEAD, plain []byte, aad string) ([]byte, error) {
	out := make([]byte, 1+aead.NonceSize(), 1+aead.NonceSize()+len(plain)+aead.Overhead())
	out[0] = sealVersion
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, err
	}
	return aead.Seal(out, out[1:], plain, []byte(aad)), nil
}

func open(aead cipher.AEAD, sealed []byte, aad string) ([]byte, error) {
	n := aead.NonceSize()
	if len(sealed) < 1+n+aead.Overhead() || sealed[0] != sealVersion {
		return nil, errors.New("sealed value malformed")
	}
	return aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(aad))
}
