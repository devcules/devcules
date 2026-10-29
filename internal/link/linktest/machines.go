package linktest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
)

// MachineKeys is an in-memory machine verifier for tests: the hub
// asks it to check a challenge answer, as serve asks the enrolled
// machine store. The zero value is not usable; build with
// NewMachineKeys.
type MachineKeys struct {
	mu   sync.Mutex
	keys map[string]ed25519.PrivateKey
}

// NewMachineKeys returns an empty key set.
func NewMachineKeys() *MachineKeys {
	return &MachineKeys{keys: map[string]ed25519.PrivateKey{}}
}

// Add enrols name with a fresh key and returns the signer a machine
// dialer passes as dial.Options.Sign. Adding a name again returns the
// same key's signer, so tests sharing one key set across parallel
// hubs never invalidate each other's machines.
func (k *MachineKeys) Add(name string) func([]byte) []byte {
	k.mu.Lock()
	defer k.mu.Unlock()
	priv, ok := k.keys[name]
	if !ok {
		var err error
		_, priv, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic("linktest: ed25519 keygen: " + err.Error())
		}
		k.keys[name] = priv
	}
	return func(nonce []byte) []byte { return ed25519.Sign(priv, nonce) }
}

// VerifyMachine checks sig over the decoded nonce with name's key.
func (k *MachineKeys) VerifyMachine(_ context.Context, name, nonce string, sig []byte) error {
	k.mu.Lock()
	priv, ok := k.keys[name]
	k.mu.Unlock()
	if !ok {
		return errors.New("linktest: machine not enrolled")
	}
	pub := priv.Public().(ed25519.PublicKey)
	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, raw, sig) {
		return errors.New("linktest: bad signature")
	}
	return nil
}
