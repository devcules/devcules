package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/terminals"
)

// providersRoot serves GET /api/providers: each router provider and
// whether its API key is in the vault, so the new-terminal form can
// offer only providers that will answer.
func providersRoot(rt *router.Router) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		type row struct {
			ID    string `json:"id"`
			Ready bool   `json:"ready"`
		}
		ids := rt.Providers()
		slices.Sort(ids)
		out := make([]row, 0, len(ids))
		for _, id := range ids {
			ok, _ := rt.Ready(r.Context(), id)
			out = append(out, row{ID: id, Ready: ok})
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": out})
	})
}

// KeyWriter stores and removes provider keys: the vault. The router
// reads a key on every call, so a change applies without a restart.
type KeyWriter interface {
	Put(ctx context.Context, name string, value []byte) error
	Delete(ctx context.Context, name string) error
}

// providerOne serves /api/providers/{id}/...: GET models (the ids the
// provider lists, read by serve with the vault key and reused for five
// minutes), and PUT or DELETE key (session plus CSRF) to set or remove
// the provider's API key. A key is never sent back, not even in part.
func providerOne(rt *router.Router, keys KeyWriter, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
			return
		}
		id, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/")
		if !terminals.ValidAgentID(id) || (rest != "models" && rest != "key") {
			writeAuthError(w, http.StatusNotFound, "unknown provider route")
			return
		}
		if rest == "key" {
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerKey(w, r, rt, keys, id, rec)
			})).ServeHTTP(w, r)
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		list, err := rt.Models(r.Context(), id)
		switch {
		case errors.Is(err, router.ErrUnknownProvider):
			writeAuthError(w, http.StatusNotFound, "unknown provider")
			return
		case errors.Is(err, router.ErrNoKey):
			writeAuthError(w, http.StatusConflict, "the "+id+" provider has no API key yet: add its key in Settings")
			return
		case err != nil:
			writeAuthError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": list})
	})
}

// providerKey sets (PUT {"key"}) or removes (DELETE) one provider's
// key in the vault, audits which provider changed (never the key), and
// drops that provider's cached model list.
func providerKey(w http.ResponseWriter, r *http.Request, rt *router.Router, keys KeyWriter, id string, rec auditor) {
	if keys == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	if !rt.Serves(id) {
		writeAuthError(w, http.StatusNotFound, "unknown provider")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
			writeAuthError(w, http.StatusBadRequest, `body must be {"key": "<the provider's API key>"}`)
			return
		}
		key := strings.TrimSpace(body.Key)
		if !validKey(key) {
			writeAuthError(w, http.StatusBadRequest, "key: paste the whole API key (8-4096 characters, no spaces)")
			return
		}
		if err := keys.Put(r.Context(), router.KeySecret(id), []byte(key)); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "could not store the key")
			return
		}
		rt.ForgetModels(id)
		rec.record(r, audit.ProviderKeySet, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := keys.Delete(r.Context(), router.KeySecret(id)); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "could not remove the key")
			return
		}
		rt.ForgetModels(id)
		rec.record(r, audit.ProviderKeyRemove, id, nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// validKey accepts a pasted API key: printable, no whitespace inside,
// long enough to be a real key and short enough to be one.
func validKey(k string) bool {
	if len(k) < 8 || len(k) > 4096 {
		return false
	}
	return !strings.ContainsFunc(k, func(r rune) bool { return r <= ' ' || r == 0x7f })
}
