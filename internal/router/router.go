package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
)

const (
	// maxRequestBody caps one model request. Coding agents send whole
	// files as context; 32 MiB is generous and still bounded.
	maxRequestBody = 32 << 20
	// maxReplyBody caps a non-streamed reply the router must decode.
	maxReplyBody = 64 << 20
	// bodyReadWindow is how long a client gets to send its request
	// body; the server-wide read timeout is too short for big ones.
	bodyReadWindow = 2 * time.Minute
)

// KeySource reads provider keys. The vault is the real one.
type KeySource interface {
	Get(ctx context.Context, name string) ([]byte, error)
}

// Options wires a Router. Every field but Client and Log is required.
type Options struct {
	Providers []Provider
	Keys      KeySource
	Tokens    *launch.Tokens
	Redactor  *redact.Redactor
	Settings  *redact.Settings
	// Client reaches the providers. Nil uses a client with no overall
	// timeout (replies stream for minutes) but a bounded wait for
	// response headers.
	Client *http.Client
	Log    *slog.Logger
}

// Router serves /llm/{provider}/... .
type Router struct {
	providers map[string]Provider
	o         Options
}

// New builds a Router. Providers with a duplicate id are a wiring bug.
func New(o Options) (*Router, error) {
	if o.Keys == nil || o.Tokens == nil || o.Redactor == nil || o.Settings == nil {
		return nil, errors.New("router: keys, tokens, redactor and settings are required")
	}
	rt := &Router{providers: map[string]Provider{}, o: o}
	for _, p := range o.Providers {
		if _, dup := rt.providers[p.ID()]; dup {
			return nil, fmt.Errorf("router: provider %q registered twice", p.ID())
		}
		rt.providers[p.ID()] = p
	}
	if rt.o.Client == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = 10 * time.Minute
		rt.o.Client = &http.Client{Transport: t}
	}
	if rt.o.Log == nil {
		rt.o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return rt, nil
}

// Providers lists the registered provider ids.
func (rt *Router) Providers() []string {
	out := make([]string, 0, len(rt.providers))
	for id := range rt.providers {
		out = append(out, id)
	}
	return out
}

// routed reports whether method and path (after /llm/{provider}/) are
// a call the router forwards.
func routed(method, path string) (ok, methodOK bool) {
	switch path {
	case "v1/chat/completions", "v1/responses", "v1/messages", "v1/messages/count_tokens":
		return true, method == http.MethodPost
	case "v1/models":
		return true, method == http.MethodGet
	}
	if id, found := strings.CutPrefix(path, "v1/models/"); found && id != "" && !strings.Contains(id, "/") {
		return true, method == http.MethodGet
	}
	return false, false
}

// requestHeaders are the client headers a provider may need; nothing
// else (cookies, the launch token, proxy headers) is forwarded.
var requestHeaders = []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta", "Idempotency-Key"}

// forwardResponseHeader reports whether an upstream header goes back
// to the client: the content type, retry and rate-limit hints, and the
// provider's request id for support tickets.
func forwardResponseHeader(name string) bool {
	switch name {
	case "Content-Type", "Retry-After", "Request-Id", "X-Request-Id", "Cache-Control":
		return true
	}
	return strings.HasPrefix(name, "X-Ratelimit-") || strings.HasPrefix(name, "Anthropic-Ratelimit-")
}

// launchToken takes the token from Authorization: Bearer (OpenAI SDKs)
// or x-api-key (Anthropic SDKs).
func launchToken(r *http.Request) string {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

// call is what the log line records about one forwarded call.
type call struct {
	provider, path, launch string
	status                 int
	stream                 bool
	findings               []redact.Finding
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rest, _ := strings.CutPrefix(r.URL.Path, "/llm/")
	id, path, _ := strings.Cut(rest, "/")
	anthropic := strings.HasPrefix(path, "v1/messages")
	c := call{provider: id, path: path}
	defer func() {
		counts := map[redact.Type]int{}
		for _, f := range c.findings {
			counts[f.Type]++
		}
		attrs := []any{"provider", c.provider, "path", c.path, "status", c.status, "ms", time.Since(start).Milliseconds(), "stream", c.stream}
		if c.launch != "" {
			attrs = append(attrs, "launch", c.launch)
		}
		for _, t := range redact.Types {
			if counts[t] > 0 {
				attrs = append(attrs, "redacted_"+strings.ToLower(string(t)), counts[t])
			}
		}
		rt.o.Log.Info("llm call", attrs...)
	}()
	fail := func(status int, kind, msg string) {
		c.status = status
		writeError(w, anthropic, status, kind, msg)
	}

	p, ok := rt.providers[id]
	if !ok {
		fail(http.StatusNotFound, "not_found_error", fmt.Sprintf("unknown provider %q (routed: %s)", id, strings.Join(rt.Providers(), ", ")))
		return
	}
	known, methodOK := routed(r.Method, path)
	if !known {
		fail(http.StatusNotFound, "not_found_error", fmt.Sprintf("the router does not forward %q: it routes v1/chat/completions, v1/responses, v1/messages, v1/messages/count_tokens and v1/models", path))
		return
	}
	if !methodOK {
		fail(http.StatusMethodNotAllowed, "invalid_request_error", r.Method+" is not allowed on "+path)
		return
	}

	token := launchToken(r)
	if token == "" {
		fail(http.StatusUnauthorized, "authentication_error", "missing launch token: send it as Authorization: Bearer <token> or x-api-key")
		return
	}
	l, err := rt.o.Tokens.Check(r.Context(), token)
	c.launch = l.ID
	switch {
	case errors.Is(err, launch.ErrInvalid):
		fail(http.StatusUnauthorized, "authentication_error", "invalid launch token")
		return
	case errors.Is(err, launch.ErrExpired):
		fail(http.StatusUnauthorized, "authentication_error", "launch token expired: start the agent again for a fresh one")
		return
	case errors.Is(err, launch.ErrRevoked):
		fail(http.StatusUnauthorized, "authentication_error", "launch token revoked")
		return
	case err != nil:
		rt.o.Log.Error("launch token check failed", "err", err.Error())
		fail(http.StatusInternalServerError, "api_error", "internal error")
		return
	}
	if !l.Allows(id) {
		fail(http.StatusForbidden, "permission_error", fmt.Sprintf("this launch token is for provider %q, not %q", l.Provider, id))
		return
	}

	// Model calls outlive the server-wide timeouts: give the body a
	// read window and the reply no write deadline at all.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadWindow))
	_ = rc.SetWriteDeadline(time.Time{})

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(http.StatusRequestEntityTooLarge, "invalid_request_error", "request body is larger than 32 MiB")
			return
		}
		fail(http.StatusBadRequest, "invalid_request_error", "could not read the request body")
		return
	}

	key, err := rt.o.Keys.Get(r.Context(), KeySecret(id))
	if err != nil || len(key) == 0 {
		fail(http.StatusServiceUnavailable, "api_error", fmt.Sprintf("no API key stored for provider %q: set its key (for meta, META_API_KEY) and restart serve", id))
		return
	}

	session := rt.o.Redactor.NewSession(rt.o.Settings.Policy())
	if len(bytes.TrimSpace(body)) > 0 {
		v, err := decodeJSON(body)
		if err != nil {
			fail(http.StatusBadRequest, "invalid_request_error", "request body must be one JSON value: the router scans it for secrets before it leaves serve")
			return
		}
		v, findings, err := redactValue(v, session)
		c.findings = findings
		var blocked *redact.BlockedError
		if errors.As(err, &blocked) {
			fail(http.StatusBadRequest, "invalid_request_error", blocked.Error()+": change the mode in Settings → Redaction to send it")
			return
		}
		if body, err = encodeJSON(v); err != nil {
			fail(http.StatusInternalServerError, "api_error", "internal error")
			return
		}
	}

	target := p.BaseURL() + "/" + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	up, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		fail(http.StatusInternalServerError, "api_error", "internal error")
		return
	}
	for _, h := range requestHeaders {
		if v := r.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	up.Header.Set("User-Agent", "devcules-router")
	p.Auth(up.Header, string(key))

	res, err := rt.o.Client.Do(up)
	if err != nil {
		if r.Context().Err() != nil {
			c.status = 499
			return
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			fail(http.StatusGatewayTimeout, "api_error", fmt.Sprintf("provider %q did not answer in time", id))
			return
		}
		fail(http.StatusBadGateway, "api_error", fmt.Sprintf("provider %q is unreachable", id))
		return
	}
	defer res.Body.Close()
	for name, values := range res.Header {
		if forwardResponseHeader(name) {
			w.Header()[name] = values
		}
	}
	c.status = res.StatusCode
	ctype := res.Header.Get("Content-Type")

	switch {
	case strings.HasPrefix(ctype, "text/event-stream"):
		c.stream = true
		w.WriteHeader(res.StatusCode)
		if session.Issued() == 0 {
			copyFlushing(w, res.Body)
			return
		}
		_ = pipeSSE(w, res.Body, session)
	case session.Issued() > 0 && strings.Contains(ctype, "json"):
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxReplyBody+1))
		if err != nil || len(raw) > maxReplyBody {
			fail(http.StatusBadGateway, "api_error", fmt.Sprintf("provider %q sent an unreadable or oversized reply", id))
			return
		}
		if v, err := decodeJSON(raw); err == nil {
			if out, err := encodeJSON(restoreValue(v, session)); err == nil {
				raw = out
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(raw)
	default:
		w.WriteHeader(res.StatusCode)
		copyFlushing(w, res.Body)
	}
}

// copyFlushing copies a reply that needs no restore, flushing as it
// goes so a stream stays a stream.
func copyFlushing(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// writeError answers in the caller's API style, so its SDK shows the
// message: Anthropic's envelope on v1/messages, OpenAI's elsewhere.
func writeError(w http.ResponseWriter, anthropic bool, status int, kind, msg string) {
	var v any
	if anthropic {
		v = map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": msg}}
	} else {
		v = map[string]any{"error": map[string]any{"message": msg, "type": kind, "code": nil}}
	}
	out, _ := encodeJSON(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(out, '\n'))
}
