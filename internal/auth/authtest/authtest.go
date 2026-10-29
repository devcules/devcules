package authtest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
)

// Authenticator flag bits (CTAP2): user presence, user verification,
// backup eligibility and state, attested credential data, and
// extensions.
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// Authenticator is one software credential: a P-256 key, a credential
// id, and the knobs the ceremony tests turn. Registration defaults to
// a verifying, present, device-bound key; Assertion replays the user
// handle Registration recorded, as a resident key would.
type Authenticator struct {
	RPID   string
	Origin string

	key          *ecdsa.PrivateKey
	CredentialID []byte
	AAGUID       []byte
	userHandle   []byte

	// Counter is the sign count the next ceremony reports.
	Counter uint32
	// UserPresent and UserVerified set the UP and UV flag bits.
	UserPresent  bool
	UserVerified bool
	// BackupEligible and BackupState set the BE and BS flag bits. The
	// server rejects BE=false with BS=true, and a login whose BE
	// differs from registration.
	BackupEligible bool
	BackupState    bool
	// Transports is reported in attestation responses.
	Transports []string
	// OriginOverride replaces Origin in client data. RPIDOverride
	// replaces RPID in the rpIdHash.
	OriginOverride string
	RPIDOverride   string
}

// New makes an authenticator for rpID serving origin, with a fresh
// P-256 key and credential id.
func New(rpID, origin string) (*Authenticator, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("authtest: cannot make credential key: %w", err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("authtest: cannot make credential id: %w", err)
	}
	return &Authenticator{
		RPID:         rpID,
		Origin:       origin,
		key:          key,
		CredentialID: id,
		AAGUID:       make([]byte, 16),
		UserPresent:  true,
		UserVerified: true,
		Transports:   []string{"internal"},
	}, nil
}

func (a *Authenticator) flags(attested bool) byte {
	var f byte
	if a.UserPresent {
		f |= flagUP
	}
	if a.UserVerified {
		f |= flagUV
	}
	if a.BackupEligible {
		f |= flagBE
	}
	if a.BackupState {
		f |= flagBS
	}
	if attested {
		f |= flagAT
	}
	return f
}

func (a *Authenticator) rpID() string {
	if a.RPIDOverride != "" {
		return a.RPIDOverride
	}
	return a.RPID
}

func (a *Authenticator) origin() string {
	if a.OriginOverride != "" {
		return a.OriginOverride
	}
	return a.Origin
}

// registrationOptions is the creation-options subset the
// authenticator reads: the challenge string (replayed verbatim into
// client data) and the user id (stored with the credential).
type registrationOptions struct {
	Challenge string `json:"challenge"`
	User      struct {
		ID string `json:"id"`
	} `json:"user"`
}

// Registration builds a register/finish body for options: "none"
// attestation over the challenged rpIdHash, and records the user
// handle for later assertions.
func (a *Authenticator) Registration(options []byte) ([]byte, error) {
	var opts registrationOptions
	if err := json.Unmarshal(options, &opts); err != nil {
		return nil, fmt.Errorf("authtest: cannot parse creation options: %w", err)
	}
	handle, err := base64.RawURLEncoding.DecodeString(opts.User.ID)
	if err != nil {
		return nil, fmt.Errorf("authtest: user id is not base64url: %w", err)
	}
	a.userHandle = handle

	clientData := map[string]string{
		"type":      "webauthn.create",
		"challenge": opts.Challenge,
		"origin":    a.origin(),
	}
	clientDataJSON, err := json.Marshal(clientData)
	if err != nil {
		return nil, fmt.Errorf("authtest: cannot encode client data: %w", err)
	}
	authData := a.registrationAuthData()

	var buf bytes.Buffer
	buf.WriteByte(0xa3) // map of 3
	writeCBORText(&buf, "fmt")
	writeCBORText(&buf, "none")
	writeCBORText(&buf, "authData")
	writeCBORBytes(&buf, authData)
	writeCBORText(&buf, "attStmt")
	buf.WriteByte(0xa0) // empty map

	return json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(a.CredentialID),
		"rawId": base64.RawURLEncoding.EncodeToString(a.CredentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientDataJSON),
			"attestationObject": base64.RawURLEncoding.EncodeToString(buf.Bytes()),
			"transports":        a.Transports,
		},
	})
}

// registrationAuthData builds the attested authenticator data:
// rpIdHash, flags with AT set, the counter, and the credential record
// (AAGUID, id length, id, COSE key).
func (a *Authenticator) registrationAuthData() []byte {
	rpHash := sha256.Sum256([]byte(a.rpID()))
	cose := coseES256Key(&a.key.PublicKey)
	authData := make([]byte, 0, 32+1+4+16+2+len(a.CredentialID)+len(cose))
	authData = append(authData, rpHash[:]...)
	authData = append(authData, a.flags(true))
	authData = binary.BigEndian.AppendUint32(authData, a.Counter)
	authData = append(authData, a.AAGUID...)
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(a.CredentialID)))
	authData = append(authData, a.CredentialID...)
	authData = append(authData, cose...)
	return authData
}

// coseES256Key encodes the P-256 public key as a COSE_Key:
// {1: 2 (EC2), 3: -7 (ES256), -1: 1 (P-256), -2: x, -3: y}. The
// coordinates come from the uncompressed encoding (0x04 || x || y):
// P-256 only, as this authenticator is.
func coseES256Key(pub *ecdsa.PublicKey) []byte {
	raw, err := pub.Bytes()
	if err != nil || len(raw) != 65 || raw[0] != 4 {
		panic("authtest: not a P-256 public key")
	}
	x, y := raw[1:33], raw[33:65]
	var buf bytes.Buffer
	buf.WriteByte(0xa5) // map of 5
	writeCBORUint(&buf, 1)
	writeCBORUint(&buf, 2)
	writeCBORUint(&buf, 3)
	writeCBORNeg(&buf, 7)
	writeCBORNeg(&buf, 1)
	writeCBORUint(&buf, 1)
	writeCBORNeg(&buf, 2)
	writeCBORBytes(&buf, x)
	writeCBORNeg(&buf, 3)
	writeCBORBytes(&buf, y)
	return buf.Bytes()
}

// ecdsaSignature is the ASN.1 form of an ECDSA signature.
type ecdsaSignature struct {
	R, S *big.Int
}

// assertionOptions is the request-options subset the authenticator
// reads: the challenge string, replayed verbatim into client data.
type assertionOptions struct {
	Challenge string `json:"challenge"`
}

// Assertion builds a login/finish body for options: the rpIdHash,
// flags and counter, signed with the credential key over the
// authenticator data and the client-data hash.
func (a *Authenticator) Assertion(options []byte) ([]byte, error) {
	var opts assertionOptions
	if err := json.Unmarshal(options, &opts); err != nil {
		return nil, fmt.Errorf("authtest: cannot parse request options: %w", err)
	}
	clientData := map[string]string{
		"type":      "webauthn.get",
		"challenge": opts.Challenge,
		"origin":    a.origin(),
	}
	clientDataJSON, err := json.Marshal(clientData)
	if err != nil {
		return nil, fmt.Errorf("authtest: cannot encode client data: %w", err)
	}
	rpHash := sha256.Sum256([]byte(a.rpID()))
	authData := make([]byte, 0, 32+1+4)
	authData = append(authData, rpHash[:]...)
	authData = append(authData, a.flags(false))
	authData = binary.BigEndian.AppendUint32(authData, a.Counter)

	// ES256 signs the digest of the authenticator data and the
	// client-data hash: ecdsa.Sign truncates longer inputs, so the
	// hash step is not optional. The signature travels DER-encoded,
	// as the server's verifier requires.
	clientHash := sha256.Sum256(clientDataJSON)
	sigInput := make([]byte, 0, len(authData)+len(clientHash))
	sigInput = append(sigInput, authData...)
	sigInput = append(sigInput, clientHash[:]...)
	digest := sha256.Sum256(sigInput)
	r, s, err := ecdsa.Sign(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, fmt.Errorf("authtest: cannot sign assertion: %w", err)
	}
	signature, err := asn1.Marshal(ecdsaSignature{R: r, S: s})
	if err != nil {
		return nil, fmt.Errorf("authtest: cannot encode assertion signature: %w", err)
	}

	return json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(a.CredentialID),
		"rawId": base64.RawURLEncoding.EncodeToString(a.CredentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientDataJSON),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
			"signature":         base64.RawURLEncoding.EncodeToString(signature),
			"userHandle":        base64.RawURLEncoding.EncodeToString(a.userHandle),
		},
	})
}

// writeCBORUint writes a small unsigned integer (major type 0).
func writeCBORUint(buf *bytes.Buffer, n uint64) {
	writeCBORHead(buf, 0, n)
}

// writeCBORNeg writes a small negative integer (major type 1): -n.
func writeCBORNeg(buf *bytes.Buffer, n uint64) {
	writeCBORHead(buf, 1, n-1)
}

// writeCBORBytes writes a byte string (major type 2).
func writeCBORBytes(buf *bytes.Buffer, b []byte) {
	writeCBORHead(buf, 2, uint64(len(b)))
	buf.Write(b)
}

// writeCBORText writes a text string (major type 3).
func writeCBORText(buf *bytes.Buffer, s string) {
	writeCBORHead(buf, 3, uint64(len(s)))
	buf.WriteString(s)
}

// writeCBORHead writes one initial byte plus its length: inline below
// 24, then 1-, 2- and 4-byte forms. Our structures never exceed them.
func writeCBORHead(buf *bytes.Buffer, major byte, n uint64) {
	switch {
	case n < 24:
		buf.WriteByte(major<<5 | byte(n))
	case n < 256:
		buf.WriteByte(major<<5 | 24)
		buf.WriteByte(byte(n))
	case n < 65536:
		buf.WriteByte(major<<5 | 25)
		_ = binary.Write(buf, binary.BigEndian, uint16(n))
	default:
		buf.WriteByte(major<<5 | 26)
		_ = binary.Write(buf, binary.BigEndian, uint32(n))
	}
}
