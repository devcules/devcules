package redact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/devcules/devcules/internal/store"
)

// policyKey is where the policy lives in the store's meta repo.
const policyKey = "redact.policy"

// Settings is the live policy, kept in the store so it survives a
// restart. Reads are cheap; a change validates, saves, then swaps.
type Settings struct {
	meta   store.MetaRepo
	mu     sync.RWMutex
	policy Policy
}

// LoadSettings reads the saved policy, or starts from DefaultPolicy
// when none is saved. A type added after the policy was saved takes
// its default mode, so an upgrade never leaves a type unscanned by
// accident or scanned without consent.
func LoadSettings(ctx context.Context, meta store.MetaRepo) (*Settings, error) {
	p := DefaultPolicy()
	raw, err := meta.Get(ctx, policyKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, fmt.Errorf("redact: read policy: %w", err)
	default:
		var saved Policy
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return nil, fmt.Errorf("redact: saved policy (meta %s) does not parse: %w", policyKey, err)
		}
		for t, m := range saved {
			if _, known := p[t]; known {
				p[t] = m
			}
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("redact: saved policy (meta %s): %w", policyKey, err)
		}
	}
	return &Settings{meta: meta, policy: p}, nil
}

// Policy returns a copy of the live policy.
func (s *Settings) Policy() Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.policy)
}

// SetPolicy validates p, saves it, and makes it live. On any error
// the live policy is unchanged.
func (s *Settings) SetPolicy(ctx context.Context, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.meta.Set(ctx, policyKey, string(raw)); err != nil {
		return fmt.Errorf("redact: save policy: %w", err)
	}
	s.policy = maps.Clone(p)
	return nil
}
