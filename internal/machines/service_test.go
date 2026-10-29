package machines

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// newStore opens a throwaway sqlite store for issuer tests.
func newStore(t *testing.T) store.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// machineKey is one machine's identity plus its enrolment proof.
type machineKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newKey(t *testing.T) machineKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return machineKey{pub, priv}
}

func (k machineKey) proof(code string) []byte { return ed25519.Sign(k.priv, EnrolMessage(code)) }

func TestMintCodeShape(t *testing.T) {
	iss := NewIssuer(newStore(t))
	code, err := iss.Mint(context.Background(), "kitchen")
	if err != nil {
		t.Fatalf("Mint: %s", err)
	}
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`).MatchString(code) {
		t.Fatalf("code = %q, want XXXX-XXXX-XXXX in Crockford base32", code)
	}
}

func TestRedeemRejectsBadInput(t *testing.T) {
	s := newStore(t)
	iss := NewIssuer(s)
	ctx := context.Background()
	k := newKey(t)
	code, err := iss.Mint(ctx, "kitchen")
	if err != nil {
		t.Fatalf("Mint: %s", err)
	}
	cases := []struct {
		name, code, machine string
		pub, sig            []byte
		want                error
	}{
		{"unknown code", "ZZZZ-ZZZZ-ZZZZ", "m1", k.pub, k.proof("ZZZZ-ZZZZ-ZZZZ"), ErrBadCode},
		{"short key", code, "m1", []byte("pub"), k.proof(code), ErrBadMachine},
		{"empty name", code, "", k.pub, k.proof(code), ErrBadMachine},
		{"slash in name", code, "a/b", k.pub, k.proof(code), ErrBadMachine},
		{"proof for another code", code, "m1", k.pub, k.proof("ZZZZ-ZZZZ-ZZZZ"), ErrBadMachine},
		{"proof by another key", code, "m1", k.pub, newKey(t).proof(code), ErrBadMachine},
	}
	for _, c := range cases {
		if _, err := iss.Redeem(ctx, c.code, c.pub, c.sig, c.machine); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	// None of the rejections burned the code; lower case and no
	// dashes redeem it.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	m, err := iss.Redeem(ctx, typed, k.pub, k.proof(typed), "kitchen-mac")
	if err != nil {
		t.Fatalf("Redeem: %s", err)
	}
	if m.Name != "kitchen-mac" {
		t.Fatalf("machine = %+v", m)
	}
	if _, err := iss.Redeem(ctx, code, k.pub, k.proof(code), "again"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("reuse: err = %v, want ErrBadCode", err)
	}
}

func TestRedeemExpired(t *testing.T) {
	iss := NewIssuer(newStore(t))
	k := newKey(t)
	iss.now = func() time.Time { return time.Unix(0, 0).UTC() }
	code, err := iss.Mint(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	iss.now = func() time.Time { return time.Unix(0, 0).UTC().Add(EnrolTTL + time.Second) }
	if _, err := iss.Redeem(context.Background(), code, k.pub, k.proof(code), "late"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("err = %v, want ErrBadCode", err)
	}
}

func TestRedeemNameTaken(t *testing.T) {
	iss := NewIssuer(newStore(t))
	ctx := context.Background()
	for i, want := range []error{nil, ErrNameTaken} {
		k := newKey(t)
		code, err := iss.Mint(ctx, "lab")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := iss.Redeem(ctx, code, k.pub, k.proof(code), "lab-top"); !errors.Is(err, want) {
			t.Fatalf("redeem %d: err = %v, want %v", i, err, want)
		}
	}
}

// Racing redeems of one code enrol exactly one machine.
func TestRedeemRace(t *testing.T) {
	s := newStore(t)
	iss := NewIssuer(s)
	ctx := context.Background()
	code, err := iss.Mint(ctx, "race")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k := newKey(t)
			if _, err := iss.Redeem(ctx, code, k.pub, k.proof(code), "racer-"+string(rune('a'+i))); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	all, err := s.Machines().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wins != 1 || len(all) != 1 {
		t.Fatalf("wins = %d, machines = %d, want 1 and 1", wins, len(all))
	}
}

func TestServiceVerifyAndRemove(t *testing.T) {
	s := newStore(t)
	iss := NewIssuer(s)
	svc := NewService(s)
	ctx := context.Background()
	k := newKey(t)
	code, err := iss.Mint(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Redeem(ctx, code, k.pub, k.proof(code), "lab-top"); err != nil {
		t.Fatal(err)
	}
	if m, err := svc.GetMachineByName(ctx, "lab-top"); err != nil || m.Name != "lab-top" {
		t.Fatalf("GetMachineByName: %v %+v", err, m)
	}
	nonce, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(nonce)
	if err := svc.VerifyMachine(ctx, "lab-top", nonce, ed25519.Sign(k.priv, raw)); err != nil {
		t.Fatalf("VerifyMachine: %s", err)
	}
	if m, _ := svc.GetMachineByName(ctx, "lab-top"); m.LastSeen.IsZero() {
		t.Fatal("a verified link did not record last seen")
	}
	if err := svc.VerifyMachine(ctx, "lab-top", nonce, ed25519.Sign(newKey(t).priv, raw)); err == nil {
		t.Fatal("another key verified")
	}
	if err := svc.Remove(ctx, "lab-top"); err != nil {
		t.Fatalf("Remove: %s", err)
	}
	if err := svc.VerifyMachine(ctx, "lab-top", nonce, ed25519.Sign(k.priv, raw)); err == nil {
		t.Fatal("a removed machine still verifies")
	}
	if err := svc.Remove(ctx, "lab-top"); err != nil {
		t.Fatalf("second Remove: %s", err)
	}
}
