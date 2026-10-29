package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Key is a 32-byte vault key. It wraps the data key and nothing else.
type Key [32]byte

// NewKey returns a fresh random key.
func NewKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("vault: cannot generate a key: %w", err)
	}
	return k, nil
}

// ParseKey decodes a standard base64 key of exactly 32 bytes.
func ParseKey(b64 string) (Key, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return Key{}, errors.New("vault key invalid: not base64 (want base64 of 32 raw bytes)")
	}
	if len(raw) != len(Key{}) {
		return Key{}, fmt.Errorf("vault key invalid: decodes to %d bytes (want 32)", len(raw))
	}
	var k Key
	copy(k[:], raw)
	return k, nil
}

// Encode returns the key as standard base64, the form DEVCULES_VAULT_KEY
// takes.
func (k Key) Encode() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// ID is a short, non-secret fingerprint of the key: enough to tell two
// keys apart in an error message, useless for recovering the key.
func (k Key) ID() string {
	sum := sha256.Sum256(append([]byte("devcules vault key id\x00"), k[:]...))
	return hex.EncodeToString(sum[:6])
}

// LoadOrCreateKeyFile reads the key file at path, or creates it with a
// fresh key (0600, never overwriting) when missing. created reports a
// new file, so the caller can log the path; the key itself is never
// logged.
func LoadOrCreateKeyFile(path string) (k Key, created bool, err error) {
	k, err = ReadKeyFile(path)
	if err == nil {
		return k, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Key{}, false, err
	}
	k, err = NewKey()
	if err != nil {
		return Key{}, false, err
	}
	if err := writeKeyFile(path, k); err != nil {
		if errors.Is(err, fs.ErrExist) {
			// Another serve won the race: use its key, not ours.
			k, err = ReadKeyFile(path)
			return k, false, err
		}
		return Key{}, false, err
	}
	return k, true, nil
}

// ReadKeyFile reads a base64 key from path. A missing file wraps
// fs.ErrNotExist.
func ReadKeyFile(path string) (Key, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Key{}, fmt.Errorf("vault key file %s missing: %w", path, err)
		}
		return Key{}, fmt.Errorf("vault key file %s unreadable: check its owner and mode (%s)", path, err)
	}
	k, err := ParseKey(string(raw))
	if err != nil {
		return Key{}, fmt.Errorf("vault key file %s: %w", path, err)
	}
	return k, nil
}

// writeKeyFile creates path with k, mode 0600. It never overwrites: an
// existing file wraps fs.ErrExist.
func writeKeyFile(path string, k Key) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("vault key file %s already exists: %w", path, err)
		}
		return fmt.Errorf("cannot create vault key file %s: set DEVCULES_DATA_DIR to a writable path (%s)", path, err)
	}
	_, werr := f.WriteString(k.Encode() + "\n")
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(path)
		return fmt.Errorf("cannot write vault key file %s: %w", path, err)
	}
	return nil
}
