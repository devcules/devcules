package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/auth/authtest"
)

const (
	testPublicURL = "http://localhost:7659"
	testRPID      = "localhost"
)

// passkeyFixture opens a service with passkeys on, a bootstrapped
// admin, and a log buffer for warning assertions.
func passkeyFixture(t *testing.T, publicURL string) (*Service, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	svc := New(openStore(t), WithPasskeys(publicURL, slog.New(slog.NewTextHandler(&logs, nil))))
	mustBootstrap(t, svc)
	return svc, &logs
}

func mustAuthenticator(t *testing.T) *authtest.Authenticator {
	t.Helper()
	a, err := authtest.New(testRPID, testPublicURL)
	if err != nil {
		t.Fatalf("authtest.New: %s", err)
	}
	return a
}

// mustRegister runs a register ceremony and returns the ceremony-free
// aftermath: the finish must succeed.
func mustRegister(t *testing.T, svc *Service, a *authtest.Authenticator, name string) {
	t.Helper()
	ctx := context.Background()
	options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("BeginRegistration: %s", err)
	}
	body, err := a.Registration(options)
	if err != nil {
		t.Fatalf("Registration: %s", err)
	}
	if name != "" {
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("cannot add name: %s", err)
		}
		raw["name"] = name
		if body, err = json.Marshal(raw); err != nil {
			t.Fatalf("cannot add name: %s", err)
		}
	}
	if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); err != nil {
		t.Fatalf("FinishRegistration: %s", err)
	}
}

// mustLogin runs a login ceremony and returns the cookie value.
func mustLogin(t *testing.T, svc *Service, a *authtest.Authenticator, clientIP string) string {
	t.Helper()
	ctx := context.Background()
	options, ceremonyID, err := svc.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin: %s", err)
	}
	body, err := a.Assertion(options)
	if err != nil {
		t.Fatalf("Assertion: %s", err)
	}
	cookieValue, err := svc.FinishLogin(ctx, ceremonyID, body, clientIP)
	if err != nil {
		t.Fatalf("FinishLogin: %s", err)
	}
	return cookieValue
}

func TestPasskeyRegisterThenSignIn(t *testing.T) {
	ctx := context.Background()
	svc, _ := passkeyFixture(t, testPublicURL)
	a := mustAuthenticator(t)
	mustRegister(t, svc, a, "Phone")

	listed, err := svc.ListPasskeys(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("ListPasskeys: %s", err)
	}
	if len(listed) != 1 || listed[0].Name != "Phone" || !bytes.Equal(listed[0].ID, a.CredentialID) {
		t.Fatalf("ListPasskeys = %+v, want the one registered key", listed)
	}

	cookieValue := mustLogin(t, svc, a, "10.1.0.1")
	if len(cookieValue) != tokenBytes*2 {
		t.Fatalf("cookie value length = %d, want %d like a password sign-in", len(cookieValue), tokenBytes*2)
	}
	who, err := svc.Authenticate(ctx, cookieValue)
	if err != nil {
		t.Fatalf("Authenticate after passkey login: %s", err)
	}
	if who != AdminUsername {
		t.Fatalf("Authenticate = %q, want %q", who, AdminUsername)
	}
	if err := svc.SignOut(ctx, cookieValue); err != nil {
		t.Fatalf("SignOut after passkey login: %s", err)
	}
	// The login recorded its use.
	stored, err := svc.store.Passkeys().Get(ctx, a.CredentialID)
	if err != nil {
		t.Fatalf("Get after login: %s", err)
	}
	if stored.LastUsedAt.Before(stored.CreatedAt) {
		t.Fatal("last used predates creation after a login")
	}
}

func TestPasskeyWrongOrigin(t *testing.T) {
	ctx := context.Background()
	t.Run("register", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		a.OriginOverride = "http://evil.example"
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishRegistration with a wrong origin = %v, want ErrPasskeyInvalid", err)
		}
	})
	t.Run("login", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		a.OriginOverride = "http://evil.example"
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := a.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, body, "10.1.0.2"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin with a wrong origin = %v, want ErrPasskeyInvalid", err)
		}
	})
}

func TestPasskeyWrongRPID(t *testing.T) {
	ctx := context.Background()
	t.Run("register", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		a.RPIDOverride = "other.example"
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishRegistration with a wrong RP id = %v, want ErrPasskeyInvalid", err)
		}
	})
	t.Run("login", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		a.RPIDOverride = "other.example"
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := a.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, body, "10.1.0.3"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin with a wrong RP id = %v, want ErrPasskeyInvalid", err)
		}
	})
}

func TestPasskeyChallengeMisuse(t *testing.T) {
	ctx := context.Background()
	t.Run("reused challenge", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); err != nil {
			t.Fatalf("first FinishRegistration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("second finish = %v, want ErrBadCeremony (single use)", err)
		}
	})
	t.Run("failed finish still consumes", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		_, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, []byte("{broken"), "10.1.0.4"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin with garbage = %v, want ErrPasskeyInvalid", err)
		}
		options, _, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := a.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, body, "10.1.0.4"); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("retry on the consumed ceremony = %v, want ErrBadCeremony", err)
		}
	})
	t.Run("expired challenge", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		past := svc.now().Add(-CeremonyTTL).Add(-time.Second)
		c := svc.ceremonies[ceremonyID]
		c.expires = past
		c.session.Expires = past
		svc.ceremonies[ceremonyID] = c
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("FinishRegistration past expiry = %v, want ErrBadCeremony", err)
		}
	})
	t.Run("unknown challenge", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		if err := svc.FinishRegistration(ctx, AdminUsername, "0123456789abcdef0123456789abcdef", []byte("{}")); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("FinishRegistration with an unknown ceremony = %v, want ErrBadCeremony", err)
		}
		if _, err := svc.FinishLogin(ctx, "0123456789abcdef0123456789abcdef", []byte("{}"), "10.1.0.5"); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("FinishLogin with an unknown ceremony = %v, want ErrBadCeremony", err)
		}
	})
	t.Run("register challenge at login finish", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		regOptions, regCeremony, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		regBody, err := a.Registration(regOptions)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, regCeremony, regBody, "10.1.0.6"); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("FinishLogin with a register ceremony = %v, want ErrBadCeremony", err)
		}
	})
	t.Run("login challenge at register finish", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		loginOptions, loginCeremony, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		loginBody, err := a.Assertion(loginOptions)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, loginCeremony, loginBody); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("FinishRegistration with a login ceremony = %v, want ErrBadCeremony", err)
		}
	})
	t.Run("challenge from another ceremony", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		_, ceremonyOne, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		optionsTwo, _, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		bodyTwo, err := a.Assertion(optionsTwo)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyOne, bodyTwo, "10.1.0.7"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin with another ceremony's challenge = %v, want ErrPasskeyInvalid", err)
		}
	})
	t.Run("register finish for another user", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		if err := svc.store.Users().Create(ctx, "bob", "hash"); err != nil {
			t.Fatalf("Create bob: %s", err)
		}
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, "bob", ceremonyID, body); !errors.Is(err, ErrBadCeremony) {
			t.Fatalf("FinishRegistration as another user = %v, want ErrBadCeremony", err)
		}
	})
}

func TestPasskeyUserVerification(t *testing.T) {
	ctx := context.Background()
	t.Run("register without UV", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		a.UserVerified = false
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishRegistration without UV = %v, want ErrPasskeyInvalid", err)
		}
	})
	t.Run("login without UV", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		a.UserVerified = false
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := a.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, body, "10.1.0.8"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin without UV = %v, want ErrPasskeyInvalid", err)
		}
	})
}

func TestPasskeyCounter(t *testing.T) {
	ctx := context.Background()
	login := func(t *testing.T, svc *Service, a *authtest.Authenticator, counter uint32, ip string) error {
		t.Helper()
		a.Counter = counter
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := a.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		_, err = svc.FinishLogin(ctx, ceremonyID, body, ip)
		return err
	}

	t.Run("regression rejected and logged", func(t *testing.T) {
		svc, logs := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		a.Counter = 5
		mustRegister(t, svc, a, "")
		if err := login(t, svc, a, 5, "10.1.0.9"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("login with an equal counter = %v, want ErrPasskeyInvalid", err)
		}
		if err := login(t, svc, a, 3, "10.1.0.9"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("login with a lower counter = %v, want ErrPasskeyInvalid", err)
		}
		if !strings.Contains(logs.String(), "possible cloned passkey") {
			t.Fatalf("logs lack the clone warning:\n%s", logs.String())
		}
		full := base64.RawURLEncoding.EncodeToString(a.CredentialID)
		if strings.Contains(logs.String(), full) {
			t.Fatal("logs carry the whole credential id, want a prefix only")
		}
	})

	t.Run("zero stays usable", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		a.Counter = 5
		mustRegister(t, svc, a, "")
		// A synced key that reports 0 after a counted registration
		// still signs in: only two non-zero counts compare.
		if err := login(t, svc, a, 0, "10.1.0.10"); err != nil {
			t.Fatalf("login with a zero counter: %s", err)
		}
	})

	t.Run("advance accepted", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		a.Counter = 5
		mustRegister(t, svc, a, "")
		if err := login(t, svc, a, 6, "10.1.0.11"); err != nil {
			t.Fatalf("login with a larger counter: %s", err)
		}
		stored, err := svc.store.Passkeys().Get(ctx, a.CredentialID)
		if err != nil {
			t.Fatalf("Get after login: %s", err)
		}
		if stored.SignCount != 6 {
			t.Fatalf("stored counter = %d, want the new 6", stored.SignCount)
		}
	})
}

func TestPasskeyUnknownAndDeleted(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown credential", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		registered := mustAuthenticator(t)
		mustRegister(t, svc, registered, "")
		stranger := mustAuthenticator(t)
		// The stranger never registered: its assertion is well formed
		// (it even recorded a handle), but the credential is unknown.
		regOptions, _, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		if _, err := stranger.Registration(regOptions); err != nil {
			t.Fatalf("Registration: %s", err)
		}
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := stranger.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, body, "10.1.0.12"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin with an unknown credential = %v, want ErrPasskeyInvalid", err)
		}
	})
	t.Run("deleted passkey", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		if err := svc.DeletePasskey(ctx, AdminUsername, a.CredentialID); err != nil {
			t.Fatalf("DeletePasskey: %s", err)
		}
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := a.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		if _, err := svc.FinishLogin(ctx, ceremonyID, body, "10.1.0.13"); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("FinishLogin with a deleted passkey = %v, want ErrPasskeyInvalid", err)
		}
	})
	t.Run("duplicate registration", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if err := svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body); !errors.Is(err, ErrPasskeyRegistered) {
			t.Fatalf("second FinishRegistration for one credential = %v, want ErrPasskeyRegistered", err)
		}
	})
}

func TestPasskeyLoginRateLimit(t *testing.T) {
	ctx := context.Background()
	svc, _ := passkeyFixture(t, testPublicURL)
	stranger := mustAuthenticator(t)
	registered := mustAuthenticator(t)
	mustRegister(t, svc, registered, "")
	// The stranger records a handle so its assertions are well
	// formed; only the credential stays unknown.
	regOptions, _, err := svc.BeginRegistration(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("BeginRegistration: %s", err)
	}
	if _, err := stranger.Registration(regOptions); err != nil {
		t.Fatalf("Registration: %s", err)
	}
	fail := func(ip string) error {
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			t.Fatalf("BeginLogin: %s", err)
		}
		body, err := stranger.Assertion(options)
		if err != nil {
			t.Fatalf("Assertion: %s", err)
		}
		_, err = svc.FinishLogin(ctx, ceremonyID, body, ip)
		return err
	}
	ip := "10.1.0.14"
	for i := range MaxSignInFailures {
		if err := fail(ip); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("failure %d = %v, want ErrPasskeyInvalid", i+1, err)
		}
	}
	if err := fail(ip); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("failure %d = %v, want ErrTooManyAttempts", MaxSignInFailures+1, err)
	}
	// The limiter is shared with passwords: the same IP is limited
	// there too.
	if _, err := svc.SignIn(ctx, AdminUsername, "wrong", ip); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("password SignIn on the limited IP = %v, want ErrTooManyAttempts", err)
	}
	// A passkey success clears the count, as a password one does.
	other := "10.1.0.15"
	for range MaxSignInFailures - 1 {
		if err := fail(other); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("failure on the other IP = %v, want ErrPasskeyInvalid", err)
		}
	}
	mustLogin(t, svc, registered, other)
	for i := range MaxSignInFailures {
		if err := fail(other); !errors.Is(err, ErrPasskeyInvalid) {
			t.Fatalf("post-success failure %d = %v, want ErrPasskeyInvalid", i+1, err)
		}
	}
	if err := fail(other); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("post-success overflow = %v, want ErrTooManyAttempts", err)
	}
}

func TestPasskeyNames(t *testing.T) {
	ctx := context.Background()
	register := func(t *testing.T, svc *Service, a *authtest.Authenticator, name string) error {
		t.Helper()
		options, ceremonyID, err := svc.BeginRegistration(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("BeginRegistration: %s", err)
		}
		body, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if name != "" {
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("cannot add name: %s", err)
			}
			raw["name"] = name
			if body, err = json.Marshal(raw); err != nil {
				t.Fatalf("cannot add name: %s", err)
			}
		}
		return svc.FinishRegistration(ctx, AdminUsername, ceremonyID, body)
	}

	t.Run("default", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		if err := register(t, svc, a, ""); err != nil {
			t.Fatalf("FinishRegistration without a name: %s", err)
		}
		listed, err := svc.ListPasskeys(ctx, AdminUsername)
		if err != nil {
			t.Fatalf("ListPasskeys: %s", err)
		}
		if len(listed) != 1 || listed[0].Name != "Passkey" {
			t.Fatalf("ListPasskeys = %+v, want one key called Passkey", listed)
		}
	})
	t.Run("trimmed", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		if err := register(t, svc, a, "  Phone  "); err != nil {
			t.Fatalf("FinishRegistration: %s", err)
		}
		stored, err := svc.store.Passkeys().Get(ctx, a.CredentialID)
		if err != nil {
			t.Fatalf("Get: %s", err)
		}
		if stored.Name != "Phone" {
			t.Fatalf("name = %q, want trimmed Phone", stored.Name)
		}
	})
	t.Run("too long", func(t *testing.T) {
		svc, _ := passkeyFixture(t, testPublicURL)
		a := mustAuthenticator(t)
		if err := register(t, svc, a, strings.Repeat("n", maxPasskeyName+1)); !errors.Is(err, ErrPasskeyName) {
			t.Fatalf("FinishRegistration with a long name = %v, want ErrPasskeyName", err)
		}
	})
}

func TestPasskeyManagement(t *testing.T) {
	ctx := context.Background()
	svc, _ := passkeyFixture(t, testPublicURL)
	a := mustAuthenticator(t)
	b := mustAuthenticator(t)
	mustRegister(t, svc, a, "Phone")
	mustRegister(t, svc, b, "Laptop")

	listed, err := svc.ListPasskeys(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("ListPasskeys: %s", err)
	}
	// Same-second registrations tie on created_at and order by
	// credential id, so compare as a set here; the storetest suite
	// pins the order.
	var names []string
	for _, p := range listed {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Laptop", "Phone"}) {
		t.Fatalf("ListPasskeys = %+v, want both keys", listed)
	}
	if listed[0].BackedUp {
		t.Fatal("device-bound key reports backed up")
	}

	if err := svc.RenamePasskey(ctx, AdminUsername, a.CredentialID, "Tablet"); err != nil {
		t.Fatalf("RenamePasskey: %s", err)
	}
	stored, err := svc.store.Passkeys().Get(ctx, a.CredentialID)
	if err != nil {
		t.Fatalf("Get after rename: %s", err)
	}
	if stored.Name != "Tablet" {
		t.Fatalf("name = %q, want Tablet", stored.Name)
	}
	for _, name := range []string{"", "  ", strings.Repeat("n", maxPasskeyName+1)} {
		if err := svc.RenamePasskey(ctx, AdminUsername, a.CredentialID, name); !errors.Is(err, ErrPasskeyName) {
			t.Fatalf("RenamePasskey(%q) = %v, want ErrPasskeyName", name, err)
		}
	}
	if err := svc.RenamePasskey(ctx, AdminUsername, []byte("missing"), "Ghost"); !errors.Is(err, ErrPasskeyNotFound) {
		t.Fatalf("RenamePasskey missing = %v, want ErrPasskeyNotFound", err)
	}
	if err := svc.DeletePasskey(ctx, AdminUsername, []byte("missing")); !errors.Is(err, ErrPasskeyNotFound) {
		t.Fatalf("DeletePasskey missing = %v, want ErrPasskeyNotFound", err)
	}
	if err := svc.store.Users().Create(ctx, "bob", "hash"); err != nil {
		t.Fatalf("Create bob: %s", err)
	}
	if err := svc.RenamePasskey(ctx, "bob", a.CredentialID, "Theirs"); !errors.Is(err, ErrPasskeyNotFound) {
		t.Fatalf("RenamePasskey another user's = %v, want ErrPasskeyNotFound", err)
	}
	if err := svc.DeletePasskey(ctx, "bob", a.CredentialID); !errors.Is(err, ErrPasskeyNotFound) {
		t.Fatalf("DeletePasskey another user's = %v, want ErrPasskeyNotFound", err)
	}
	if err := svc.DeletePasskey(ctx, AdminUsername, a.CredentialID); err != nil {
		t.Fatalf("DeletePasskey: %s", err)
	}
	listed, err = svc.ListPasskeys(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("ListPasskeys after delete: %s", err)
	}
	if len(listed) != 1 || listed[0].Name != "Laptop" {
		t.Fatalf("ListPasskeys = %+v, want only Laptop left", listed)
	}
}

func TestPasskeyOptions(t *testing.T) {
	ctx := context.Background()
	svc, _ := passkeyFixture(t, testPublicURL)
	a := mustAuthenticator(t)
	mustRegister(t, svc, a, "")

	var creation struct {
		RP struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"user"`
		Challenge string `json:"challenge"`
		Params    []struct {
			Type      string `json:"type"`
			Algorithm int    `json:"alg"`
		} `json:"pubKeyCredParams"`
		Exclude []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"excludeCredentials"`
		Selection struct {
			ResidentKey      string `json:"residentKey"`
			UserVerification string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Attestation string `json:"attestation"`
		Timeout     int    `json:"timeout"`
	}
	options, _, err := svc.BeginRegistration(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("BeginRegistration: %s", err)
	}
	if err := json.Unmarshal(options, &creation); err != nil {
		t.Fatalf("creation options are not JSON: %s", err)
	}
	if creation.RP.ID != testRPID {
		t.Fatalf("rp.id = %q, want %q", creation.RP.ID, testRPID)
	}
	if creation.User.Name != AdminUsername {
		t.Fatalf("user.name = %q, want %q", creation.User.Name, AdminUsername)
	}
	handle, err := base64.RawURLEncoding.DecodeString(creation.User.ID)
	if err != nil || len(handle) != 32 {
		t.Fatalf("user.id is %q, want 32 base64url bytes", creation.User.ID)
	}
	storedHandle, err := svc.store.Users().EnsureWebAuthnHandle(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("EnsureWebAuthnHandle: %s", err)
	}
	if !bytes.Equal(handle, storedHandle) {
		t.Fatal("user.id is not the stored opaque handle")
	}
	rawChallenge, err := base64.RawURLEncoding.DecodeString(creation.Challenge)
	if err != nil || len(rawChallenge) != 32 {
		t.Fatalf("challenge is %q, want 32 base64url bytes", creation.Challenge)
	}
	if len(creation.Params) != 2 || creation.Params[0].Algorithm != -7 || creation.Params[1].Algorithm != -8 {
		t.Fatalf("pubKeyCredParams = %+v, want ES256 then EdDSA", creation.Params)
	}
	wantExclude := base64.RawURLEncoding.EncodeToString(a.CredentialID)
	if len(creation.Exclude) != 1 || creation.Exclude[0].ID != wantExclude {
		t.Fatalf("excludeCredentials = %+v, want the registered key", creation.Exclude)
	}
	if creation.Selection.ResidentKey != "required" || creation.Selection.UserVerification != "required" {
		t.Fatalf("authenticatorSelection = %+v, want resident key and user verification required", creation.Selection)
	}
	if creation.Attestation != "none" {
		t.Fatalf("attestation = %q, want none", creation.Attestation)
	}
	if creation.Timeout != int(CeremonyTTL.Milliseconds()) {
		t.Fatalf("timeout = %d, want the %s ceremony TTL in ms", creation.Timeout, CeremonyTTL)
	}

	var request struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
		Allow     []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
		Verification string `json:"userVerification"`
		Timeout      int    `json:"timeout"`
	}
	loginOptions, _, err := svc.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin: %s", err)
	}
	if err := json.Unmarshal(loginOptions, &request); err != nil {
		t.Fatalf("request options are not JSON: %s", err)
	}
	if request.RPID != testRPID {
		t.Fatalf("rpId = %q, want %q", request.RPID, testRPID)
	}
	if len(request.Allow) != 0 {
		t.Fatalf("allowCredentials = %+v, want none (discoverable login)", request.Allow)
	}
	if request.Verification != "required" {
		t.Fatalf("userVerification = %q, want required", request.Verification)
	}
	if request.Timeout != int(CeremonyTTL.Milliseconds()) {
		t.Fatalf("timeout = %d, want the %s ceremony TTL in ms", request.Timeout, CeremonyTTL)
	}
}

func TestPasskeyStatus(t *testing.T) {
	t.Run("on and off", func(t *testing.T) {
		svc, logs := passkeyFixture(t, testPublicURL)
		enabled, reason := svc.PasskeysStatus()
		if !enabled || reason != "" {
			t.Fatalf("status = (%v, %q), want enabled with no reason", enabled, reason)
		}
		if got := strings.Count(logs.String(), "rpID=localhost"); got != 1 {
			t.Fatalf("RP id logged %d times, want once:\n%s", got, logs.String())
		}
		if strings.Contains(logs.String(), "challenge") {
			t.Fatalf("logs mention challenges:\n%s", logs.String())
		}

		off, offLogs := passkeyFixture(t, "")
		enabled, reason = off.PasskeysStatus()
		if enabled || !strings.Contains(reason, "DEVCULES_PUBLIC_URL") {
			t.Fatalf("status without a public URL = (%v, %q), want disabled with next steps", enabled, reason)
		}
		if _, _, err := off.BeginRegistration(context.Background(), AdminUsername); !errors.Is(err, ErrPasskeysDisabled) {
			t.Fatalf("BeginRegistration while off = %v, want ErrPasskeysDisabled", err)
		}
		if _, _, err := off.BeginLogin(); !errors.Is(err, ErrPasskeysDisabled) {
			t.Fatalf("BeginLogin while off = %v, want ErrPasskeysDisabled", err)
		}
		if err := off.FinishRegistration(context.Background(), AdminUsername, "id", []byte("{}")); !errors.Is(err, ErrPasskeysDisabled) {
			t.Fatalf("FinishRegistration while off = %v, want ErrPasskeysDisabled", err)
		}
		if _, err := off.FinishLogin(context.Background(), "id", []byte("{}"), "10.1.0.16"); !errors.Is(err, ErrPasskeysDisabled) {
			t.Fatalf("FinishLogin while off = %v, want ErrPasskeysDisabled", err)
		}
		if !strings.Contains(offLogs.String(), "passkeys disabled") {
			t.Fatalf("logs lack the disabled note:\n%s", offLogs.String())
		}
		// Management still works while off: a disabled sibling over
		// the same store lists the registered key.
		a := mustAuthenticator(t)
		mustRegister(t, svc, a, "")
		sibling := New(svc.store, WithPasskeys("", nil))
		listed, err := sibling.ListPasskeys(context.Background(), AdminUsername)
		if err != nil {
			t.Fatalf("ListPasskeys while off: %s", err)
		}
		if len(listed) != 1 || !bytes.Equal(listed[0].ID, a.CredentialID) {
			t.Fatalf("ListPasskeys while off = %+v, want the registered key", listed)
		}
	})

	t.Run("reasons", func(t *testing.T) {
		cases := []struct {
			url      string
			enabled  bool
			contains string
		}{
			{"", false, "DEVCULES_PUBLIC_URL"},
			{"http://127.0.0.1:7659", false, "not an IP address"},
			{"http://100.64.0.1:7659", false, "not an IP address"},
			{"http://myserver:7659", false, "https, or http on localhost"},
			{"http://localhost:7659", true, ""},
			{"https://devcules.example.ts.net", true, ""},
		}
		for _, tc := range cases {
			svc, _ := passkeyFixture(t, tc.url)
			enabled, reason := svc.PasskeysStatus()
			if enabled != tc.enabled {
				t.Fatalf("status(%q) enabled = %v, want %v", tc.url, enabled, tc.enabled)
			}
			if !tc.enabled && !strings.Contains(reason, tc.contains) {
				t.Fatalf("status(%q) reason = %q, want it to mention %q", tc.url, reason, tc.contains)
			}
			if !tc.enabled && !strings.Contains(reason, "password keeps working") {
				t.Fatalf("status(%q) reason = %q, want the password fallback", tc.url, reason)
			}
		}
	})
}

func TestPasskeyCeremonyCap(t *testing.T) {
	svc, _ := passkeyFixture(t, testPublicURL)
	now := svc.now()
	// Fill the map directly: 1,000 ceremonies expiring in order.
	for i := range maxCeremonies {
		var raw [ceremonyIDBytes]byte
		raw[0] = byte(i >> 8)
		raw[1] = byte(i)
		id := base64.RawURLEncoding.EncodeToString(raw[:])
		svc.ceremonies[id] = ceremony{kind: ceremonyLogin, expires: now.Add(time.Duration(i+1) * time.Second)}
	}
	var first [ceremonyIDBytes]byte
	firstID := base64.RawURLEncoding.EncodeToString(first[:])
	if _, _, err := svc.BeginLogin(); err != nil {
		t.Fatalf("BeginLogin at the cap: %s", err)
	}
	if len(svc.ceremonies) != maxCeremonies {
		t.Fatalf("%d ceremonies filed, want the %d cap", len(svc.ceremonies), maxCeremonies)
	}
	if _, ok := svc.ceremonies[firstID]; ok {
		t.Fatal("earliest-expiring ceremony survived, want it evicted")
	}
}

func TestArgonCap(t *testing.T) {
	svc := New(openStore(t))
	mustBootstrap(t, svc)

	// Hold both slots: the third hash waits instead of hashing. The
	// waiter can only proceed once a slot frees, so its return after
	// the release below proves it waited — no timing involved.
	svc.argon <- struct{}{}
	svc.argon <- struct{}{}
	waiting := make(chan func(), 1)
	go func() {
		release, err := svc.argonSlot(context.Background())
		if err != nil {
			t.Errorf("argonSlot: %s", err)
			waiting <- nil
			return
		}
		waiting <- release
	}()
	<-svc.argon
	release := <-waiting
	if release == nil {
		t.Fatal("waiter failed")
	}
	if len(svc.argon) != maxArgonHashes {
		t.Fatalf("argon slots = %d after the waiter acquired, want %d", len(svc.argon), maxArgonHashes)
	}
	release()
	<-svc.argon

	// A cancelled wait fails with the context, without hashing.
	svc.argon <- struct{}{}
	svc.argon <- struct{}{}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := svc.SignIn(ctx, AdminUsername, "wrong", "10.1.0.17"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SignIn on a cancelled slot wait = %v, want context.Canceled", err)
	}
	<-svc.argon
	<-svc.argon
	if _, err := svc.SignIn(context.Background(), AdminUsername, "wrong", "10.1.0.17"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("SignIn after the slots drain = %v, want ErrInvalidCredentials", err)
	}
}

func TestFailuresMapBound(t *testing.T) {
	svc := New(openStore(t))
	now := svc.now()
	for i := range maxFailureIPs {
		ip := fmt.Sprintf("10.3.%d.%d", i>>8, i&0xff)
		svc.failures[ip] = []time.Time{now.Add(time.Duration(i) * time.Millisecond)}
	}
	// The map holds 10,000 distinct addresses; the oldest failure is
	// the i=0 entry.
	if len(svc.failures) != maxFailureIPs {
		t.Fatalf("%d failures filed, want %d", len(svc.failures), maxFailureIPs)
	}
	svc.recordFailure("10.9.9.9", now.Add(time.Hour))
	if len(svc.failures) != maxFailureIPs {
		t.Fatalf("%d failures after one more, want the bound held", len(svc.failures))
	}
	if _, ok := svc.failures["10.3.0.0"]; ok {
		t.Fatal("oldest entry survived, want it evicted")
	}
	if _, ok := svc.failures["10.9.9.9"]; !ok {
		t.Fatal("new entry missing after eviction")
	}
	// A known address appends without evicting.
	svc.recordFailure("10.9.9.9", now.Add(2*time.Hour))
	if len(svc.failures) != maxFailureIPs || len(svc.failures["10.9.9.9"]) != 2 {
		t.Fatal("recording a known address evicted or dropped")
	}
}
