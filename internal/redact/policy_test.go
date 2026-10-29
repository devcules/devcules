package redact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/store"
)

type memMeta struct {
	m      map[string]string
	setErr error
}

func (f *memMeta) Get(_ context.Context, k string) (string, error) {
	v, ok := f.m[k]
	if !ok {
		return "", fmt.Errorf("%s: %w", k, store.ErrNotFound)
	}
	return v, nil
}

func (f *memMeta) Set(_ context.Context, k, v string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.m[k] = v
	return nil
}

func (f *memMeta) Delete(_ context.Context, k string) error { delete(f.m, k); return nil }

func TestSettingsDefaultAndPersist(t *testing.T) {
	ctx := context.Background()
	meta := &memMeta{m: map[string]string{}}
	s, err := LoadSettings(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	if s.Policy()[Secret] != Mask || s.Policy()[Email] != Off {
		t.Fatalf("default policy: %v", s.Policy())
	}
	p := s.Policy()
	p[Email] = Block
	if err := s.SetPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadSettings(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Policy()[Email] != Block {
		t.Fatalf("policy did not persist: %v", reloaded.Policy())
	}
	// The copy handed out is not the live map.
	got := reloaded.Policy()
	got[Email] = Off
	if reloaded.Policy()[Email] != Block {
		t.Fatal("Policy() returned the live map")
	}
}

func TestSettingsRejectsAndKeepsLive(t *testing.T) {
	ctx := context.Background()
	meta := &memMeta{m: map[string]string{}}
	s, _ := LoadSettings(ctx, meta)
	bad := s.Policy()
	bad[Phone] = "shout"
	if err := s.SetPolicy(ctx, bad); err == nil {
		t.Fatal("invalid policy accepted")
	}
	meta.setErr = errors.New("disk full")
	p := s.Policy()
	p[Card] = Mask
	if err := s.SetPolicy(ctx, p); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("save error: %v", err)
	}
	if s.Policy()[Card] != Off || s.Policy()[Phone] != Off {
		t.Fatalf("a failed change went live: %v", s.Policy())
	}
}

func TestSettingsSavedPolicy(t *testing.T) {
	ctx := context.Background()
	// A type the saved policy lacks takes its default; an unknown one
	// from a newer build is dropped.
	meta := &memMeta{m: map[string]string{policyKey: `{"SECRET":"block","EMAIL":"mask","FUTURE":"mask"}`}}
	s, err := LoadSettings(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Policy()
	if p[Secret] != Block || p[Email] != Mask || p[Card] != Off || len(p) != len(Types) {
		t.Fatalf("merged policy: %v", p)
	}
	for _, raw := range []string{`not json`, `{"SECRET":"loud"}`} {
		meta.m[policyKey] = raw
		if _, err := LoadSettings(ctx, meta); err == nil || !strings.Contains(err.Error(), "redact.policy") {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	broken := &brokenMeta{}
	if _, err := LoadSettings(ctx, broken); err == nil {
		t.Fatal("a store error was ignored")
	}
}

type brokenMeta struct{ memMeta }

func (*brokenMeta) Get(context.Context, string) (string, error) {
	return "", errors.New("store down")
}

func TestSelfTest(t *testing.T) {
	if err := SelfTest(newTestRedactor(t)); err != nil {
		t.Fatal(err)
	}
}

func TestSelfTestCatchesBrokenRules(t *testing.T) {
	r := newTestRedactor(t)
	r.rules = nil
	if err := SelfTest(r); err == nil || !strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("self-test passed with no secret rules: %v", err)
	}
	if st := r.Stats()[Email]; st != (Counts{}) {
		t.Fatalf("self-test touched the counters: %+v", st)
	}
}
