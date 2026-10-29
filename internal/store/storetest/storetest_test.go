package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"maps"
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
	mu        sync.Mutex
	data      map[string]string
	users     map[string]string
	handles   map[string][]byte
	sessions  map[string]store.Session
	passkeys  map[string]store.Passkey
	secrets   map[string][]byte
	launches  map[string]store.LaunchToken
	audit     []store.AuditEntry
	traces    []store.TraceCall
	approvals []store.Approval
	push      []store.PushSubscription
	machines  map[string]store.Machine
	enrol     map[string]store.EnrolCode
	projects  map[string]store.Project
	closed    map[*memStore]bool
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
		launches: map[string]store.LaunchToken{},
		machines: map[string]store.Machine{},
		enrol:    map[string]store.EnrolCode{},
		projects: map[string]store.Project{},
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

func (s *memStore) LaunchTokens() store.LaunchTokensRepo {
	return memLaunches{db: s.db, s: s}
}

func (s *memStore) Audit() store.AuditRepo {
	return memAudit{memLaunches{db: s.db, s: s}}
}

func (s *memStore) Traces() store.TracesRepo {
	return memTraces{memLaunches{db: s.db, s: s}}
}

func (s *memStore) Approvals() store.ApprovalsRepo {
	return memApprovals{memLaunches{db: s.db, s: s}}
}

func (s *memStore) PushSubscriptions() store.PushSubscriptionsRepo {
	return memPush{memLaunches{db: s.db, s: s}}
}
func (s *memStore) Machines() store.MachinesRepo     { return memMachines{memLaunches{db: s.db, s: s}} }
func (s *memStore) EnrolCodes() store.EnrolCodesRepo { return memEnrol{memLaunches{db: s.db, s: s}} }
func (s *memStore) Projects() store.ProjectsRepo     { return memProjects{memLaunches{db: s.db, s: s}} }

type memProjects struct{ memLaunches }

func (r memProjects) Create(ctx context.Context, p store.Project) error {
	if p.ID == "" || p.Name == "" || p.Target == "" || p.Dir == "" {
		return fmt.Errorf("store: project id/name/target/dir must not be empty")
	}
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	for _, o := range r.db.projects {
		if o.ID == p.ID || o.Name == p.Name {
			return fmt.Errorf("store: project exists: %w", store.ErrExists)
		}
	}
	r.db.projects[p.ID] = p
	return nil
}

func (r memProjects) Get(ctx context.Context, id string) (store.Project, error) {
	if err := r.lock(ctx); err != nil {
		return store.Project{}, err
	}
	defer r.db.mu.Unlock()
	p, ok := r.db.projects[id]
	if !ok {
		return store.Project{}, fmt.Errorf("store: project not found: %w", store.ErrNotFound)
	}
	return p, nil
}

func (r memProjects) List(ctx context.Context) ([]store.Project, error) {
	if err := r.lock(ctx); err != nil {
		return nil, err
	}
	defer r.db.mu.Unlock()
	var out []store.Project
	for _, p := range r.db.projects {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b store.Project) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (r memProjects) Delete(ctx context.Context, id string) error {
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	delete(r.db.projects, id)
	return nil
}

// memApprovals and memPush reuse memLaunches' lock too.
type memApprovals struct{ memLaunches }

func (m memApprovals) Create(ctx context.Context, a store.Approval) error {
	if a.ID == "" {
		return fmt.Errorf("store: approval id must not be empty")
	}
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	for _, o := range m.db.approvals {
		if o.ID == a.ID {
			return fmt.Errorf("store: approval exists: %w", store.ErrExists)
		}
	}
	m.db.approvals = append(m.db.approvals, a)
	return nil
}

func (m memApprovals) Get(ctx context.Context, id string) (store.Approval, error) {
	if err := m.lock(ctx); err != nil {
		return store.Approval{}, err
	}
	defer m.db.mu.Unlock()
	for _, a := range m.db.approvals {
		if a.ID == id {
			return a, nil
		}
	}
	return store.Approval{}, fmt.Errorf("store: approval not found: %w", store.ErrNotFound)
}

func (m memApprovals) Answer(ctx context.Context, id, status, by string, at time.Time) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	for i, a := range m.db.approvals {
		if a.ID != id {
			continue
		}
		if a.Status != store.ApprovalPending {
			return fmt.Errorf("store: approval answered: %w", store.ErrConflict)
		}
		m.db.approvals[i].Status, m.db.approvals[i].AnsweredBy, m.db.approvals[i].AnsweredAt = status, by, at
		return nil
	}
	return fmt.Errorf("store: approval not found: %w", store.ErrNotFound)
}

func (m memApprovals) List(ctx context.Context, status string, limit int) ([]store.Approval, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	out := []store.Approval{}
	// Insertion order breaks ties, newest first.
	for i := len(m.db.approvals) - 1; i >= 0; i-- {
		if a := m.db.approvals[i]; status == "" || a.Status == status {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b store.Approval) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m memApprovals) ExpirePending(ctx context.Context, before, at time.Time) ([]string, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	ids := []string{}
	for i, a := range m.db.approvals {
		if a.Status == store.ApprovalPending && a.CreatedAt.Before(before) {
			m.db.approvals[i].Status, m.db.approvals[i].AnsweredAt, m.db.approvals[i].AnsweredBy = store.ApprovalExpired, at, ""
			ids = append(ids, a.ID)
		}
	}
	return ids, nil
}

type memPush struct{ memLaunches }

func (m memPush) Put(ctx context.Context, s store.PushSubscription) error {
	if s.Endpoint == "" || len(s.P256dh) == 0 || len(s.Auth) == 0 {
		return fmt.Errorf("store: a push subscription needs an endpoint and both keys")
	}
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	for i, o := range m.db.push {
		if o.Endpoint == s.Endpoint {
			m.db.push[i] = s
			return nil
		}
	}
	m.db.push = append(m.db.push, s)
	return nil
}

func (m memPush) List(ctx context.Context) ([]store.PushSubscription, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	out := slices.Clone(m.db.push)
	slices.SortStableFunc(out, func(a, b store.PushSubscription) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if out == nil {
		out = []store.PushSubscription{}
	}
	return out, nil
}

func (m memPush) Delete(ctx context.Context, endpoint string) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	m.db.push = slices.DeleteFunc(m.db.push, func(s store.PushSubscription) bool { return s.Endpoint == endpoint })
	return nil
}

func (m memPush) Delivered(ctx context.Context, endpoint string, at time.Time) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	for i, s := range m.db.push {
		if s.Endpoint == endpoint {
			m.db.push[i].LastOKAt = at
			return nil
		}
	}
	return fmt.Errorf("store: push subscription not found: %w", store.ErrNotFound)
}

// memAudit and memTraces reuse memLaunches' lock.
type memAudit struct{ memLaunches }

func (m memAudit) Append(ctx context.Context, e store.AuditEntry) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	if e.Seq != int64(len(m.db.audit))+1 {
		return fmt.Errorf("store: audit seq %d is not next: %w", e.Seq, store.ErrConflict)
	}
	e.Detail = maps.Clone(e.Detail)
	if e.Detail == nil {
		e.Detail = map[string]string{}
	}
	m.db.audit = append(m.db.audit, e)
	return nil
}

func (m memAudit) Last(ctx context.Context) (store.AuditEntry, error) {
	if err := m.lock(ctx); err != nil {
		return store.AuditEntry{}, err
	}
	defer m.db.mu.Unlock()
	if len(m.db.audit) == 0 {
		return store.AuditEntry{}, fmt.Errorf("store: audit log is empty: %w", store.ErrNotFound)
	}
	return m.db.audit[len(m.db.audit)-1], nil
}

func (m memAudit) After(ctx context.Context, after int64, limit int) ([]store.AuditEntry, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	var out []store.AuditEntry
	for _, e := range m.db.audit {
		if e.Seq > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m memAudit) Before(ctx context.Context, before int64, limit int) ([]store.AuditEntry, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	var out []store.AuditEntry
	for i := len(m.db.audit) - 1; i >= 0; i-- {
		if e := m.db.audit[i]; (before <= 0 || e.Seq < before) && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

type memTraces struct{ memLaunches }

func (m memTraces) Add(ctx context.Context, c store.TraceCall) (int64, error) {
	if c.LaunchID == "" {
		return 0, fmt.Errorf("store: trace launch id must not be empty")
	}
	if err := m.lock(ctx); err != nil {
		return 0, err
	}
	defer m.db.mu.Unlock()
	c.ID = int64(len(m.db.traces)) + 1
	if c.Redacted == nil {
		c.Redacted = map[string]int{}
	}
	m.db.traces = append(m.db.traces, c)
	return c.ID, nil
}

func (m memTraces) Summaries(ctx context.Context, limit int) ([]store.TraceSession, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	var out []store.TraceSession
	idx := map[string]int{}
	for i := len(m.db.traces) - 1; i >= 0; i-- {
		c := m.db.traces[i]
		if c.LaunchID == "" {
			continue
		}
		j, ok := idx[c.LaunchID]
		if !ok {
			j = len(out)
			idx[c.LaunchID] = j
			out = append(out, store.TraceSession{LaunchID: c.LaunchID, First: c.At, Last: c.At})
		}
		s := &out[j]
		s.Calls++
		s.InputTokens += c.InputTokens
		s.OutputTokens += c.OutputTokens
		for _, n := range c.Redacted {
			s.Redacted += n
		}
		if c.At.Before(s.First) {
			s.First = c.At
		}
		if c.At.After(s.Last) {
			s.Last = c.At
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m memTraces) Calls(ctx context.Context, launchID string) ([]store.TraceCall, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	var out []store.TraceCall
	for _, c := range m.db.traces {
		if c.LaunchID == launchID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m memTraces) DeleteBefore(ctx context.Context, before time.Time) (int, error) {
	if err := m.lock(ctx); err != nil {
		return 0, err
	}
	defer m.db.mu.Unlock()
	n := 0
	for i, c := range m.db.traces {
		if c.LaunchID != "" && c.At.Before(before) {
			// Keep ids stable: a deleted call leaves a hole.
			m.db.traces[i] = store.TraceCall{ID: c.ID}
			n++
		}
	}
	return n, nil
}

type memLaunches struct {
	db *memDB
	s  *memStore
}

// lock takes the db lock and refuses a closed handle or a cancelled
// context; the caller unlocks.
func (m memLaunches) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.db.mu.Lock()
	if m.db.closed[m.s] {
		m.db.mu.Unlock()
		return fmt.Errorf("store: memStore is closed")
	}
	return nil
}

func (m memLaunches) Create(ctx context.Context, t store.LaunchToken) error {
	if t.ID == "" || len(t.TokenHash) == 0 {
		return fmt.Errorf("store: launch token id and hash must not be empty")
	}
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	if _, ok := m.db.launches[t.ID]; ok {
		return fmt.Errorf("store: launch token exists: %w", store.ErrExists)
	}
	for _, o := range m.db.launches {
		if bytes.Equal(o.TokenHash, t.TokenHash) {
			return fmt.Errorf("store: launch token exists: %w", store.ErrExists)
		}
	}
	t.TokenHash = bytes.Clone(t.TokenHash)
	m.db.launches[t.ID] = t
	return nil
}

func (m memLaunches) ByHash(ctx context.Context, hash []byte) (store.LaunchToken, error) {
	if err := m.lock(ctx); err != nil {
		return store.LaunchToken{}, err
	}
	defer m.db.mu.Unlock()
	for _, t := range m.db.launches {
		if bytes.Equal(t.TokenHash, hash) {
			return t, nil
		}
	}
	return store.LaunchToken{}, fmt.Errorf("store: launch token not found: %w", store.ErrNotFound)
}

func (m memLaunches) List(ctx context.Context) ([]store.LaunchToken, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.db.mu.Unlock()
	out := make([]store.LaunchToken, 0, len(m.db.launches))
	for _, t := range m.db.launches {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b store.LaunchToken) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m memLaunches) update(ctx context.Context, id string, f func(*store.LaunchToken)) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.db.mu.Unlock()
	t, ok := m.db.launches[id]
	if !ok {
		return fmt.Errorf("store: launch token not found: %w", store.ErrNotFound)
	}
	f(&t)
	m.db.launches[id] = t
	return nil
}

func (m memLaunches) Revoke(ctx context.Context, id string, at time.Time) error {
	return m.update(ctx, id, func(t *store.LaunchToken) {
		if t.RevokedAt.IsZero() {
			t.RevokedAt = at.UTC().Truncate(time.Second)
		}
	})
}

func (m memLaunches) Touch(ctx context.Context, id string, at time.Time) error {
	return m.update(ctx, id, func(t *store.LaunchToken) { t.LastUsedAt = at.UTC().Truncate(time.Second) })
}

func (m memLaunches) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	if err := m.lock(ctx); err != nil {
		return 0, err
	}
	defer m.db.mu.Unlock()
	n := 0
	for id, t := range m.db.launches {
		if t.ExpiresAt.Before(before) {
			delete(m.db.launches, id)
			n++
		}
	}
	return n, nil
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

type memMachines struct{ memLaunches }

func (r memMachines) Create(ctx context.Context, m store.Machine) error {
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	for _, o := range r.db.machines {
		if o.ID == m.ID || o.Name == m.Name {
			return fmt.Errorf("store: machine exists: %w", store.ErrExists)
		}
	}
	r.db.machines[m.ID] = m
	return nil
}
func (r memMachines) Get(ctx context.Context, id string) (store.Machine, error) {
	if err := r.lock(ctx); err != nil {
		return store.Machine{}, err
	}
	defer r.db.mu.Unlock()
	m, ok := r.db.machines[id]
	if !ok {
		return store.Machine{}, fmt.Errorf("store: machine not found: %w", store.ErrNotFound)
	}
	return m, nil
}
func (r memMachines) GetByName(ctx context.Context, name string) (store.Machine, error) {
	if err := r.lock(ctx); err != nil {
		return store.Machine{}, err
	}
	defer r.db.mu.Unlock()
	for _, m := range r.db.machines {
		if m.Name == name {
			return m, nil
		}
	}
	return store.Machine{}, fmt.Errorf("store: machine name not found: %w", store.ErrNotFound)
}
func (r memMachines) List(ctx context.Context) ([]store.Machine, error) {
	if err := r.lock(ctx); err != nil {
		return nil, err
	}
	defer r.db.mu.Unlock()
	out := make([]store.Machine, 0, len(r.db.machines))
	for _, m := range r.db.machines {
		out = append(out, m)
	}
	return out, nil
}
func (r memMachines) Touch(ctx context.Context, id string, at time.Time) error {
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	m := r.db.machines[id]
	if m.ID == "" {
		return fmt.Errorf("store: machine not found: %w", store.ErrNotFound)
	}
	m.LastSeen = at
	r.db.machines[id] = m
	return nil
}
func (r memMachines) Delete(ctx context.Context, id string) error {
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	delete(r.db.machines, id)
	return nil
}

type memEnrol struct{ memLaunches }

func (r memEnrol) Create(ctx context.Context, c store.EnrolCode) error {
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	for _, o := range r.db.enrol {
		if string(o.CodeHash) == string(c.CodeHash) || o.ID == c.ID {
			return fmt.Errorf("store: enrol exists: %w", store.ErrExists)
		}
	}
	r.db.enrol[c.ID] = c
	return nil
}
func (r memEnrol) GetByHash(ctx context.Context, hash []byte) (store.EnrolCode, error) {
	if err := r.lock(ctx); err != nil {
		return store.EnrolCode{}, err
	}
	defer r.db.mu.Unlock()
	for _, c := range r.db.enrol {
		if string(c.CodeHash) == string(hash) {
			return c, nil
		}
	}
	return store.EnrolCode{}, fmt.Errorf("store: enrol not found: %w", store.ErrNotFound)
}
func (r memEnrol) Use(ctx context.Context, id string, at time.Time) error {
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.db.mu.Unlock()
	c := r.db.enrol[id]
	if c.ID == "" || !c.UsedAt.IsZero() {
		return fmt.Errorf("store: enrol use: %w", store.ErrExists)
	}
	c.UsedAt = at
	r.db.enrol[id] = c
	return nil
}
func (r memEnrol) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	if err := r.lock(ctx); err != nil {
		return 0, err
	}
	defer r.db.mu.Unlock()
	n := 0
	for k, c := range r.db.enrol {
		if !c.UsedAt.IsZero() {
			continue
		}
		if c.ExpiresAt.Before(before) {
			delete(r.db.enrol, k)
			n++
		}
	}
	return n, nil
}
func (r memEnrol) List(_ context.Context) ([]store.EnrolCode, error) {
	if err := r.lock(context.TODO()); err != nil { // the list ignores ctx, but lock must see a non-nil one
		return nil, err
	}
	defer r.db.mu.Unlock()
	out := make([]store.EnrolCode, 0, len(r.db.enrol))
	for _, c := range r.db.enrol {
		out = append(out, c)
	}
	return out, nil
}
