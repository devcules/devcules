package storetest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/devcules/devcules/internal/store"
)

// memDB is an in-memory store.Store backend shared by the handles one Run
// needs: data outlives any single handle (close is a disconnect, not a
// delete), while closed handles stay closed.
type memDB struct {
	mu     sync.Mutex
	data   map[string]string
	closed map[*memStore]bool
}

type memStore struct {
	db *memDB
}

type memMeta struct {
	db *memDB
	s  *memStore
}

func newMemDB() *memDB {
	return &memDB{data: map[string]string{}, closed: map[*memStore]bool{}}
}

func (s *memStore) failIfClosed() error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.closed[s] {
		return fmt.Errorf("store: memStore is closed")
	}
	return nil
}

func (s *memStore) Ping(ctx context.Context) error {
	if err := s.failIfClosed(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *memStore) Close() error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.closed[s] = true
	return nil
}

func (s *memStore) SchemaVersion(context.Context) (int, error) {
	return 1, nil
}

func (s *memStore) Meta() store.MetaRepo {
	return memMeta{db: s.db, s: s}
}

func (m memMeta) Get(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return "", fmt.Errorf("store: memStore is closed")
	}
	v, ok := m.db.data[key]
	if !ok {
		return "", fmt.Errorf("store: meta key %q not found: %w", key, store.ErrNotFound)
	}
	return v, nil
}

func (m memMeta) Set(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("store: meta key must not be empty")
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	m.db.data[key] = value
	return nil
}

func (m memMeta) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	delete(m.db.data, key)
	return nil
}

// TestMemStoreClosedHandles pins the fake's closed-handle behaviour, which
// the contract relies on for Ping-after-Close, and covers those branches.
func TestMemStoreClosedHandles(t *testing.T) {
	ctx := context.Background()
	s := &memStore{db: newMemDB()}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	ops := map[string]func() error{
		"Ping":   func() error { return s.Ping(ctx) },
		"Get":    func() error { _, err := s.Meta().Get(ctx, "k"); return err },
		"Set":    func() error { return s.Meta().Set(ctx, "k", "v") },
		"Delete": func() error { return s.Meta().Delete(ctx, "k") },
	}
	for name, op := range ops {
		if err := op(); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("%s after Close = %v, want a closed error", name, err)
		}
	}
}

// TestRunPassesOnFake proves the suite accepts a correct Store.
func TestRunPassesOnFake(t *testing.T) {
	db := newMemDB()
	Run(t, func(t *testing.T) store.Store {
		t.Helper()
		return &memStore{db: db}
	})
}

// brokenStore answers every Get with stale data: the suite must fail it.
type brokenStore struct {
	store.Store
}

type brokenMeta struct {
	store.MetaRepo
}

func (b brokenMeta) Get(context.Context, string) (string, error) {
	return "stale", nil
}

func (b brokenStore) Meta() store.MetaRepo {
	return brokenMeta{MetaRepo: b.Store.Meta()}
}

// TestBrokenProbe runs the suite against the broken store. It only runs
// inside the child process started by TestRunCatchesBroken: a failed
// subtest would taint the parent, so the failure must cross a process
// boundary to be assertable.
func TestBrokenProbe(t *testing.T) {
	if os.Getenv("DEVCULES_CONTRACT_PROBE") != "1" {
		t.Skip("child-process probe; run via TestRunCatchesBroken")
	}
	db := newMemDB()
	Run(t, func(t *testing.T) store.Store {
		t.Helper()
		return brokenStore{Store: &memStore{db: db}}
	})
}

// TestRunCatchesBroken proves the suite rejects a wrong Store: the probe
// process must fail, and its output must show the suite itself failing
// (not a build or setup error, which would be a vacuous pass).
func TestRunCatchesBroken(t *testing.T) {
	cmd := exec.Command("go", "test", "-run", "TestBrokenProbe$", "-count=1", ".")
	cmd.Env = append(os.Environ(), "DEVCULES_CONTRACT_PROBE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("contract probe passed on a broken store, want failure:\n%s", out)
	}
	if !strings.Contains(string(out), "FAIL: TestBrokenProbe") {
		t.Fatalf("probe failed without the suite running:\n%s", out)
	}
}
