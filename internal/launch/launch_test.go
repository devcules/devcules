package launch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store/sqlite"
)

func newTokens(t *testing.T) (*Tokens, *time.Time) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)
	tk := New(s)
	tk.now = func() time.Time { return now }
	return tk, &now
}

func TestMintCheckRevoke(t *testing.T) {
	ctx := context.Background()
	tk, now := newTokens(t)
	token, l, err := tk.Mint(ctx, "  musecode in t1 ", "meta", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, Prefix) || len(token) != len(Prefix)+43 {
		t.Fatalf("token shape: %d chars", len(token))
	}
	if l.Label != "musecode in t1" || !l.Expires.Equal(now.Add(DefaultTTL)) || !l.Active {
		t.Fatalf("launch = %+v", l)
	}
	got, err := tk.Check(ctx, token)
	if err != nil || got.ID != l.ID || !got.Allows("meta") || got.Allows("openai") {
		t.Fatalf("check = %+v, %v", got, err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err := tk.Check(ctx, token); err != nil {
		t.Fatal(err)
	}
	list, _ := tk.List(ctx)
	if len(list) != 1 || list[0].LastUsed.IsZero() {
		t.Fatalf("list = %+v, want last use recorded", list)
	}
	if err := tk.Revoke(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Check(ctx, token); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
	if err := tk.Revoke(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke unknown: %v", err)
	}
	list, _ = tk.List(ctx)
	if list[0].Active || list[0].Revoked.IsZero() {
		t.Fatalf("revoked token listed as %+v", list[0])
	}
}

func TestCheckRejects(t *testing.T) {
	ctx := context.Background()
	tk, now := newTokens(t)
	token, _, _ := tk.Mint(ctx, "short", "", time.Minute)
	for _, bad := range []string{"", "dvl_short", "Bearer " + token, token[:len(token)-1] + "A", strings.Replace(token, Prefix, "xyz_", 1)} {
		if bad == token {
			continue
		}
		if _, err := tk.Check(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Check(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	l, err := tk.Check(ctx, token)
	if err != nil || !l.Allows("anything") {
		t.Fatalf("a provider-free token: %+v %v", l, err)
	}
	*now = now.Add(time.Minute)
	if _, err := tk.Check(ctx, token); !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry: %v", err)
	}
}

func TestMintValidates(t *testing.T) {
	ctx := context.Background()
	tk, _ := newTokens(t)
	for _, tc := range []struct {
		label string
		ttl   time.Duration
		want  string
	}{
		{"", 0, "label is required"},
		{strings.Repeat("x", 101), 0, "longer than"},
		{"ok", time.Second, "between 1 minute"},
		{"ok", MaxTTL + time.Second, "between 1 minute"},
		{"ok", -time.Minute, "between 1 minute"},
	} {
		if _, _, err := tk.Mint(ctx, tc.label, "", tc.ttl); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Mint(%q, %s) = %v, want %q", tc.label, tc.ttl, err, tc.want)
		}
	}
	if _, l, err := tk.Mint(ctx, "max", "", MaxTTL); err != nil || l.Expires.Sub(l.Created) != MaxTTL {
		t.Fatalf("max ttl: %+v %v", l, err)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	tk, now := newTokens(t)
	_, _, _ = tk.Mint(ctx, "old", "", time.Hour)
	_, _, _ = tk.Mint(ctx, "new", "", MaxTTL)
	*now = now.Add(26 * time.Hour)
	n, err := tk.Prune(ctx)
	if err != nil || n != 1 {
		t.Fatalf("prune = %d %v, want 1", n, err)
	}
	list, _ := tk.List(ctx)
	if len(list) != 1 || list[0].Label != "new" {
		t.Fatalf("after prune: %+v", list)
	}
}

func TestMintOnRunner(t *testing.T) {
	ctx := context.Background()
	tk, _ := newTokens(t)
	token, l, err := tk.MintOn(ctx, "laptop", "musecode", "meta", time.Hour)
	if err != nil || l.Runner != "laptop" {
		t.Fatalf("MintOn = %+v %v", l, err)
	}
	if got, err := tk.Check(ctx, token); err != nil || got.Runner != "laptop" {
		t.Fatalf("Check = %+v %v", got, err)
	}
	for _, bad := range []string{strings.Repeat("r", 65), "a\nb"} {
		if _, _, err := tk.MintOn(ctx, bad, "x", "", 0); err == nil || !strings.Contains(err.Error(), "runner") {
			t.Fatalf("MintOn(%q) = %v", bad, err)
		}
	}
}
