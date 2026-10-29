package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
)

// CeremonyTTL bounds a passkey ceremony: the challenge expires this
// long after begin, on the server and in the browser's options alike.
// Five minutes is long enough to find a phone and short enough that
// abandoned ceremonies die on their own.
const CeremonyTTL = 5 * time.Minute

const (
	// maxCeremonies bounds the in-flight ceremony map: past this many,
	// a new begin evicts the ceremony expiring first.
	maxCeremonies = 1000
	// ceremonyIDBytes is the ceremony id entropy: 128 bits, hex for
	// the cookie.
	ceremonyIDBytes = 16
	// maxPasskeyName caps the user-facing label in bytes.
	maxPasskeyName = 100
	// defaultPasskeyName labels keys registered without a name; the
	// owner renames them in Settings.
	defaultPasskeyName = "Passkey"
)

var (
	// ErrPasskeysDisabled reports ceremony calls while passkeys are
	// off. The status endpoint says why.
	ErrPasskeysDisabled = errors.New("auth: passkeys are not enabled")
	// ErrBadCeremony reports an unknown, consumed, expired or
	// wrong-kind ceremony id.
	ErrBadCeremony = errors.New("auth: passkey ceremony expired or invalid")
	// ErrPasskeyInvalid reports a response that failed verification:
	// a bad signature, challenge, origin, user verification or sign
	// counter, or an unknown credential.
	ErrPasskeyInvalid = errors.New("auth: passkey verification failed")
	// ErrPasskeyRegistered reports a credential id that is already
	// stored.
	ErrPasskeyRegistered = errors.New("auth: this passkey is already registered")
	// ErrPasskeyName reports an empty or overlong label.
	ErrPasskeyName = errors.New("auth: invalid passkey name")
	// ErrPasskeyNotFound reports an unknown passkey id, or one owned
	// by another user.
	ErrPasskeyNotFound = errors.New("auth: passkey not found")
)

// passkeyAlgorithms is what new credentials may use: ECDSA P-256
// first, then EdDSA. Platform authenticators speak ES256; EdDSA
// covers the rest without opening RSA.
var passkeyAlgorithms = []protocol.CredentialParameter{
	{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256},
	{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgEdDSA},
}

// ceremonyKind separates register challenges from login challenges:
// a challenge begun for one finish is rejected by the other.
type ceremonyKind string

const (
	ceremonyRegister ceremonyKind = "register"
	ceremonyLogin    ceremonyKind = "login"
)

// ceremony is one in-flight passkey ceremony: the library's session
// (challenge, RP id, origin, expiry) plus whose it is. Ceremonies
// live only in memory: a serve restart cancels every in-flight one,
// and the client simply begins again.
type ceremony struct {
	session  webauthn.SessionData
	kind     ceremonyKind
	username string
	expires  time.Time
}

// WithPasskeys enables WebAuthn passkeys from DEVCULES_PUBLIC_URL
// and logs the decision once. The RP id is the URL's host and the
// only allowed origin is its scheme://host[:port]. Passkeys stay off
// without a public URL, on an IP host (WebAuthn refuses IP RP ids),
// or on plain http away from localhost (not a secure context); the
// password always keeps working. A nil log discards.
func WithPasskeys(publicURL string, log *slog.Logger) Option {
	return func(a *Service) {
		if log != nil {
			a.log = log
		}
		rpID, origin, disabled := rpConfig(publicURL)
		if disabled != "" {
			a.passkeysOff = disabled
			a.log.Info("passkeys disabled", "reason", disabled)
			return
		}
		wa, err := webauthn.New(&webauthn.Config{
			RPID:                  rpID,
			RPDisplayName:         "Devcules",
			RPOrigins:             []string{origin},
			AttestationPreference: protocol.PreferNoAttestation,
			AuthenticatorSelection: protocol.AuthenticatorSelection{
				RequireResidentKey: protocol.ResidentKeyRequired(),
				ResidentKey:        protocol.ResidentKeyRequirementRequired,
				UserVerification:   protocol.VerificationRequired,
			},
			Timeouts: webauthn.TimeoutsConfig{
				Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: CeremonyTTL},
				Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: CeremonyTTL},
			},
		})
		if err != nil {
			a.passkeysOff = "DEVCULES_PUBLIC_URL names a host WebAuthn rejects as a relying party id. " +
				"Set it to an https name (for example with tailscale serve), or to http://localhost:7659 for local use. " +
				"Your password keeps working."
			a.log.Warn("passkeys disabled", "reason", a.passkeysOff)
			return
		}
		a.wa = wa
		a.log.Info("passkeys enabled", "rpID", rpID)
	}
}

// rpConfig derives the relying party from the public URL. It returns
// the RP id and origin, or a disabled reason in plain words with next
// steps. The URL already passed config validation, so anything
// unparseable here fails closed.
func rpConfig(publicURL string) (rpID, origin, disabled string) {
	next := "Set DEVCULES_PUBLIC_URL to an https name (for example with tailscale serve), " +
		"or to http://localhost:7659 for local use. Your password keeps working."
	if publicURL == "" {
		return "", "", "Passkeys are off. " + next
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", "DEVCULES_PUBLIC_URL is not a valid http(s) URL. " + next
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return "", "", "Passkeys need a hostname, not an IP address. " + next
	}
	if u.Scheme == "http" && !strings.EqualFold(host, "localhost") {
		return "", "", "Passkeys need https, or http on localhost. " + next
	}
	return host, u.Scheme + "://" + u.Host, ""
}

// PasskeysStatus reports whether passkeys are usable. When they are
// not, reason says why in plain words with next steps.
func (a *Service) PasskeysStatus() (enabled bool, reason string) {
	return a.wa != nil, a.passkeysOff
}

// BeginRegistration opens a register ceremony for username and
// returns the creation options as JSON plus the ceremony id the
// caller stores in the ceremony cookie. The options exclude the
// user's existing passkeys, require a resident key with user
// verification, and request no attestation.
func (a *Service) BeginRegistration(ctx context.Context, username string) (options []byte, ceremonyID string, err error) {
	if a.wa == nil {
		return nil, "", ErrPasskeysDisabled
	}
	user, err := a.passkeyUser(ctx, username)
	if err != nil {
		return nil, "", err
	}
	excludes := make([]protocol.CredentialDescriptor, len(user.creds))
	for i, cred := range user.creds {
		excludes[i] = cred.Descriptor()
	}
	creation, session, err := a.wa.BeginRegistration(user,
		webauthn.WithCredentialParameters(passkeyAlgorithms),
		webauthn.WithExclusions(excludes))
	if err != nil {
		return nil, "", fmt.Errorf("auth: cannot begin passkey registration: %w", err)
	}
	options, err = json.Marshal(creation.Response)
	if err != nil {
		return nil, "", fmt.Errorf("auth: cannot encode passkey options: %w", err)
	}
	id, err := newCeremonyID()
	if err != nil {
		return nil, "", err
	}
	a.storeCeremony(id, ceremony{session: *session, kind: ceremonyRegister, username: username, expires: a.now().Add(CeremonyTTL)})
	return options, id, nil
}

// FinishRegistration verifies the creation response for ceremonyID
// and stores the credential. The body is the credential JSON with an
// optional top-level name; without one the key is called Passkey.
// The ceremony is single use: any finish consumes it.
func (a *Service) FinishRegistration(ctx context.Context, username, ceremonyID string, body []byte) error {
	if a.wa == nil {
		return ErrPasskeysDisabled
	}
	c, err := a.takeCeremony(ceremonyID, ceremonyRegister, username)
	if err != nil {
		return err
	}
	var named struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &named); err != nil {
		return ErrPasskeyInvalid
	}
	name := strings.TrimSpace(named.Name)
	if name == "" {
		name = defaultPasskeyName
	}
	if len(name) > maxPasskeyName {
		return ErrPasskeyName
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return ErrPasskeyInvalid
	}
	user, err := a.passkeyUser(ctx, c.username)
	if err != nil {
		return err
	}
	cred, err := a.wa.CreateCredential(user, c.session, parsed)
	if err != nil {
		return ErrPasskeyInvalid
	}
	now := a.now()
	transports := make([]string, len(cred.Transport))
	for i, t := range cred.Transport {
		transports[i] = string(t)
	}
	err = a.store.Passkeys().Create(ctx, store.Passkey{
		ID:             cred.ID,
		Username:       c.username,
		PublicKey:      cred.PublicKey,
		SignCount:      cred.Authenticator.SignCount,
		AAGUID:         cred.Authenticator.AAGUID,
		Transports:     transports,
		BackupEligible: cred.Flags.BackupEligible,
		BackupState:    cred.Flags.BackupState,
		Name:           name,
		CreatedAt:      now,
		LastUsedAt:     now,
	})
	if err != nil {
		if errors.Is(err, store.ErrExists) {
			return ErrPasskeyRegistered
		}
		return fmt.Errorf("auth: cannot store passkey: %w", err)
	}
	return nil
}

// BeginLogin opens a discoverable-credential login ceremony — no
// username needed — and returns the request options as JSON plus the
// ceremony id the caller stores in the ceremony cookie.
func (a *Service) BeginLogin() (options []byte, ceremonyID string, err error) {
	if a.wa == nil {
		return nil, "", ErrPasskeysDisabled
	}
	assertion, session, err := a.wa.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", fmt.Errorf("auth: cannot begin passkey login: %w", err)
	}
	options, err = json.Marshal(assertion.Response)
	if err != nil {
		return nil, "", fmt.Errorf("auth: cannot encode passkey options: %w", err)
	}
	id, err := newCeremonyID()
	if err != nil {
		return nil, "", err
	}
	a.storeCeremony(id, ceremony{session: *session, kind: ceremonyLogin, expires: a.now().Add(CeremonyTTL)})
	return options, id, nil
}

// FinishLogin verifies the assertion for ceremonyID and opens a
// session exactly like SignIn, returning the raw cookie value. Bad
// ceremonies fail uncounted; every other failure counts against the
// same per-IP limiter as passwords. The ceremony is single use: any
// finish consumes it.
func (a *Service) FinishLogin(ctx context.Context, ceremonyID string, body []byte, clientIP string) (string, error) {
	if a.wa == nil {
		return "", ErrPasskeysDisabled
	}
	now := a.now()
	if a.overLimit(clientIP, now) {
		return "", ErrTooManyAttempts
	}
	fail := func() (string, error) {
		a.recordFailure(clientIP, now)
		return "", ErrPasskeyInvalid
	}
	c, err := a.takeCeremony(ceremonyID, ceremonyLogin, "")
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return fail()
	}
	// One login at a time: the counter check and its writeback below
	// must not interleave with another login for the same key.
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	stored, err := a.store.Passkeys().Get(ctx, parsed.RawID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("auth: cannot look up passkey: %w", err)
		}
		return fail()
	}
	if newCount := parsed.Response.AuthenticatorData.Counter; stored.SignCount != 0 && newCount != 0 && newCount <= stored.SignCount {
		a.log.Warn("possible cloned passkey", "credential", credPrefix(stored.ID))
		return fail()
	}
	handler := func(rawID, _ []byte) (webauthn.User, error) {
		found, err := a.store.Passkeys().Get(ctx, rawID)
		if err != nil {
			return nil, err
		}
		return a.passkeyUser(ctx, found.Username)
	}
	_, cred, err := a.wa.ValidatePasskeyLogin(handler, c.session, parsed)
	if err != nil {
		return fail()
	}
	if err := a.store.Passkeys().UpdateAfterLogin(ctx, cred.ID, cred.Authenticator.SignCount, cred.Flags.BackupState, now); err != nil {
		return "", fmt.Errorf("auth: cannot update passkey: %w", err)
	}
	a.clearFailures(clientIP)
	return a.mintSession(ctx, stored.Username, now)
}

// credPrefix is the credential id's first bytes as hex: enough to
// correlate log lines, never the whole id.
func credPrefix(id []byte) string {
	return hex.EncodeToString(id[:min(len(id), 4)])
}

// newCeremonyID mints 128 random bits, hex-encoded, for the ceremony
// cookie.
func newCeremonyID() (string, error) {
	var raw [ceremonyIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("auth: cannot mint ceremony id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// storeCeremony files a ceremony, sweeping expired ones first. Past
// the cap, the ceremony expiring first is evicted: in-flight
// ceremonies die, but memory stays bounded.
func (a *Service) storeCeremony(id string, c ceremony) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for key, old := range a.ceremonies {
		if now.After(old.expires) {
			delete(a.ceremonies, key)
		}
	}
	for len(a.ceremonies) >= maxCeremonies {
		oldest, at, first := "", now, true
		for key, old := range a.ceremonies {
			if first || old.expires.Before(at) {
				oldest, at, first = key, old.expires, false
			}
		}
		if first {
			break
		}
		delete(a.ceremonies, oldest)
	}
	a.ceremonies[id] = c
}

// takeCeremony consumes the ceremony: any finish, valid or not,
// removes the id, so challenges are single use. A register ceremony
// also demands its own username, so a session cannot finish another
// user's ceremony; login ceremonies carry no username.
func (a *Service) takeCeremony(id string, kind ceremonyKind, username string) (ceremony, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.ceremonies[id]
	if !ok {
		return ceremony{}, ErrBadCeremony
	}
	delete(a.ceremonies, id)
	// At exactly the deadline the ceremony is still valid; past it,
	// it is gone. One convention, as with sessions.
	if c.kind != kind || a.now().After(c.expires) || (kind == ceremonyRegister && c.username != username) {
		return ceremony{}, ErrBadCeremony
	}
	return c, nil
}

// passkeyUser loads the library's user view: the opaque handle (never
// the username), the display name, and every stored credential.
func (a *Service) passkeyUser(ctx context.Context, username string) (*passkeyUser, error) {
	handle, err := a.store.Users().EnsureWebAuthnHandle(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("auth: cannot load webauthn handle: %w", err)
	}
	stored, err := a.store.Passkeys().ListByUser(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("auth: cannot list passkeys: %w", err)
	}
	creds := make([]webauthn.Credential, len(stored))
	for i, p := range stored {
		creds[i] = toCredential(p)
	}
	return &passkeyUser{id: handle, name: username, creds: creds}, nil
}

// passkeyUser is the library's user: the opaque handle, the display
// name, and the stored credentials.
type passkeyUser struct {
	id    []byte
	name  string
	creds []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.id }
func (u *passkeyUser) WebAuthnName() string                       { return u.name }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// toCredential rebuilds the library's credential from a stored row.
// User presence and verification read true: registration demands
// both, so a stored key proved them. The backup flags round-trip
// from the row: the library rejects a login whose backup-eligible
// flag changed.
func toCredential(p store.Passkey) webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, len(p.Transports))
	for i, t := range p.Transports {
		transports[i] = protocol.AuthenticatorTransport(t)
	}
	return webauthn.Credential{
		ID:        p.ID,
		PublicKey: p.PublicKey,
		Transport: transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: p.BackupEligible,
			BackupState:    p.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    p.AAGUID,
			SignCount: p.SignCount,
		},
	}
}

// PasskeySummary is one passkey for Settings: the credential id, the
// label, both timestamps, and whether the key is backed up.
type PasskeySummary struct {
	ID        []byte
	Name      string
	CreatedAt time.Time
	LastUsed  time.Time
	BackedUp  bool
}

// ListPasskeys returns the user's passkeys, oldest first. It works
// whether passkeys are enabled or not: existing keys stay visible
// (and removable) while the feature is off.
func (a *Service) ListPasskeys(ctx context.Context, username string) ([]PasskeySummary, error) {
	stored, err := a.store.Passkeys().ListByUser(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("auth: cannot list passkeys: %w", err)
	}
	out := make([]PasskeySummary, len(stored))
	for i, p := range stored {
		out[i] = PasskeySummary{ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt, LastUsed: p.LastUsedAt, BackedUp: p.BackupState}
	}
	return out, nil
}

// RenamePasskey relabels the user's passkey. Unknown ids and other
// users' ids share ErrPasskeyNotFound, so labels cannot be probed.
func (a *Service) RenamePasskey(ctx context.Context, username string, id []byte, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxPasskeyName {
		return ErrPasskeyName
	}
	found, err := a.store.Passkeys().Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrPasskeyNotFound
		}
		return fmt.Errorf("auth: cannot look up passkey: %w", err)
	}
	if found.Username != username {
		return ErrPasskeyNotFound
	}
	if err := a.store.Passkeys().Rename(ctx, id, name); err != nil {
		return fmt.Errorf("auth: cannot rename passkey: %w", err)
	}
	return nil
}

// DeletePasskey removes the user's passkey. Unknown ids and other
// users' ids share ErrPasskeyNotFound, so keys cannot be probed.
func (a *Service) DeletePasskey(ctx context.Context, username string, id []byte) error {
	found, err := a.store.Passkeys().Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrPasskeyNotFound
		}
		return fmt.Errorf("auth: cannot look up passkey: %w", err)
	}
	if found.Username != username {
		return ErrPasskeyNotFound
	}
	if err := a.store.Passkeys().Delete(ctx, id); err != nil {
		return fmt.Errorf("auth: cannot delete passkey: %w", err)
	}
	return nil
}
