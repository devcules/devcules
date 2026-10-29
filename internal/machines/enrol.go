package machines

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// EnrolTTL is how long a minted code stays redeemable (PLAN.md §4.7).
const EnrolTTL = 15 * time.Minute

// codeAlphabet is Crockford base32: no I, L, O or U, so a code read
// aloud or typed from a phone has no look-alike characters.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// codeLen is the PLAN's 12-character code: 60 bits, single use, 15
// minutes, and the enrol endpoint is rate limited.
const codeLen = 12

var (
	// ErrBadCode covers unknown, used and expired codes alike: the
	// caller learns nothing about which codes exist.
	ErrBadCode = errors.New("machines: bad or expired enrolment code")
	// ErrNameTaken means another machine is enrolled under the name.
	ErrNameTaken = errors.New("machines: name already enrolled")
	// ErrBadMachine covers a malformed name, key or proof.
	ErrBadMachine = errors.New("machines: bad name, key or signature")
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName reports a usable machine name: it is the link name, so
// it never holds "/" (the hub's machine address separator).
func ValidName(name string) bool { return nameRE.MatchString(name) }

// NormalizeCode upper-cases a typed code and drops the dashes and
// spaces people add, so "abcd-efgh-jkmn" redeems "ABCDEFGHJKMN".
func NormalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, code)
}

// EnrolMessage is what a machine signs to prove it holds the key it
// enrols: a fixed context string plus the normalized code, so the
// signature can never be replayed as a link challenge answer.
func EnrolMessage(code string) []byte {
	return []byte("devcules-enrol-v1\n" + NormalizeCode(code))
}

// Issuer mints enrolment codes (serve side).
type Issuer struct {
	db  store.Store
	now func() time.Time
}

func NewIssuer(s store.Store) *Issuer { return &Issuer{db: s, now: time.Now} }

// Mint returns a one-time code, shown once as XXXX-XXXX-XXXX; only
// its hash is stored.
func (m *Issuer) Mint(ctx context.Context, label string) (string, error) {
	raw := make([]byte, codeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	b := make([]byte, codeLen)
	for i, v := range raw {
		b[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	code := string(b)
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
	return code[0:4] + "-" + code[4:8] + "-" + code[8:12], nil
}

// Redeem enrols a machine: sig must be pubkey's signature over
// EnrolMessage(code), the code must be unused and unexpired, and the
// name free. The code is claimed before the machine row is written,
// so two racing redeems can never both enrol; a taken name burns the
// code (mint another).
func (m *Issuer) Redeem(ctx context.Context, code string, pubkey, sig []byte, name string) (*store.Machine, error) {
	if !ValidName(name) || len(pubkey) != ed25519.PublicKeySize || !ed25519.Verify(pubkey, EnrolMessage(code), sig) {
		return nil, ErrBadMachine
	}
	h := sha256.Sum256([]byte(NormalizeCode(code)))
	c, err := m.db.EnrolCodes().GetByHash(ctx, h[:])
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrBadCode
		}
		return nil, err
	}
	now := m.now().UTC()
	if !c.UsedAt.IsZero() || c.ExpiresAt.Before(now) {
		return nil, ErrBadCode
	}
	if err := m.db.EnrolCodes().Use(ctx, c.ID, now); err != nil {
		if errors.Is(err, store.ErrExists) {
			return nil, ErrBadCode
		}
		return nil, err
	}
	mach := store.Machine{ID: "m_" + c.ID, Name: name, PubKey: pubkey, EnrolledAt: now}
	if err := m.db.Machines().Create(ctx, mach); err != nil {
		if errors.Is(err, store.ErrExists) {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	return &mach, nil
}

// Service ties the machine store to the link handshake and the
// Machines tab.
type Service struct {
	db  store.Store
	now func() time.Time
}

func NewService(s store.Store) *Service { return &Service{db: s, now: time.Now} }

func (s *Service) GetMachineByName(ctx context.Context, name string) (*store.Machine, error) {
	m, err := s.db.Machines().GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// VerifyMachine answers the hub's challenge check: sig must be the
// enrolled key's signature over the nonce. A pass records last seen.
func (s *Service) VerifyMachine(ctx context.Context, name, nonce string, sig []byte) error {
	m, err := s.db.Machines().GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("machines: %q not enrolled", name)
	}
	if err := Verify(nonce, m.PubKey, sig); err != nil {
		return err
	}
	_ = s.db.Machines().Touch(ctx, m.ID, s.now().UTC())
	return nil
}

// List returns every enrolled machine, linked or not.
func (s *Service) List(ctx context.Context) ([]store.Machine, error) {
	return s.db.Machines().List(ctx)
}

// Remove revokes a machine by name: its key stops verifying, so the
// next connect fails and it has to enrol again. Missing is not an
// error.
func (s *Service) Remove(ctx context.Context, name string) error {
	m, err := s.db.Machines().GetByName(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	return s.db.Machines().Delete(ctx, m.ID)
}
