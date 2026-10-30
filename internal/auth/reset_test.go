package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/devcules/devcules/internal/store"
)

// TestResetPassword: the new password signs in, the old one and every
// existing session stop working, and an unknown user is not created.
func TestResetPassword(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	svc := New(s)
	old := mustBootstrap(t, svc)
	cookies := []string{}
	for range 2 {
		c, err := svc.SignIn(ctx, AdminUsername, old, "192.0.2.1")
		if err != nil {
			t.Fatalf("SignIn: %s", err)
		}
		cookies = append(cookies, c)
	}

	fresh, ended, err := svc.ResetPassword(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("ResetPassword: %s", err)
	}
	if fresh == "" || fresh == old || ended != 2 {
		t.Fatalf("reset = %q (old %q), ended %d, want a new password and 2 sessions ended", fresh, old, ended)
	}
	for _, c := range cookies {
		if _, err := svc.Authenticate(ctx, c); err == nil {
			t.Fatal("a session from before the reset still authenticates")
		}
	}
	if _, err := svc.SignIn(ctx, AdminUsername, old, "192.0.2.1"); err == nil {
		t.Fatal("the old password still signs in")
	}
	if _, err := svc.SignIn(ctx, AdminUsername, fresh, "192.0.2.1"); err != nil {
		t.Fatalf("the new password: %s", err)
	}
	if _, _, err := svc.ResetPassword(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user = %v, want ErrNotFound", err)
	}
}
