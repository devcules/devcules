package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/store"
)

// Options wires the mux. Store serves readiness, Auth serves the auth
// routes, Web serves the built PWA, PublicURL (when set) joins the
// CSRF trusted origins so the browser UI can post from outside the
// serve host, and Link serves the runner WebSocket at GET /link.
// A nil Link answers /link with 503 and reports zero runners; serve
// always passes a real hub.
type Options struct {
	Store     store.Store
	Auth      *auth.Service
	Web       http.Handler
	PublicURL string
	Link      *hub.Hub
}

// Handler returns the M1.3 mux. /healthz, /readyz and GET /link stay
// public (/link authenticates with its own credential, never the session
// cookie); POST /auth/sign-in is the public login call; the static
// app (Web) is public too, since it holds no data; everything else
// needs a live session and answers 401 without one. Only GET routes
// are registered for reads: the mux itself answers 405 to wrong
// methods. Every response carries the security headers.
func Handler(o Options) http.Handler {
	// Patterns carry no method on purpose: the "/" catch-all below
	// would swallow method-mismatched requests before the mux could
	// answer 405, so each handler enforces its own method instead.
	// "/link" still wins over "/": the mux prefers the longer pattern.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz(o.Store, o.Link))
	if o.Link != nil {
		mux.Handle("/link", o.Link.Handler())
	} else {
		mux.HandleFunc("/link", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "link not configured", http.StatusServiceUnavailable)
		})
	}
	mux.Handle("/auth/sign-in", csrf(o.PublicURL, signIn(o.Auth)))
	mux.Handle("/auth/sign-out", csrf(o.PublicURL, requireSession(o.Auth, signOut(o.Auth))))
	mux.Handle("/auth/me", requireSession(o.Auth, me()))
	mux.Handle("/", root(o.Web, o.Auth))
	return secureHeaders(mux)
}

// root serves the app shell and guards the unknown. Reads outside
// /auth/* belong to the static app (public: it holds no data and
// checks the session itself), with page navigations falling back to
// the shell; anything else needs a session, and answers 401 without
// one. /auth/* never falls back to the app.
func root(web http.Handler, svc *auth.Service) http.HandlerFunc {
	rest := requireSession(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	return func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasPrefix(r.URL.Path, "/auth/") {
			web.ServeHTTP(w, r)
			return
		}
		rest.ServeHTTP(w, r)
	}
}

// secureHeaders stamps every response: no inline scripts or frames
// from anywhere, no MIME sniffing, no referrer. base-uri and
// form-action are spelled out because they do not fall back to
// default-src.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// requireMethod rejects anything but method with 405. It lives in the
// handlers (not the mux patterns) so mismatches surface as 405
// instead of falling through to the catch-all's 401.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// healthz answers 200 whenever the process is up. Only GET is allowed.
func healthz(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyzBody is the /readyz JSON shape. Field order is stable by
// construction so smoke tests and docs can quote exact bytes.
type readyzBody struct {
	Status        string `json:"status"`
	RunnersLinked int    `json:"runners_linked"`
}

// readyz answers 503 only when the store is down, with a fixed reason:
// the underlying error can name paths, so it is never repeated to the
// caller. Otherwise it answers JSON with the linked runner count, so
// the UI can show "runner offline" instead of failing readiness — a
// deliberate deviation from PLAN.md §6, which wanted readiness to
// require a linked runner when runners are configured. That would
// deadlock the compose healthcheck (the runner waits for serve to be
// healthy) and hide the UI that reports the outage.
func readyz(s store.Store, link *hub.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		if err := s.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: store unreachable"))
			return
		}
		runners := 0
		if link != nil {
			runners = link.Count()
		}
		// A struct (not a map) keeps the documented key order stable.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(readyzBody{Status: "ok", RunnersLinked: runners})
	}
}

// signIn checks the posted form and opens a session. Success sets
// the cookie and answers 204: the React app is in charge, so there
// is no redirect to follow. Failures are JSON the app renders as
// plain words. A wrong password and an unknown user share one 401 so
// logins cannot be enumerated.
func signIn(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		// PostFormValue parses on first use and treats parse
		// failures as empty: missing or broken fields fail closed
		// as invalid credentials below.
		value, err := svc.SignIn(r.Context(),
			r.PostFormValue("username"), r.PostFormValue("password"), clientIP(r))
		switch {
		case errors.Is(err, auth.ErrTooManyAttempts):
			w.Header().Set("Retry-After", "60")
			writeAuthError(w, http.StatusTooManyRequests, "too many sign-in attempts, try again later")
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeAuthError(w, http.StatusUnauthorized, "invalid username or password")
		case err != nil:
			// Storage failures stay server-side: the message must
			// never name files or drivers.
			writeAuthError(w, http.StatusInternalServerError, "internal error")
		default:
			http.SetCookie(w, &http.Cookie{
				Name:     auth.CookieName,
				Value:    value,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteStrictMode,
				// The browser drops the cookie at the absolute
				// timeout even if the server never sees it again.
				MaxAge: int(auth.AbsoluteTimeout.Seconds()),
			})
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// signOut closes the session and clears the cookie. It runs behind
// requireSession, so the cookie it reads is known good.
func signOut(svc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		// requireSession guarantees the cookie, but a miswired mux
		// must fail closed, never panic on a nil cookie.
		c, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := svc.SignOut(r.Context(), c.Value); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
			Expires:  time.Unix(1, 0).UTC(),
		})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

// me reports the session's username.
func me() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		username, ok := r.Context().Value(usernameKey{}).(string)
		if !ok {
			// Unreachable through requireSession, which always sets
			// the username before calling on: a miswired mux must
			// fail loudly, never as another user.
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":` + strconv.Quote(username) + "}\n"))
	}
}

type usernameKey struct{}

// requireSession rejects requests without a live session with 401
// and hands the username to the next handler through the request
// context.
func requireSession(svc *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		username, err := svc.Authenticate(r.Context(), c.Value)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), usernameKey{}, username)))
	})
}

// csrf wraps state-changing routes in the stdlib origin check, which
// answers cross-site POSTs with 403. Requests without Origin or
// Sec-Fetch-Site (curl, non-browser clients) are allowed: there is
// nothing to compare. DEVCULES_PUBLIC_URL, already validated as an
// http(s) URL by config, is trusted by origin (scheme plus host), so
// the later browser UI can post from its own origin. Anything
// unparseable trusts nothing: fail closed.
func csrf(publicURL string, next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	if u, err := url.Parse(publicURL); err == nil && u.Scheme != "" && u.Host != "" {
		// Built from parsed parts, so this cannot fail; the error
		// is discarded rather than branched on.
		_ = cop.AddTrustedOrigin(u.Scheme + "://" + u.Host)
	}
	return cop.Handler(next)
}

// clientIP is the connection's remote address without its port. No
// proxy headers are trusted: there is no proxy in M1.1.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeAuthError answers API failures as JSON. Messages are fixed
// strings chosen at the call site, never error text: storage and
// driver details stay server-side.
func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + strconv.Quote(message) + "}\n"))
}
