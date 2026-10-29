package machines

import (
	"context"
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

func TestRedeemRejectsBadInput(t *testing.T) {
	s := newStore(t)
	iss := NewIssuer(s)
	ctx := context.Background()

	// Mint a valid code, then exercise every rejection path.
	code, err := iss.Mint(ctx, "kitchen")
	if err != nil {
		t.Fatalf("Mint: %s", err)
	}
	if len(code) < 20 || code[:4] != "enr_" {
		t.Fatalf("code shape: %q", code)
	}
	// Unknown code.
	if _, err := iss.Redeem(ctx, "enr_nope", []byte("pub"), "m1"); err == nil {
		t.Fatal("unknown code redeemed")
	}
	// Empty pubkey and name are rejected even with a live code.
	if _, err := iss.Redeem(ctx, code, nil, "m1"); err == nil {
		t.Fatal("empty pubkey accepted")
	}
	if _, err := iss.Redeem(ctx, code, []byte("pub"), ""); err == nil {
		t.Fatal("empty name accepted")
	}
	// A fresh, unexpired code redeems.
	m, err := iss.Redeem(ctx, code, []byte("pub"), "kitchen-mac")
	if err != nil {
		t.Fatalf("Redeem: %s", err)
	}
	if m.Name != "kitchen-mac" {
		t.Fatalf("machine = %+v", m)
	}
	// And only once.
	if _, err := iss.Redeem(ctx, code, []byte("pub"), "again"); err == nil {
		t.Fatal("code reused")
	}
}

func TestRedeemExpired(t *testing.T) {
	s := newStore(t)
	iss := NewIssuer(s)
	// Wind the clock: mint at t0, then redeem past the TTL.
	iss.now = func() time.Time { return time.Unix(0, 0).UTC() }
	code, err := iss.Mint(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	iss.now = func() time.Time { return time.Unix(0, 0).UTC().Add(EnrolTTL + time.Second) }
	if _, err := iss.Redeem(context.Background(), code, []byte("pub"), "late"); err == nil {
		t.Fatal("expired code redeemed")
	}
}

func TestServiceGetMachineByName(t *testing.T) {
	s := newStore(t)
	iss := NewIssuer(s)
	svc := NewService(s)
	ctx := context.Background()

	code, err := iss.Mint(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Redeem(ctx, code, []byte("pub"), "lab-top"); err != nil {
		t.Fatal(err)
	}
	m, err := svc.GetMachineByName(ctx, "lab-top")
	if err != nil {
		t.Fatalf("GetMachineByName: %s", err)
	}
	if m.Name != "lab-top" {
		t.Fatalf("machine = %+v", m)
	}
	if _, err := svc.GetMachineByName(ctx, "nope"); err == nil {
		t.Fatal("missing machine found")
	}
}
