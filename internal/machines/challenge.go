package machines

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

const NonceSize = 32

func NewNonce() (string, error) {
	b := make([]byte, NonceSize)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Verify(nonce string, pub, sig []byte) error {
	nb, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil || len(nb) != NonceSize {
		return fmt.Errorf("machines: bad nonce")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("machines: bad pubkey")
	}
	if !ed25519.Verify(pub, nb, sig) {
		return fmt.Errorf("machines: bad signature")
	}
	return nil
}
