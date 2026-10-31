package machines

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/store/sqlite"
)

func TestEnrolLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, t.TempDir()+"/e.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	iss := NewIssuer(st)
	code, err := iss.Mint(ctx, "laptop")
	if err != nil || code == "" {
		t.Fatalf("Mint: %v %q", err, code)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	proof := ed25519.Sign(priv, EnrolMessage(code))
	m, err := iss.Redeem(ctx, code, pub, proof, "laptop")
	if err != nil || m.Name != "laptop" {
		t.Fatalf("Redeem: %v %+v", err, m)
	}
	if _, err := iss.Redeem(ctx, code, pub, proof, "laptop2"); err == nil {
		t.Fatal("reuse of enrol code succeeded")
	}
}

func TestChallengeVerify(t *testing.T) {
	nonce, err := NewNonce()
	if err != nil || nonce == "" {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, b)
	if err := Verify(nonce, pub, sig); err != nil {
		t.Fatal("good sig:", err)
	}
	bad := make([]byte, len(sig))
	copy(bad, sig)
	bad[0] ^= 1
	if err := Verify(nonce, pub, bad); err == nil {
		t.Fatal("bad sig passed")
	}
	if err := Verify("short", pub, sig); err == nil {
		t.Fatal("short nonce passed")
	}
}

func TestChallengeBadInputs(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	n, _ := NewNonce()
	b, _ := base64.RawURLEncoding.DecodeString(n)
	sig := ed25519.Sign(priv, b)
	for _, tt := range []struct {
		nonce    string
		pub, sig []byte
	}{{"bad!", pub, sig}, {n, pub[:1], sig}, {n, pub, sig[:1]}} {
		if err := Verify(tt.nonce, tt.pub, tt.sig); err == nil {
			t.Fatalf("Verify passed for %+v", tt)
		}
	}
}

// TestValidName pins the link-name shape the API boundary applies: it
// matches the hub's helloPayload, so a picker's display name ("host
// (Mac.home)", "container (…)") enrols without an alias and a "/" is
// still rejected (it would collide with the hub's machine address).
func TestValidName(t *testing.T) {
	for _, name := range []string{"Mac.home", "host (Mac.home)", "container (c87802936386)", "vpn-machine.kiran", "🔗 link"} {
		if !ValidName(name) {
			t.Errorf("ValidName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "with/slash", strings.Repeat("a", 65)} {
		if ValidName(name) {
			t.Errorf("ValidName(%q) = true, want false", name)
		}
	}
}
