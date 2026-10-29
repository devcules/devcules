package linktest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestMachineKeys(t *testing.T) {
	k := NewMachineKeys()
	sign := k.Add("lab")
	again := k.Add("lab")
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	ctx := context.Background()
	if err := k.VerifyMachine(ctx, "lab", nonce, sign(raw)); err != nil {
		t.Fatalf("own signature: %s", err)
	}
	if err := k.VerifyMachine(ctx, "lab", nonce, again(raw)); err != nil {
		t.Fatalf("adding a name twice changed its key: %s", err)
	}
	if err := k.VerifyMachine(ctx, "lab", nonce, NewMachineKeys().Add("lab")(raw)); err == nil {
		t.Fatal("another key verified")
	}
	if err := k.VerifyMachine(ctx, "ghost", nonce, sign(raw)); err == nil {
		t.Fatal("an unknown machine verified")
	}
	if err := k.VerifyMachine(ctx, "lab", "%%%", sign(raw)); err == nil {
		t.Fatal("a malformed nonce verified")
	}
}
