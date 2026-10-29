package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
)

const (
	// ceremonyCookieName carries the in-flight ceremony id. It is
	// scoped to /auth/passkeys and lives only for the ceremony.
	ceremonyCookieName = "devcules_ceremony"
	// maxPasskeyBody caps ceremony and rename bodies. Creation and
	// assertion responses are kilobytes; a megabyte is generous and
	// still DoS-safe.
	maxPasskeyBody = 1 << 20
)

// requestUsername returns the session username requireSession filed
// in the context. False means a miswired mux: fail loudly, never as
// another user.
func requestUsername(r *http.Request) (string, bool) {
	username, ok := r.Context().Value(usernameKey{}).(string)
	return username, ok
}

// passkeyStatus answers GET /auth/passkeys/status, public: whether
// passkeys are usable, or why not in plain words. The sign-in screen
// reads it before deciding to offer passkeys.
func passkeyStatus(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		enabled, reason := svc.PasskeysStatus()
		w.Header().Set("Content-Type", "application/json")
		if enabled {
			_, _ = w.Write([]byte("{\"enabled\":true}\n"))
			return
		}
		_, _ = w.Write([]byte("{\"enabled\":false,\"reason\":" + strconv.Quote(reason) + "}\n"))
	}
}

// passkeyRegisterBegin opens a register ceremony for the session
// user: 200 with the creation options, and the ceremony id in the
// ceremony cookie.
func passkeyRegisterBegin(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		username, ok := requestUsername(r)
		if !ok {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		options, ceremonyID, err := svc.BeginRegistration(r.Context(), username)
		if err != nil {
			if errors.Is(err, auth.ErrPasskeysDisabled) {
				writeAuthError(w, http.StatusBadRequest, "passkeys are not enabled")
				return
			}
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		setCeremonyCookie(w, ceremonyID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(options, '\n'))
	}
}

// passkeyRegisterFinish verifies the creation response and stores
// the credential: 201. Failures are 400, never 401: the session is
// fine, the ceremony is not, and the UI must not route to sign-in.
func passkeyRegisterFinish(svc *auth.Service, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		username, ok := requestUsername(r)
		if !ok {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		ceremonyID, ok := ceremonyCookie(r)
		if !ok {
			writeAuthError(w, http.StatusBadRequest, "passkey ceremony expired or invalid")
			return
		}
		// A short or empty body fails closed in auth as an invalid
		// response; read errors need no status of their own.
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
		clearCeremonyCookie(w)
		switch err := svc.FinishRegistration(r.Context(), username, ceremonyID, body); {
		case err == nil:
			rec.record(r, audit.PasskeyAdd, "", nil)
			w.WriteHeader(http.StatusCreated)
		case errors.Is(err, auth.ErrPasskeysDisabled):
			writeAuthError(w, http.StatusBadRequest, "passkeys are not enabled")
		case errors.Is(err, auth.ErrBadCeremony):
			writeAuthError(w, http.StatusBadRequest, "passkey ceremony expired or invalid")
		case errors.Is(err, auth.ErrPasskeyRegistered):
			writeAuthError(w, http.StatusBadRequest, "this passkey is already registered")
		case errors.Is(err, auth.ErrPasskeyName):
			writeAuthError(w, http.StatusBadRequest, "invalid passkey name")
		case errors.Is(err, auth.ErrPasskeyInvalid):
			writeAuthError(w, http.StatusBadRequest, "passkey verification failed")
		default:
			writeAuthError(w, http.StatusInternalServerError, "internal error")
		}
	}
}

// passkeyLoginBegin opens a discoverable login ceremony, public: 200
// with the request options, and the ceremony id in the ceremony
// cookie.
func passkeyLoginBegin(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		options, ceremonyID, err := svc.BeginLogin()
		if err != nil {
			if errors.Is(err, auth.ErrPasskeysDisabled) {
				writeAuthError(w, http.StatusBadRequest, "passkeys are not enabled")
				return
			}
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		setCeremonyCookie(w, ceremonyID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(options, '\n'))
	}
}

// passkeyLoginFinish verifies the assertion and opens a session
// exactly like a password sign-in: 204 with the session cookie. A
// failed assertion is 401 and counts against the shared per-IP
// limiter; a bad ceremony is 400 and uncounted.
func passkeyLoginFinish(svc *auth.Service, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		ceremonyID, ok := ceremonyCookie(r)
		if !ok {
			writeAuthError(w, http.StatusBadRequest, "passkey ceremony expired or invalid")
			return
		}
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
		clearCeremonyCookie(w)
		value, err := svc.FinishLogin(r.Context(), ceremonyID, body, clientIP(r))
		switch {
		case err == nil:
			if rec.log != nil {
				// The assertion names no user; the new session does.
				username, _ := svc.Authenticate(r.Context(), value)
				rec.as(r, username, audit.SignIn, "", map[string]string{"method": "passkey"})
			}
			setSessionCookie(w, value)
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, auth.ErrTooManyAttempts):
			rec.signInFailed(r, "passkey", "throttled")
			w.Header().Set("Retry-After", "60")
			writeAuthError(w, http.StatusTooManyRequests, "too many sign-in attempts, try again later")
		case errors.Is(err, auth.ErrBadCeremony):
			writeAuthError(w, http.StatusBadRequest, "passkey ceremony expired or invalid")
		case errors.Is(err, auth.ErrPasskeysDisabled):
			writeAuthError(w, http.StatusBadRequest, "passkeys are not enabled")
		case errors.Is(err, auth.ErrPasskeyInvalid):
			rec.signInFailed(r, "passkey", "invalid")
			writeAuthError(w, http.StatusUnauthorized, "passkey verification failed")
		default:
			writeAuthError(w, http.StatusInternalServerError, "internal error")
		}
	}
}

// setCeremonyCookie files the ceremony id: HttpOnly and
// SameSite=Strict so only same-site scripts and requests carry it,
// scoped to /auth/passkeys, and short-lived like the challenge. It
// is deliberately not Secure, unlike the session cookie: the id
// alone completes nothing (only the passkey's private key signs),
// and it is single use with a five-minute life.
func setCeremonyCookie(w http.ResponseWriter, ceremonyID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     ceremonyCookieName,
		Value:    ceremonyID,
		Path:     "/auth/passkeys",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.CeremonyTTL.Seconds()),
	})
}

// clearCeremonyCookie retires the id after a finish consumed it.
func clearCeremonyCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     ceremonyCookieName,
		Value:    "",
		Path:     "/auth/passkeys",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	})
}

// ceremonyCookie reads the ceremony id. A missing or empty cookie is
// not a ceremony.
func ceremonyCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(ceremonyCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// passkeysRoot serves the GET /api/passkeys collection: the session
// user's passkeys as id, name, unix times and backup state. Reads
// need a session; there are no writes here, so no CSRF check.
func passkeysRoot(svc *auth.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		username, ok := requestUsername(r)
		if !ok {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		listed, err := svc.ListPasskeys(r.Context(), username)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		type entry struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Created  int64  `json:"created"`
			LastUsed int64  `json:"lastUsed"`
			BackedUp bool   `json:"backedUp"`
		}
		entries := make([]entry, len(listed))
		for i, p := range listed {
			entries[i] = entry{
				ID:       base64.RawURLEncoding.EncodeToString(p.ID),
				Name:     p.Name,
				Created:  p.CreatedAt.Unix(),
				LastUsed: p.LastUsed.Unix(),
				BackedUp: p.BackedUp,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Passkeys []entry `json:"passkeys"`
		}{Passkeys: entries})
	})
}

// passkeyOne serves one passkey under /api/passkeys/{id}, the
// credential id base64url-encoded: PATCH renames it, DELETE removes
// it idempotently (unknown ids still 204). Writes need a session
// plus the CSRF origin check.
func passkeyOne(svc *auth.Service, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := passkeyPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		rawID, err := base64.RawURLEncoding.DecodeString(id)
		if err != nil || len(rawID) == 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				username, ok := requestUsername(r)
				if !ok {
					writeAuthError(w, http.StatusInternalServerError, "internal error")
					return
				}
				body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
				var named struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(body, &named); err != nil {
					writeAuthError(w, http.StatusBadRequest, "invalid passkey name")
					return
				}
				switch err := svc.RenamePasskey(r.Context(), username, rawID, named.Name); {
				case err == nil:
					rec.record(r, audit.PasskeyRename, id, map[string]string{"name": named.Name})
					w.WriteHeader(http.StatusNoContent)
				case errors.Is(err, auth.ErrPasskeyName):
					writeAuthError(w, http.StatusBadRequest, "invalid passkey name")
				case errors.Is(err, auth.ErrPasskeyNotFound):
					writeAuthError(w, http.StatusNotFound, "passkey not found")
				default:
					writeAuthError(w, http.StatusInternalServerError, "internal error")
				}
			})).ServeHTTP(w, r)
		case http.MethodDelete:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				username, ok := requestUsername(r)
				if !ok {
					writeAuthError(w, http.StatusInternalServerError, "internal error")
					return
				}
				// Idempotent: unknown ids are already gone.
				err := svc.DeletePasskey(r.Context(), username, rawID)
				if err != nil && !errors.Is(err, auth.ErrPasskeyNotFound) {
					writeAuthError(w, http.StatusInternalServerError, "internal error")
					return
				}
				if err == nil {
					rec.record(r, audit.PasskeyRemove, id, nil)
				}
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "PATCH, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// passkeyPath splits /api/passkeys/{id}. Anything else is not a
// passkey route.
func passkeyPath(path string) (string, bool) {
	const prefix = "/api/passkeys/"
	rest, found := strings.CutPrefix(path, prefix)
	if !found || rest == "" || hasSlash(rest) {
		return "", false
	}
	return rest, true
}
