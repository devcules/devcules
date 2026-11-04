package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestLimitKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"203.0.113.9", "203.0.113.9"},
		{"::ffff:203.0.113.9", "203.0.113.9"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"::1", "::/64"},
		{"fe80::1%eth0", "fe80::/64"},
		{"not-an-ip", "not-an-ip"},
		{"", ""},
	} {
		if got := limitKey(tc.in); got != tc.want {
			t.Errorf("limitKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Rotating IPv6 addresses inside one /64 buys no extra password
// guesses; a neighbouring /64 is a different client.
func TestSignInLimiterIPv6Per64(t *testing.T) {
	svc := New(openStore(t))
	password := mustBootstrap(t, svc)
	ctx := context.Background()
	for i := range MaxSignInFailures {
		ip := fmt.Sprintf("2001:db8:aa:1::%x", i+1)
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d = %v", i+1, err)
		}
	}
	if _, err := svc.SignIn(ctx, AdminUsername, "wrong", "2001:db8:aa:1:dead:beef:0:1"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("fresh address in the same /64 = %v, want ErrTooManyAttempts", err)
	}
	if _, err := svc.SignIn(ctx, AdminUsername, password, "2001:db8:aa:2::1"); err != nil {
		t.Fatalf("another /64 = %v, want a sign-in", err)
	}
	// A success clears the whole /64, as it clears an IPv4 address.
	if _, err := svc.SignIn(ctx, AdminUsername, "wrong", "2001:db8:aa:3::1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal(err)
	}
	if _, err := svc.SignIn(ctx, AdminUsername, password, "2001:db8:aa:3::2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.failures["2001:db8:aa:3::/64"]; ok {
		t.Fatal("success left the /64's failures behind")
	}
}

// Login begins and live login ceremonies are counted per /64 too.
func TestLoginBeginIPv6Per64(t *testing.T) {
	svc := New(openStore(t), WithPasskeys("http://localhost:7659", nil))
	mustBootstrap(t, svc)
	b6Clock(svc)
	for i := range maxLoginBegins {
		if _, _, err := svc.BeginLogin(fmt.Sprintf("2001:db8:bb:1::%x", i+1)); err != nil {
			t.Fatalf("begin %d = %v", i+1, err)
		}
	}
	if _, _, err := svc.BeginLogin("2001:db8:bb:1:1:2:3:4"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("fresh address in the same /64 = %v, want ErrTooManyAttempts", err)
	}
	live := 0
	for _, c := range svc.ceremonies {
		if c.ip == "2001:db8:bb:1::/64" {
			live++
		}
	}
	if live > maxLoginCeremoniesPerIP {
		t.Fatalf("one /64 holds %d live ceremonies, want ≤ %d", live, maxLoginCeremoniesPerIP)
	}
	if _, _, err := svc.BeginLogin("2001:db8:bb:2::1"); err != nil {
		t.Fatalf("another /64 = %v", err)
	}
}

// Passkey finishes share the per-/64 failure count with passwords.
func TestPasskeyFinishIPv6Per64(t *testing.T) {
	svc := New(openStore(t), WithPasskeys("http://localhost:7659", nil))
	mustBootstrap(t, svc)
	ctx := context.Background()
	for i := range MaxSignInFailures {
		_, _ = svc.SignIn(ctx, AdminUsername, "wrong", fmt.Sprintf("2001:db8:cc:1::%x", i+1))
	}
	if _, err := svc.FinishLogin(ctx, "no-such-ceremony", []byte(`{}`), "2001:db8:cc:1::99"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("passkey finish from the same /64 = %v, want ErrTooManyAttempts", err)
	}
}
