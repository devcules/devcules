package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// memDB is an in-memory store.Store backend shared by the handles one Run
// needs: data outlives any single handle (close is a disconnect, not a
// delete), while closed handles stay closed.
type memDB struct {
	mu       sync.Mutex
	data     map[string]string
	users    map[string]string
	handles  map[string][]byte
	sessions map[string]store.Session
	passkeys map[string]store.Passkey
	secrets  map[string][]byte
	closed   map[*memStore]bool
}

type memStore struct {
	db *memDB
}

type memMeta struct {
	db *memDB
	s  *memStore
}

func newMemDB() *memDB {
	return &memDB{
		data:     map[string]string{},
		users:    map[string]string{},
		handles:  map[string][]byte{},
		sessions: map[string]store.Session{},
		passkeys: map[string]store.Passkey{},
		secrets:  map[string][]byte{},
		closed:   map[*memStore]bool{},
	}
}

func (s *memStore) Users() store.UsersRepo {
	return memUsers{db: s.db, s: s}
}

func (s *memStore) Sessions() store.SessionsRepo {
	return memSessions{db: s.db, s: s}
}

func (s *memStore) Passkeys() store.PasskeysRepo {
	return memPasskeys{db: s.db, s: s}
}

func (s *memStore) Secrets() store.SecretsRepo {
	return memSecrets{db: s.db, s: s}
}

type memSecrets struct {
	db *memDB
	s  *memStore
}

func (m memSecrets) Put(ctx context.Context, name string, sealed []byte, _ time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" || len(sealed) == 0 {
		return fmt.Errorf("store: secret name and value must not be empty")
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	m.db.secrets[name] = bytes.Clone(sealed)
	return nil
}

func (m memSecrets) Get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return nil, fmt.Errorf("store: memStore is closed")
	}
	v, ok := m.db.secrets[name]
	if !ok {
		return nil, fmt.Errorf("store: secret not found: %w", store.ErrNotFound)
	}
	return bytes.Clone(v), nil
}

func (m memSecrets) Names(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return nil, fmt.Errorf("store: memStore is closed")
	}
	names := make([]string, 0, len(m.db.secrets))
	for n := range m.db.secrets {
		names = append(names, n)
	}
	slices.Sort(names)
	return names, nil
}

func (m memSecrets) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	delete(m.db.secrets, name)
	return nil
}

type memUsers struct {
	db *memDB
	s  *memStore
}

func (m memUsers) Create(ctx context.Context, username, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if username == "" {
		return fmt.Errorf("store: username must not be empty")
	}
	if hash == "" {
		return fmt.Errorf("store: password hash must not be empty")
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	if _, ok := m.db.users[username]; ok {
		return fmt.Errorf("store: user %q already exists: %w", username, store.ErrExists)
	}
	m.db.users[username] = hash
	return nil
}

func (m memUsers) PasswordHash(ctx context.Context, username string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return "", fmt.Errorf("store: memStore is closed")
	}
	hash, ok := m.db.users[username]
	if !ok {
		return "", fmt.Errorf("store: user %q not found: %w", username, store.ErrNotFound)
	}
	return hash, nil
}

func (m memUsers) EnsureWebAuthnHandle(ctx context.Context, username string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return nil, fmt.Errorf("store: memStore is closed")
	}
	if _, ok := m.db.users[username]; !ok {
		return nil, fmt.Errorf("store: user %q not found: %w", username, store.ErrNotFound)
	}
	if handle, ok := m.db.handles[username]; ok {
		return append([]byte(nil), handle...), nil
	}
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return nil, fmt.Errorf("store: cannot mint webauthn handle: %w", err)
	}
	m.db.handles[username] = handle
	return append([]byte(nil), handle...), nil
}

type memSessions struct {
	db *memDB
	s  *memStore
}

func (m memSessions) Create(ctx context.Context, sess store.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	if _, ok := m.db.sessions[sess.TokenHash]; ok {
		return fmt.Errorf("store: session already exists: %w", store.ErrExists)
	}
	m.db.sessions[sess.TokenHash] = sess
	return nil
}

func (m memSessions) Get(ctx context.Context, hash string) (store.Session, error) {
	if err := ctx.Err(); err != nil {
		return store.Session{}, err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return store.Session{}, fmt.Errorf("store: memStore is closed")
	}
	sess, ok := m.db.sessions[hash]
	if !ok {
		return store.Session{}, fmt.Errorf("store: session not found: %w", store.ErrNotFound)
	}
	return sess, nil
}

func (m memSessions) Touch(ctx context.Context, hash string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	sess, ok := m.db.sessions[hash]
	if !ok {
		return fmt.Errorf("store: session not found: %w", store.ErrNotFound)
	}
	sess.LastSeen = at
	m.db.sessions[hash] = sess
	return nil
}

func (m memSessions) Delete(ctx context.Context, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	delete(m.db.sessions, hash)
	return nil
}

type memPasskeys struct {
	db *memDB
	s  *memStore
}

func (m memPasskeys) Create(ctx context.Context, p store.Passkey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case len(p.ID) == 0:
		return fmt.Errorf("store: passkey id must not be empty")
	case p.Username == "":
		return fmt.Errorf("store: passkey username must not be empty")
	case len(p.PublicKey) == 0:
		return fmt.Errorf("store: passkey public key must not be empty")
	case p.Name == "":
		return fmt.Errorf("store: passkey name must not be empty")
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	if _, ok := m.db.passkeys[string(p.ID)]; ok {
		return fmt.Errorf("store: passkey already exists: %w", store.ErrExists)
	}
	m.db.passkeys[string(p.ID)] = p
	return nil
}

func (m memPasskeys) ListByUser(ctx context.Context, username string) ([]store.Passkey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return nil, fmt.Errorf("store: memStore is closed")
	}
	var out []store.Passkey
	for _, p := range m.db.passkeys {
		if p.Username == username {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b store.Passkey) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return bytes.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m memPasskeys) Get(ctx context.Context, id []byte) (store.Passkey, error) {
	if err := ctx.Err(); err != nil {
		return store.Passkey{}, err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return store.Passkey{}, fmt.Errorf("store: memStore is closed")
	}
	p, ok := m.db.passkeys[string(id)]
	if !ok {
		return store.Passkey{}, fmt.Errorf("store: passkey not found: %w", store.ErrNotFound)
	}
	return p, nil
}

func (m memPasskeys) UpdateAfterLogin(ctx context.Context, id []byte, signCount uint32, backedUp bool, lastUsed time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	p, ok := m.db.passkeys[string(id)]
	if !ok {
		return fmt.Errorf("store: passkey not found: %w", store.ErrNotFound)
	}
	p.SignCount = signCount
	p.BackupState = backedUp
	p.LastUsedAt = lastUsed
	m.db.passkeys[string(id)] = p
	return nil
}

func (m memPasskeys) Rename(ctx context.Context, id []byte, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("store: passkey name must not be empty")
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	p, ok := m.db.passkeys[string(id)]
	if !ok {
		return fmt.Errorf("store: passkey not found: %w", store.ErrNotFound)
	}
	p.Name = name
	m.db.passkeys[string(id)] = p
	return nil
}

func (m memPasskeys) Delete(ctx context.Context, id []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	defer m.db.mu.Unlock()
	if m.db.closed[m.s] {
		return fmt.Errorf("store: memStore is closed")
	}
	delete(m.db.passkeys, string(id))
	return nil
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
		"Ping":         func() error { return s.Ping(ctx) },
		"Get":          func() error { _, err := s.Meta().Get(ctx, "k"); return err },
		"Set":          func() error { return s.Meta().Set(ctx, "k", "v") },
		"Delete":       func() error { return s.Meta().Delete(ctx, "k") },
		"CreateUser":   func() error { return s.Users().Create(ctx, "u", "h") },
		"PasswordHash": func() error { _, err := s.Users().PasswordHash(ctx, "u"); return err },
		"CreateSess":   func() error { return s.Sessions().Create(ctx, store.Session{}) },
		"GetSess":      func() error { _, err := s.Sessions().Get(ctx, "h"); return err },
		"TouchSess":    func() error { return s.Sessions().Touch(ctx, "h", time.Now()) },
		"DeleteSess":   func() error { return s.Sessions().Delete(ctx, "h") },
		"Handle":       func() error { _, err := s.Users().EnsureWebAuthnHandle(ctx, "u"); return err },
		"CreateKey": func() error {
			return s.Passkeys().Create(ctx, store.Passkey{ID: []byte("i"), Username: "u", PublicKey: []byte("p"), Name: "n"})
		},
		"GetKey":    func() error { _, err := s.Passkeys().Get(ctx, []byte("i")); return err },
		"ListKeys":  func() error { _, err := s.Passkeys().ListByUser(ctx, "u"); return err },
		"UpdateKey": func() error { return s.Passkeys().UpdateAfterLogin(ctx, []byte("i"), 1, false, time.Now()) },
		"RenameKey": func() error { return s.Passkeys().Rename(ctx, []byte("i"), "n") },
		"DeleteKey": func() error { return s.Passkeys().Delete(ctx, []byte("i")) },
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
