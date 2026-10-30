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

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/machines"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
	"github.com/devcules/devcules/internal/push"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/terminals/relay"
	"github.com/devcules/devcules/internal/trace"
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
	// Terminals relays browser terminals to the runner. A nil relay
	// answers the terminal routes with 503; serve always passes a
	// real one.
	Terminals *relay.Relay
	// Redact and RedactSettings serve the redaction settings and test
	// box. Nil answers those routes with 503; serve always passes
	// both.
	Redact         *redact.Redactor
	RedactSettings *redact.Settings
	// Router serves /llm/{provider}/... for agents holding a launch
	// token (never the session cookie); Launches mints and revokes
	// those tokens. Nil answers their routes with 503.
	Router   *router.Router
	Launches *launch.Tokens
	// AuditLog records who did what; TraceStore serves the model call
	// traces. A nil AuditLog records nothing and answers its routes with
	// 503; so does a nil TraceStore.
	AuditLog   *audit.Log
	TraceStore *trace.Store
	// MCP serves POST /mcp, the MCP server agents reach with their
	// launch token (internal/mcp). Nil answers it with 503.
	MCP http.Handler
	// ApprovalQueue serves the approval queue and its settings, and the
	// hook side at /hook/approvals (launch tokens). Push serves Web
	// Push subscriptions. Nil answers their routes with 503.
	ApprovalQueue *approvals.Service
	Push          *push.Service
	// Enrol mints machine enrolment codes; nil answers the machine
	// routes with 503.
	Enrol *machines.Issuer
	// MachineStore lists and revokes enrolled machines.
	MachineStore *machines.Service
	// Projects stores the Projects tab's named directories; nil answers
	// its routes with 503.
	Projects store.ProjectsRepo
	// Machines relays the machine task view (screen, input, tree,
	// grounding, done-verify) to the linked machine helpers. Nil
	// answers those routes with 503; serve always passes a real one.
	Machines *machrelay.Relay
}

// Handler returns the M3.2 mux. /healthz, /readyz, GET /link, /llm/,
// /mcp and /hook/ stay public (/link, the model router, the MCP server
// and the approval hooks authenticate with their own credentials, the
// runner token and launch tokens, never the session cookie); POST /auth/sign-in is the public password call, the
// passkey login pair and the passkey status read are public too; the
// static app (Web) is public too, since it holds no data; everything
// else needs a live session and answers 401 without one. Only GET
// routes are registered for reads: the mux itself answers 405 to
// wrong methods. Every response carries the security headers.
func Handler(o Options) http.Handler {
	// Patterns carry no method on purpose: the "/" catch-all below
	// would swallow method-mismatched requests before the mux could
	// answer 405, so each handler enforces its own method instead.
	// "/link" still wins over "/": the mux prefers the longer pattern.
	mux := http.NewServeMux()
	rec := auditor{o.AuditLog}
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz(o.Store, o.Link))
	if o.Link != nil {
		mux.Handle("/link", o.Link.Handler())
	} else {
		mux.HandleFunc("/link", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "link not configured", http.StatusServiceUnavailable)
		})
	}
	mux.Handle("/auth/sign-in", csrf(o.PublicURL, signIn(o.Auth, rec)))
	mux.Handle("/auth/sign-out", csrf(o.PublicURL, requireSession(o.Auth, signOut(o.Auth, rec))))
	mux.Handle("/auth/me", requireSession(o.Auth, me()))
	// Passkey ceremonies: registration needs a session, sign-in is
	// public, and every POST carries the CSRF check. The status read
	// is public so the sign-in screen can ask before offering
	// passkeys.
	mux.Handle("/auth/passkeys/status", passkeyStatus(o.Auth))
	mux.Handle("/auth/passkeys/register/begin", csrf(o.PublicURL, requireSession(o.Auth, passkeyRegisterBegin(o.Auth))))
	mux.Handle("/auth/passkeys/register/finish", csrf(o.PublicURL, requireSession(o.Auth, passkeyRegisterFinish(o.Auth, rec))))
	mux.Handle("/auth/passkeys/login/begin", csrf(o.PublicURL, passkeyLoginBegin(o.Auth)))
	mux.Handle("/auth/passkeys/login/finish", csrf(o.PublicURL, passkeyLoginFinish(o.Auth, rec)))
	// Passkey management sorts above "/" like the terminal routes:
	// the mux prefers longer patterns, so /api/passkeys never falls
	// through to the app shell.
	mux.Handle("/api/passkeys", requireSession(o.Auth, passkeysRoot(o.Auth)))
	mux.Handle("/api/passkeys/", requireSession(o.Auth, passkeyOne(o.Auth, o.PublicURL, rec)))
	// Terminal routes sort above "/": the mux prefers longer patterns,
	// so /api/terminals never falls through to the app shell. The
	// collection holds list and open; anything deeper is one terminal
	// (DELETE) or its stream (GET upgrade).
	mux.Handle("/api/terminals", requireSession(o.Auth, terminalsRoot(o.Terminals, o.PublicURL, rec)))
	mux.Handle("/api/terminals/", requireSession(o.Auth, terminalOne(o.Terminals, o.PublicURL, rec)))
	// Projects: a named directory on a runner or machine; terminals
	// open in it. Reads need a session, writes add the CSRF check.
	mux.Handle("/api/projects", requireSession(o.Auth, projectsRoot(o.Projects, o.Terminals, o.PublicURL, rec)))
	mux.Handle("/api/projects/", csrf(o.PublicURL, requireSession(o.Auth, projectOne(o.Projects, o.Terminals, rec))))
	mux.Handle("/api/browse", requireSession(o.Auth, browseRoot(o.Terminals)))
	// Redaction settings: reads need a session, the policy write and
	// the test box add the CSRF check.
	mux.Handle("/api/redact", requireSession(o.Auth, redactRoot(o.Redact, o.RedactSettings)))
	mux.Handle("/api/redact/policy", csrf(o.PublicURL, requireSession(o.Auth, redactPolicy(o.Redact, o.RedactSettings, rec))))
	mux.Handle("/api/redact/test", csrf(o.PublicURL, requireSession(o.Auth, redactTest(o.Redact, o.RedactSettings))))
	// The model router authenticates with launch tokens of its own,
	// like /link: no session, no CSRF (agents are not browsers).
	if o.Router != nil {
		mux.Handle("/llm/", o.Router)
	} else {
		mux.HandleFunc("/llm/", func(w http.ResponseWriter, r *http.Request) {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
		})
	}
	// The MCP server takes launch tokens too, and checks Origin itself.
	if o.MCP != nil {
		mux.Handle("/mcp", o.MCP)
	} else {
		mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			writeAuthError(w, http.StatusServiceUnavailable, "MCP server not configured")
		})
	}
	// Approvals: the queue and settings need a session (writes add
	// the CSRF check); the hook side takes launch tokens.
	runners := func() []string { return nil }
	if o.Link != nil {
		runners = o.Link.Runners
	}
	mux.Handle("/api/approvals", requireSession(o.Auth, approvalsRoot(o.ApprovalQueue)))
	mux.Handle("/api/approvals/", requireSession(o.Auth, approvalOne(o.ApprovalQueue, runners, o.PublicURL)))
	mux.Handle("/hook/approvals", hookApprovals(o.ApprovalQueue, o.Launches))
	mux.Handle("/hook/approvals/", hookApprovals(o.ApprovalQueue, o.Launches))
	mux.Handle("/api/push", requireSession(o.Auth, pushRoot(o.Push)))
	mux.Handle("/api/push/", requireSession(o.Auth, pushOne(o.Push, o.PublicURL, rec)))
	providers := func() []string { return nil }
	if o.Router != nil {
		providers = o.Router.Providers
	}
	var ready func(context.Context, string) bool
	if o.Router != nil {
		ready = func(ctx context.Context, id string) bool { ok, _ := o.Router.Ready(ctx, id); return ok }
	}
	mux.Handle("/api/agents", requireSession(o.Auth, agentsRoot(o.Terminals, o.Launches, o.ApprovalQueue, o.Projects, providers, ready, o.PublicURL, rec)))
	// Providers: which ones have a key, and each one's model list,
	// fetched by serve with the vault key (never the browser).
	mux.Handle("/api/providers", requireSession(o.Auth, providersRoot(o.Router)))
	mux.Handle("/api/providers/", requireSession(o.Auth, providerModels(o.Router)))
	mux.Handle("/api/launch-tokens", requireSession(o.Auth, launchesRoot(o.Launches, providers, o.PublicURL, rec)))
	mux.Handle("/api/launch-tokens/", requireSession(o.Auth, launchOne(o.Launches, o.PublicURL, rec)))
	// Audit and traces: reads need a session, and reading a trace or
	// exporting the log is itself audited.
	mux.Handle("/api/audit", requireSession(o.Auth, auditRoot(o.AuditLog)))
	mux.Handle("/api/audit/export", requireSession(o.Auth, auditExport(o.AuditLog, rec)))
	mux.Handle("/api/traces", requireSession(o.Auth, tracesRoot(o.TraceStore, o.Launches)))
	mux.Handle("/api/traces/", requireSession(o.Auth, traceOne(o.TraceStore, o.Launches, rec)))
	// Machines: the owner mints an enrolment code in the UI (session
	// and CSRF) and types it on the computer, which enrols itself with
	// its key (no session: machines are not browsers, and the code
	// plus a signature is the credential). Removing revokes the key.
	kick := func(string) {}
	if o.Link != nil {
		kick = func(name string) { o.Link.Kick(hub.MachineAddr(name), "machine removed") }
	}
	mux.Handle("/api/machines/enrol-code", csrf(o.PublicURL, requireSession(o.Auth, machinesEnrolCode(o.Enrol, rec))))
	mux.Handle("/api/machines/enrol", machinesEnrol(o.Enrol, newEnrolLimiter(), rec))
	mux.Handle("/api/machines/", csrf(o.PublicURL, requireSession(o.Auth, machineOne(o.MachineStore, kick, rec))))
	// Machine task view (M4.5): the live screen, the accessibility
	// tree, input injection (take control), SAM grounding and the
	// done-verifier, all session-guarded, all over the link relay.
	mux.Handle("/api/machines", requireSession(o.Auth, machinesList(o.Machines, o.MachineStore)))
	mux.Handle("/api/machines-budget", requireSession(o.Auth, machinesBudget(o.Machines)))
	mux.Handle("/api/machines-screen", requireSession(o.Auth, machinesScreen(o.Machines)))
	mux.Handle("/api/machines-tree", requireSession(o.Auth, machinesTree(o.Machines)))
	mux.Handle("/api/machines-input", requireSession(o.Auth, machinesInput(o.Machines, o.PublicURL)))
	mux.Handle("/api/machines-ground", requireSession(o.Auth, machinesGround(o.Machines, o.PublicURL)))
	mux.Handle("/api/machines-verify", requireSession(o.Auth, machinesVerify(o.Machines, o.PublicURL)))
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
// default-src. style-src allows xterm.js's inline styles (M1.4): the
// terminal renderer sets style attributes and a <style> element at
// runtime, so the Terminals tab stays blank without it. script-src
// stays default-src 'self': no inline script, ever.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
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
func signIn(svc *auth.Service, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		// PostFormValue parses on first use and treats parse
		// failures as empty: missing or broken fields fail closed
		// as invalid credentials below.
		username := r.PostFormValue("username")
		value, err := svc.SignIn(r.Context(), username, r.PostFormValue("password"), clientIP(r))
		switch {
		case errors.Is(err, auth.ErrTooManyAttempts):
			rec.signInFailed(r, "password", "throttled")
			w.Header().Set("Retry-After", "60")
			writeAuthError(w, http.StatusTooManyRequests, "too many sign-in attempts, try again later")
		case errors.Is(err, auth.ErrInvalidCredentials):
			rec.signInFailed(r, "password", "invalid")
			writeAuthError(w, http.StatusUnauthorized, "invalid username or password")
		case err != nil:
			// Storage failures stay server-side: the message must
			// never name files or drivers.
			writeAuthError(w, http.StatusInternalServerError, "internal error")
		default:
			rec.as(r, username, audit.SignIn, "", map[string]string{"method": "password"})
			setSessionCookie(w, value)
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// setSessionCookie files the session value: HttpOnly, Secure and
// SameSite=Strict on Path=/, dropped by the browser at the absolute
// timeout even if the server never sees it again. Password and
// passkey sign-ins share it byte for byte.
func setSessionCookie(w http.ResponseWriter, value string) {
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
}

// signOut closes the session and clears the cookie. It runs behind
// requireSession, so the cookie it reads is known good.
func signOut(svc *auth.Service, rec auditor) http.HandlerFunc {
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
		rec.record(r, audit.SignOut, "", nil)
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
		username, ok := requestUsername(r)
		if !ok {
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
