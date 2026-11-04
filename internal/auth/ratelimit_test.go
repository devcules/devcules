package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// b6Clock pins svc.now to a movable instant for window tests.
func b6Clock(svc *Service) *time.Time {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	return &now
}

// A concurrent burst of wrong guesses from one IP gets at most
// MaxSignInFailures password checks per window (GOSEC-03): each
// attempt reserves its failure before the argon2id wait.
func TestSignInLimiterBurst(t *testing.T) {
	svc := New(openStore(t))
	mustBootstrap(t, svc)
	const n = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked, throttled := 0, 0
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.SignIn(context.Background(), AdminUsername, "wrong-guess", "203.0.113.9")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrInvalidCredentials):
				checked++
			case errors.Is(err, ErrTooManyAttempts):
				throttled++
			default:
				t.Errorf("SignIn = %v", err)
			}
		}()
	}
	wg.Wait()
	if checked > MaxSignInFailures {
		t.Fatalf("%d of %d concurrent guesses from one IP were password-checked; limit is %d per %s", checked, n, MaxSignInFailures, SignInWindow)
	}
	if checked+throttled != n {
		t.Fatalf("checked %d + throttled %d != %d", checked, throttled, n)
	}
}

// The owner is never locked out by their own typos: a correct
// password after fewer than MaxSignInFailures failures signs in and
// clears the count, and attempts that never reached a verdict are
// refunded rather than counted.
func TestSignInReservationRefund(t *testing.T) {
	svc := New(openStore(t))
	password := mustBootstrap(t, svc)
	ctx := context.Background()
	const ip = "203.0.113.10"
	for i := range MaxSignInFailures - 1 {
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d = %v", i+1, err)
		}
	}
	// A cancelled wait is refunded: it must not be the fifth failure.
	svc.argon <- struct{}{}
	svc.argon <- struct{}{}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := svc.SignIn(cancelled, AdminUsername, "wrong", ip); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SignIn = %v", err)
	}
	<-svc.argon
	<-svc.argon
	if got := len(svc.failures[ip]); got != MaxSignInFailures-1 {
		t.Fatalf("%d failures after a refunded attempt, want %d", got, MaxSignInFailures-1)
	}
	if _, err := svc.SignIn(ctx, AdminUsername, password, ip); err != nil {
		t.Fatalf("correct password after %d failures = %v", MaxSignInFailures-1, err)
	}
	if _, ok := svc.failures[ip]; ok {
		t.Fatal("success left failures behind")
	}
}

// Throttling reports ErrTooManyAttempts once per IP per window and
// ErrThrottledAgain after, so callers audit it once (GOSEC-04). Both
// match ErrTooManyAttempts.
func TestThrottleReportedOncePerWindow(t *testing.T) {
	svc := New(openStore(t))
	mustBootstrap(t, svc)
	now := b6Clock(svc)
	ctx := context.Background()
	const ip = "203.0.113.11"
	for range MaxSignInFailures {
		_, _ = svc.SignIn(ctx, AdminUsername, "wrong", ip)
	}
	_, err := svc.SignIn(ctx, AdminUsername, "wrong", ip)
	if !errors.Is(err, ErrTooManyAttempts) || errors.Is(err, ErrThrottledAgain) {
		t.Fatalf("first throttled = %v, want ErrTooManyAttempts only", err)
	}
	for range 10 {
		if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrThrottledAgain) || !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("repeat throttled = %v, want ErrThrottledAgain", err)
		}
	}
	// Another IP is unaffected.
	if _, err := svc.SignIn(ctx, AdminUsername, "wrong", "203.0.113.12"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("other IP = %v", err)
	}
	// Past the window the failures and the note expire together.
	*now = now.Add(SignInWindow)
	if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("after the window = %v, want a fresh check", err)
	}
	if _, ok := svc.throttled[ip]; ok {
		t.Fatal("throttle note outlived the failures")
	}
}

// Anonymous login begins cannot evict a signed-in user's register
// ceremony (GOSEC-09): registrations live in their own map. Begins
// come from many addresses, the strongest case for the shared map.
func TestLoginBeginEvictsRegister(t *testing.T) {
	svc := New(openStore(t), WithPasskeys("http://localhost:7659", nil))
	mustBootstrap(t, svc)
	_, regID, err := svc.BeginRegistration(context.Background(), AdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxCeremonies + 10 {
		if _, _, err := svc.BeginLogin(fmt.Sprintf("198.51.%d.%d", i>>8, i&0xff)); err != nil {
			t.Fatal(err)
		}
	}
	err = svc.FinishRegistration(context.Background(), AdminUsername, regID, []byte(`{}`))
	if errors.Is(err, ErrBadCeremony) {
		t.Fatalf("owner's register ceremony was evicted by %d anonymous login begins", maxCeremonies)
	}
	if len(svc.ceremonies) > maxCeremonies {
		t.Fatalf("%d login ceremonies, want ≤ %d", len(svc.ceremonies), maxCeremonies)
	}
}

// One address's begins are rate limited and hold at most
// maxLoginCeremoniesPerIP live ceremonies, evicting their own oldest,
// never another address's.
func TestLoginBeginPerIPLimits(t *testing.T) {
	svc := New(openStore(t), WithPasskeys("http://localhost:7659", nil))
	mustBootstrap(t, svc)
	now := b6Clock(svc)
	_, victim, err := svc.BeginLogin("192.0.2.50")
	if err != nil {
		t.Fatal(err)
	}
	const flooder = "192.0.2.66"
	for i := range maxLoginBegins {
		if _, _, err := svc.BeginLogin(flooder); err != nil {
			t.Fatalf("begin %d = %v", i+1, err)
		}
	}
	if _, _, err := svc.BeginLogin(flooder); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("begin past the limit = %v, want ErrTooManyAttempts", err)
	}
	live := 0
	for _, c := range svc.ceremonies {
		if c.ip == flooder {
			live++
		}
	}
	if live > maxLoginCeremoniesPerIP {
		t.Fatalf("flooder holds %d live ceremonies, want ≤ %d", live, maxLoginCeremoniesPerIP)
	}
	if _, ok := svc.ceremonies[victim]; !ok {
		t.Fatal("another address's ceremony was evicted")
	}
	// Another address still begins; the flooder begins again after
	// the window.
	if _, _, err := svc.BeginLogin("192.0.2.51"); err != nil {
		t.Fatalf("other address = %v", err)
	}
	*now = now.Add(SignInWindow)
	if _, _, err := svc.BeginLogin(flooder); err != nil {
		t.Fatalf("after the window = %v", err)
	}
}

// The login-begin limiter map stays bounded like the failures map.
func TestLoginBeginsMapBound(t *testing.T) {
	svc := New(openStore(t), WithPasskeys("http://localhost:7659", nil))
	now := svc.now()
	for i := range maxFailureIPs {
		svc.loginBegins[fmt.Sprintf("10.4.%d.%d", i>>8, i&0xff)] = []time.Time{now.Add(time.Duration(i) * time.Millisecond)}
	}
	if !svc.allowLoginBegin("10.9.9.9", now.Add(time.Second)) {
		t.Fatal("new address refused")
	}
	if len(svc.loginBegins) != maxFailureIPs {
		t.Fatalf("%d entries, want the %d bound", len(svc.loginBegins), maxFailureIPs)
	}
	if _, ok := svc.loginBegins["10.4.0.0"]; ok {
		t.Fatal("oldest entry survived, want it evicted")
	}
}

// Passkey finishes share the reservation: a concurrent burst of bad
// assertions from one IP is checked at most MaxSignInFailures times,
// and a bad ceremony is refunded, not counted.
func TestPasskeyFinishLimiterBurst(t *testing.T) {
	svc := New(openStore(t), WithPasskeys("http://localhost:7659", nil))
	mustBootstrap(t, svc)
	ctx := context.Background()
	const ip = "203.0.113.20"
	if _, err := svc.FinishLogin(ctx, "0123456789abcdef0123456789abcdef", []byte("{}"), ip); !errors.Is(err, ErrBadCeremony) {
		t.Fatalf("unknown ceremony = %v", err)
	}
	if _, ok := svc.failures[ip]; ok {
		t.Fatal("a bad ceremony was counted")
	}
	const n = 30
	ids := make([]string, n)
	for i := range ids {
		_, id, err := svc.BeginLogin(fmt.Sprintf("198.51.100.%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.FinishLogin(ctx, id, []byte("{}"), ip)
			if errors.Is(err, ErrPasskeyInvalid) {
				mu.Lock()
				checked++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if checked > MaxSignInFailures {
		t.Fatalf("%d of %d concurrent passkey finishes were verified; limit is %d", checked, n, MaxSignInFailures)
	}
}
