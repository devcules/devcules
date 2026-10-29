package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/vault"
)

// redactKeySecret holds the placeholder HMAC key. It is minted on first
// start and never leaves the vault and the Redactor.
const redactKeySecret = "redact/hmac_key"

// setupRedact loads (or mints) the placeholder key, compiles the
// detectors, runs the self-test, and loads the saved policy. Serve
// refuses to start when the self-test fails: an unredacted model call
// is worse than no model call. Logs carry counts and modes, never
// keys or values.
func setupRedact(ctx context.Context, v *vault.Vault, s store.Store, log *slog.Logger) (*redact.Redactor, *redact.Settings, error) {
	key, err := v.Get(ctx, redactKeySecret)
	if errors.Is(err, vault.ErrNotFound) {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
		if err := v.Put(ctx, redactKeySecret, key); err != nil {
			return nil, nil, err
		}
		log.Info("redaction key created", "secret", redactKeySecret)
	} else if err != nil {
		return nil, nil, err
	}
	r, err := redact.New(key)
	if err != nil {
		return nil, nil, err
	}
	if err := redact.SelfTest(r); err != nil {
		return nil, nil, fmt.Errorf("%w: serve will not start with broken redaction", err)
	}
	settings, err := redact.LoadSettings(ctx, s.Meta())
	if err != nil {
		return nil, nil, err
	}
	attrs := []any{"rules", r.Rules()}
	p := settings.Policy()
	for _, t := range redact.Types {
		attrs = append(attrs, string(t), string(p[t]))
	}
	log.Info("redaction self-test passed", attrs...)
	return r, settings, nil
}
