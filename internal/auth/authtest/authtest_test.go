package authtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strconv"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
)

// creationOptions is the creation-options subset Registration reads.
func creationOptions(challenge, userID string) []byte {
	return []byte(`{"challenge":` + strconv.Quote(challenge) + `,"user":{"id":` + strconv.Quote(userID) + `}}`)
}

func requestOptions(challenge string) []byte {
	return []byte(`{"challenge":` + strconv.Quote(challenge) + `}`)
}

func mustNew(t *testing.T) *Authenticator {
	t.Helper()
	a, err := New("localhost", "http://localhost:7659")
	if err != nil {
		t.Fatalf("New: %s", err)
	}
	return a
}

func TestRegistrationParses(t *testing.T) {
	a := mustNew(t)
	a.Counter = 7
	handle := base64.RawURLEncoding.EncodeToString([]byte("user-handle-32-bytes......"))
	body, err := a.Registration(creationOptions("challenge-string", handle))
	if err != nil {
		t.Fatalf("Registration: %s", err)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		t.Fatalf("library cannot parse the attestation: %s", err)
	}
	if !bytes.Equal(parsed.RawID, a.CredentialID) {
		t.Fatalf("credential id = %x, want the authenticator's %x", parsed.RawID, a.CredentialID)
	}
	if got := parsed.Response.CollectedClientData.Type; string(got) != "webauthn.create" {
		t.Fatalf("client data type = %q, want webauthn.create", got)
	}
	if got := parsed.Response.CollectedClientData.Challenge; got != "challenge-string" {
		t.Fatalf("challenge = %q, want it replayed verbatim", got)
	}
	if got := parsed.Response.CollectedClientData.Origin; got != "http://localhost:7659" {
		t.Fatalf("origin = %q, want the authenticator's", got)
	}
	authData := parsed.Response.AttestationObject.AuthData
	rpHash := sha256.Sum256([]byte("localhost"))
	if !bytes.Equal(authData.RPIDHash, rpHash[:]) {
		t.Fatal("rpIdHash is not the RP id's SHA-256")
	}
	if !authData.Flags.HasUserPresent() || !authData.Flags.HasUserVerified() {
		t.Fatalf("flags = %08b, want UP and UV set by default", byte(authData.Flags))
	}
	if authData.Flags.HasBackupEligible() || authData.Flags.HasBackupState() {
		t.Fatalf("flags = %08b, want device-bound by default", byte(authData.Flags))
	}
	if authData.Counter != 7 {
		t.Fatalf("counter = %d, want 7", authData.Counter)
	}
	if !bytes.Equal(authData.AttData.CredentialID, a.CredentialID) {
		t.Fatal("attested credential id does not match the response id")
	}
	if len(authData.AttData.CredentialPublicKey) == 0 {
		t.Fatal("attested credential carries no public key")
	}
	if got := parsed.Response.Transports; len(got) != 1 || string(got[0]) != "internal" {
		t.Fatalf("transports = %q, want [internal]", got)
	}
	if a.userHandle == nil || base64.RawURLEncoding.EncodeToString(a.userHandle) != handle {
		t.Fatal("Registration did not record the user handle for assertions")
	}
}

func TestRegistrationKnobs(t *testing.T) {
	a := mustNew(t)
	a.UserPresent = false
	a.UserVerified = false
	a.BackupEligible = true
	a.BackupState = true
	a.OriginOverride = "https://evil.example"
	a.RPIDOverride = "other.example"
	body, err := a.Registration(creationOptions("c", base64.RawURLEncoding.EncodeToString([]byte("h"))))
	if err != nil {
		t.Fatalf("Registration: %s", err)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		t.Fatalf("library cannot parse the attestation: %s", err)
	}
	if got := parsed.Response.CollectedClientData.Origin; got != "https://evil.example" {
		t.Fatalf("origin = %q, want the override", got)
	}
	rpHash := sha256.Sum256([]byte("other.example"))
	if !bytes.Equal(parsed.Response.AttestationObject.AuthData.RPIDHash, rpHash[:]) {
		t.Fatal("rpIdHash does not follow the RP id override")
	}
	flags := parsed.Response.AttestationObject.AuthData.Flags
	if flags.HasUserPresent() || flags.HasUserVerified() {
		t.Fatalf("flags = %08b, want UP and UV clear", byte(flags))
	}
	if !flags.HasBackupEligible() || !flags.HasBackupState() {
		t.Fatalf("flags = %08b, want BE and BS set", byte(flags))
	}
}

func TestAssertionParses(t *testing.T) {
	a := mustNew(t)
	handle := []byte("user-handle-32-bytes......")
	if _, err := a.Registration(creationOptions("c", base64.RawURLEncoding.EncodeToString(handle))); err != nil {
		t.Fatalf("Registration: %s", err)
	}
	a.Counter = 12
	body, err := a.Assertion(requestOptions("login-challenge"))
	if err != nil {
		t.Fatalf("Assertion: %s", err)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		t.Fatalf("library cannot parse the assertion: %s", err)
	}
	if !bytes.Equal(parsed.RawID, a.CredentialID) {
		t.Fatalf("credential id = %x, want the authenticator's %x", parsed.RawID, a.CredentialID)
	}
	if got := parsed.Response.CollectedClientData.Type; string(got) != "webauthn.get" {
		t.Fatalf("client data type = %q, want webauthn.get", got)
	}
	if got := parsed.Response.CollectedClientData.Challenge; got != "login-challenge" {
		t.Fatalf("challenge = %q, want it replayed verbatim", got)
	}
	if !bytes.Equal(parsed.Response.UserHandle, handle) {
		t.Fatalf("user handle = %x, want the registered %x", parsed.Response.UserHandle, handle)
	}
	if got := parsed.Response.AuthenticatorData.Counter; got != 12 {
		t.Fatalf("counter = %d, want 12", got)
	}
	var sig struct {
		R, S *big.Int
	}
	if _, err := asn1.Unmarshal(parsed.Response.Signature, &sig); err != nil || sig.R == nil || sig.S == nil {
		t.Fatalf("signature is not a DER ECDSA pair: %s", err)
	}
	// The response is JSON the server reads alongside the library:
	// id, rawId and type must all be present.
	var raw struct {
		ID    string `json:"id"`
		RawID string `json:"rawId"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("assertion is not JSON: %s", err)
	}
	if raw.ID == "" || raw.RawID == "" || raw.Type != "public-key" {
		t.Fatalf("assertion envelope = %+v, want id, rawId and type public-key", raw)
	}
}

func TestBadOptions(t *testing.T) {
	a := mustNew(t)
	if _, err := a.Registration([]byte("{broken")); err == nil {
		t.Fatal("Registration with broken options succeeded, want an error")
	}
	if _, err := a.Registration(creationOptions("c", "!!!not-base64!!!")); err == nil {
		t.Fatal("Registration with a bad user id succeeded, want an error")
	}
	if _, err := a.Assertion([]byte("{broken")); err == nil {
		t.Fatal("Assertion with broken options succeeded, want an error")
	}
}

func TestDistinctCredentials(t *testing.T) {
	a := mustNew(t)
	b := mustNew(t)
	if bytes.Equal(a.CredentialID, b.CredentialID) {
		t.Fatal("two authenticators share a credential id")
	}
}
