package vault

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/devcules/devcules/internal/store"
)

// pendingSuffix names the new key file a key-file rotation writes before
// it rewraps the data key. Its presence at start means a rotation was
// interrupted; FinishPendingRotation settles it.
const pendingSuffix = ".new"

// RotateKeyFile rotates a generated key file in place: it writes a fresh
// key beside path, rewraps the data key under it, then renames it over
// path. A crash at any step is settled by FinishPendingRotation on the
// next start. It returns the old and new key ids for the caller to print.
func RotateKeyFile(ctx context.Context, s store.Store, path string) (oldID, newID string, err error) {
	if err := FinishPendingRotation(ctx, s, path); err != nil {
		return "", "", err
	}
	oldKey, err := ReadKeyFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("vault key file %s missing: start serve once to create it, then rotate", path)
		}
		return "", "", err
	}
	newKey, err := NewKey()
	if err != nil {
		return "", "", err
	}
	pending := path + pendingSuffix
	if err := writeKeyFile(pending, newKey); err != nil {
		return "", "", err
	}
	if err := Rotate(ctx, s, oldKey, newKey); err != nil {
		os.Remove(pending)
		return "", "", err
	}
	if err := os.Rename(pending, path); err != nil {
		return "", "", fmt.Errorf("vault rotated, but %s could not replace %s (%s): rename it by hand before restarting serve", pending, path, err)
	}
	return oldKey.ID(), newKey.ID(), nil
}

// FinishPendingRotation settles a key-file rotation that stopped
// midway. If the pending key opens the data key, the rewrap committed,
// so the pending file replaces path; otherwise the rewrap never
// happened and the pending file is removed. With no pending file it
// does nothing.
func FinishPendingRotation(ctx context.Context, s store.Store, path string) error {
	pending := path + pendingSuffix
	newKey, err := ReadKeyFile(pending)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = loadDEK(ctx, s.Meta(), newKey)
	switch {
	case err == nil:
		if err := os.Rename(pending, path); err != nil {
			return fmt.Errorf("cannot finish the vault key rotation: rename %s to %s by hand (%s)", pending, path, err)
		}
		return nil
	case errors.Is(err, ErrWrongKey) || errors.Is(err, store.ErrNotFound):
		if err := os.Remove(pending); err != nil {
			return fmt.Errorf("cannot discard the unfinished vault key %s: remove it by hand (%s)", pending, err)
		}
		return nil
	default:
		return err
	}
}
