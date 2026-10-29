package machines

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/devcules/devcules/internal/store"
)

const EnrolTTL = 10 * time.Minute

// Issuer mints enrolment codes (serve side).
type Issuer struct {
	db  store.Store
	now func() time.Time
}

func NewIssuer(s store.Store) *Issuer { return &Issuer{db: s, now: time.Now} }

// Mint returns a one-time code (shown once; only the hash is stored).
func (m *Issuer) Mint(ctx context.Context, label string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := "enr_" + base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(code))
	now := m.now().UTC()
	if err := m.db.EnrolCodes().Create(ctx, store.EnrolCode{
		ID:        fmt.Sprintf("ec_%d", now.UnixNano()),
		CodeHash:  h[:],
		Label:     label,
		CreatedAt: now,
		ExpiresAt: now.Add(EnrolTTL),
	}); err != nil {
		return "", fmt.Errorf("machines: mint enrol: %w", err)
	}
	return code, nil
}

// Redeem validates a code (single-use, not expired), marks it used, and stores the machine pubkey.
// Caller already checked the challenge signature via Verify().
func (m *Issuer) Redeem(ctx context.Context, code string, pubkey []byte, name string) (*store.Machine, error) {
	h := sha256.Sum256([]byte(code))
	c, err := m.db.EnrolCodes().GetByHash(ctx, h[:])
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("machines: bad enrol code")
		}
		return nil, err
	}
	now := m.now().UTC()
	if !c.UsedAt.IsZero() {
		return nil, fmt.Errorf("machines: enrol already used")
	}
	if c.ExpiresAt.Before(now) {
		return nil, fmt.Errorf("machines: enrol expired")
	}
	if len(pubkey) == 0 || name == "" {
		return nil, fmt.Errorf("machines: name/pubkey required")
	}
	mach := store.Machine{ID: c.ID, Name: name, PubKey: pubkey, EnrolledAt: now}
	if err := m.db.Machines().Create(ctx, mach); err != nil {
		return nil, err
	}
	if err := m.db.EnrolCodes().Use(ctx, c.ID, now); err != nil {
		return nil, err
	}
	return &mach, nil
}

// Service ties issuer + machine store for link handshake lookups.
type Service struct{ db store.Store }

func NewService(s store.Store) *Service { return &Service{db: s} }

func (s *Service) GetMachineByName(ctx context.Context, name string) (*store.Machine, error) {
	m, err := s.db.Machines().GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
