package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/providers"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/terminals"
)

// KeyWriter stores and removes provider keys: the vault. The router
// reads a key on every call, so a change applies without a restart.
type KeyWriter interface {
	Put(ctx context.Context, name string, value []byte) error
	Delete(ctx context.Context, name string) error
}

// profileRow is a provider profile as the browser sees it: every
// field but the key, and whether one is saved.
type profileRow struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Format        string `json:"format,omitempty"`
	AlsoAnthropic bool   `json:"alsoAnthropic,omitempty"`
	BaseURL       string `json:"baseUrl,omitempty"`
	API           string `json:"api,omitempty"`
	Model         string `json:"model,omitempty"`
	Default       bool   `json:"default,omitempty"`
	Ready         bool   `json:"ready"`
}

func rowOf(ctx context.Context, rt *router.Router, p providers.Profile) profileRow {
	ok, _ := rt.Ready(ctx, p.ID)
	return profileRow{ID: p.ID, Name: p.Name, Format: p.Format, AlsoAnthropic: p.AlsoAnthropic, BaseURL: p.BaseURL,
		API: p.API, Model: p.Model, Default: p.Default, Ready: ok}
}

// profileBody is what POST and PUT take. Key is write-only: blank on
// an edit keeps the saved one.
type profileBody struct {
	Name          string `json:"name"`
	Format        string `json:"format"`
	AlsoAnthropic bool   `json:"alsoAnthropic"`
	BaseURL       string `json:"baseUrl"`
	API           string `json:"api"`
	Model         string `json:"model"`
	Default       bool   `json:"default"`
	Key           string `json:"key"`
}

func (b profileBody) profile(id string) providers.Profile {
	return providers.Profile{ID: id, Name: b.Name, Format: b.Format, AlsoAnthropic: b.AlsoAnthropic, BaseURL: b.BaseURL, API: b.API, Model: b.Model, Default: b.Default}
}

// providersRoot serves /api/providers: GET lists every profile (no
// keys, only whether one is saved), POST adds one (session plus CSRF).
// Without a profile store it lists the router's fixed providers.
func providersRoot(rt *router.Router, profiles *providers.Service, keys KeyWriter, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
			return
		}
		switch r.Method {
		case http.MethodGet:
			out := []profileRow{}
			if profiles == nil {
				for _, id := range rt.Providers(r.Context()) {
					ok, _ := rt.Ready(r.Context(), id)
					out = append(out, profileRow{ID: id, Ready: ok})
				}
			} else {
				list, err := profiles.List(r.Context())
				if err != nil {
					writeAuthError(w, http.StatusInternalServerError, "internal error")
					return
				}
				for _, p := range list {
					out = append(out, rowOf(r.Context(), rt, p))
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"providers": out})
		case http.MethodPost:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				addProfile(w, r, rt, profiles, keys, rec)
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// readProfile decodes a profile body and checks its key, if any.
func readProfile(w http.ResponseWriter, r *http.Request) (profileBody, bool) {
	var b profileBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
		writeAuthError(w, http.StatusBadRequest, `body must be {"name", "format", "baseUrl", "api", "model", "alsoAnthropic", "default", "key"}`)
		return b, false
	}
	b.Key = strings.TrimSpace(b.Key)
	if b.Key != "" && !validKey(b.Key) {
		writeAuthError(w, http.StatusBadRequest, "key: paste the whole API key (8-4096 characters, no spaces)")
		return b, false
	}
	return b, true
}

// profileError answers a refused profile change.
func profileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providers.ErrInvalid):
		writeAuthError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), providers.ErrInvalid.Error()+": "))
	case errors.Is(err, store.ErrNotFound):
		writeAuthError(w, http.StatusNotFound, "unknown provider")
	case errors.Is(err, store.ErrExists):
		writeAuthError(w, http.StatusConflict, "a provider with that id exists")
	case errors.Is(err, providers.ErrDefault), errors.Is(err, providers.ErrLast):
		writeAuthError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "providers: "))
	default:
		writeAuthError(w, http.StatusInternalServerError, "internal error")
	}
}

func profileDetail(p providers.Profile, key string) map[string]string {
	d := map[string]string{"name": p.Name, "format": p.Format, "base_url": p.BaseURL, "api": p.API, "key": key}
	if p.Model != "" {
		d["model"] = p.Model
	}
	return d
}

// addProfile saves a new profile and, when sent, its key.
func addProfile(w http.ResponseWriter, r *http.Request, rt *router.Router, profiles *providers.Service, keys KeyWriter, rec auditor) {
	if profiles == nil || keys == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "provider profiles not configured")
		return
	}
	b, ok := readProfile(w, r)
	if !ok {
		return
	}
	p, err := profiles.Add(r.Context(), b.profile(""))
	if err != nil {
		profileError(w, err)
		return
	}
	keyNote := "none"
	if b.Key != "" {
		if err := keys.Put(r.Context(), router.KeySecret(p.ID), []byte(b.Key)); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "the provider was saved, but its key could not be stored: set it again")
			return
		}
		keyNote = "set"
	}
	rt.ForgetModels(p.ID)
	rec.record(r, audit.ProviderAdd, p.ID, profileDetail(p, keyNote))
	writeJSON(w, http.StatusCreated, map[string]any{"provider": rowOf(r.Context(), rt, p)})
}

// providerOne serves /api/providers/{id}[/...] and /api/providers/probe:
//   - PUT {id} edits a profile, DELETE {id} removes it with its key;
//   - PUT {id}/default makes it the default;
//   - GET {id}/models lists its models (serve reads them with the
//     vault key and reuses the list for five minutes);
//   - PUT or DELETE {id}/key sets or removes its key;
//   - POST probe lists the models of an unsaved form.
//
// Every write takes the session plus CSRF and is audited. A key is
// never sent back, not even in part.
func providerOne(rt *router.Router, profiles *providers.Service, keys KeyWriter, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
			return
		}
		id, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/")
		if id == "probe" && rest == "" {
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probeProfile(w, r, rt, profiles)
			})).ServeHTTP(w, r)
			return
		}
		if !terminals.ValidAgentID(id) || (rest != "" && rest != "models" && rest != "key" && rest != "default") {
			writeAuthError(w, http.StatusNotFound, "unknown provider route")
			return
		}
		switch rest {
		case "models":
			if !requireMethod(w, r, http.MethodGet) {
				return
			}
			providerModels(w, r, rt, id)
			return
		case "key":
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerKey(w, r, rt, keys, id, rec)
			})).ServeHTTP(w, r)
			return
		}
		csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if profiles == nil || keys == nil {
				writeAuthError(w, http.StatusServiceUnavailable, "provider profiles not configured")
				return
			}
			switch {
			case rest == "default" && r.Method == http.MethodPut:
				if err := profiles.SetDefault(r.Context(), id); err != nil {
					profileError(w, err)
					return
				}
				rec.record(r, audit.ProviderDefault, id, nil)
				w.WriteHeader(http.StatusNoContent)
			case rest == "" && r.Method == http.MethodPut:
				editProfile(w, r, rt, profiles, keys, id, rec)
			case rest == "" && r.Method == http.MethodDelete:
				if err := profiles.Remove(r.Context(), id); err != nil {
					profileError(w, err)
					return
				}
				// The key goes with it: a later profile reusing the id
				// must not inherit it.
				_ = keys.Delete(r.Context(), router.KeySecret(id))
				rt.ForgetModels(id)
				rec.record(r, audit.ProviderRemove, id, nil)
				w.WriteHeader(http.StatusNoContent)
			default:
				w.Header().Set("Allow", map[bool]string{true: "PUT", false: "PUT, DELETE"}[rest == "default"])
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})).ServeHTTP(w, r)
	})
}

// editProfile saves new fields and, when sent, a new key. A saved key
// is only ever sent to the address it was saved for: moving the base
// URL to another host needs the key pasted again.
func editProfile(w http.ResponseWriter, r *http.Request, rt *router.Router, profiles *providers.Service, keys KeyWriter, id string, rec auditor) {
	b, ok := readProfile(w, r)
	if !ok {
		return
	}
	old, err := profiles.Get(r.Context(), id)
	if err != nil {
		profileError(w, err)
		return
	}
	saved, _ := rt.Ready(r.Context(), id)
	if base, err := providers.CheckBaseURL(b.BaseURL); err == nil && b.Key == "" && saved && hostOf(base) != hostOf(old.BaseURL) {
		writeAuthError(w, http.StatusBadRequest, "key: the base URL moved to another host, so paste the API key again (a saved key is only sent where it was saved for)")
		return
	}
	p, err := profiles.Edit(r.Context(), b.profile(id))
	if err != nil {
		profileError(w, err)
		return
	}
	keyNote := "kept"
	if b.Key != "" {
		if err := keys.Put(r.Context(), router.KeySecret(id), []byte(b.Key)); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "the provider was saved, but its key could not be stored: set it again")
			return
		}
		keyNote = "set"
	}
	if b.Default && !p.Default {
		if err := profiles.SetDefault(r.Context(), id); err == nil {
			p.Default = true
			rec.record(r, audit.ProviderDefault, id, nil)
		}
	}
	rt.ForgetModels(id)
	rec.record(r, audit.ProviderEdit, id, profileDetail(p, keyNote))
	writeJSON(w, http.StatusOK, map[string]any{"provider": rowOf(r.Context(), rt, p)})
}

// hostOf is a checked base URL's scheme and host.
func hostOf(base string) string {
	scheme, rest, _ := strings.Cut(base, "://")
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + strings.ToLower(host)
}

// probeProfile serves POST /api/providers/probe {format, baseUrl,
// key?, id?}: the models of a form not saved yet, for "load models".
// With no key it uses the saved key of profile id, and only on that
// profile's own host. The base URL rules apply, the call gets ten
// seconds, and the key is never echoed.
func probeProfile(w http.ResponseWriter, r *http.Request, rt *router.Router, profiles *providers.Service) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var b struct {
		ID      string `json:"id"`
		Format  string `json:"format"`
		BaseURL string `json:"baseUrl"`
		Key     string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
		writeAuthError(w, http.StatusBadRequest, `body must be {"format", "baseUrl", "key" or "id"}`)
		return
	}
	b.Key = strings.TrimSpace(b.Key)
	id := "probe"
	if b.ID != "" {
		id = b.ID
	}
	p, err := providers.Check(providers.Profile{ID: id, Name: "probe", Format: b.Format, BaseURL: b.BaseURL})
	if err != nil {
		profileError(w, err)
		return
	}
	switch {
	case b.Key != "" && !validKey(b.Key):
		writeAuthError(w, http.StatusBadRequest, "key: paste the whole API key (8-4096 characters, no spaces)")
		return
	case b.Key == "":
		if profiles == nil || b.ID == "" {
			writeAuthError(w, http.StatusBadRequest, "key: paste the API key to load models")
			return
		}
		old, err := profiles.Get(r.Context(), b.ID)
		if err != nil || hostOf(old.BaseURL) != hostOf(p.BaseURL) {
			writeAuthError(w, http.StatusBadRequest, "key: paste the API key to load models (a saved key is only sent where it was saved for)")
			return
		}
	}
	list, err := rt.Probe(r.Context(), providers.Endpoint(p), b.Key)
	switch {
	case errors.Is(err, router.ErrNoKey):
		writeAuthError(w, http.StatusBadRequest, "key: paste the API key to load models")
	case err != nil:
		writeAuthError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"models": list})
	}
}

// providerModels answers GET /api/providers/{id}/models.
func providerModels(w http.ResponseWriter, r *http.Request, rt *router.Router, id string) {
	list, err := rt.Models(r.Context(), id)
	switch {
	case errors.Is(err, router.ErrUnknownProvider):
		writeAuthError(w, http.StatusNotFound, "unknown provider")
	case errors.Is(err, router.ErrNoKey):
		writeAuthError(w, http.StatusConflict, "the "+id+" provider has no API key yet: add its key in Settings")
	case err != nil:
		writeAuthError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"models": list})
	}
}

// providerKey sets (PUT {"key"}) or removes (DELETE) one provider's
// key in the vault, audits which provider changed (never the key), and
// drops that provider's cached model list.
func providerKey(w http.ResponseWriter, r *http.Request, rt *router.Router, keys KeyWriter, id string, rec auditor) {
	if keys == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "vault not configured")
		return
	}
	if !rt.Serves(r.Context(), id) {
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

// offers lists the providers an agent can use now, its own first, then
// the default, then the rest by name: every profile serving the API it
// speaks (and in its Only list, if it has one). Without profiles, or
// for a runner from before profiles (no Wire), it is the agent's fixed
// list cut to what the router serves.
func offers(ctx context.Context, info terminals.AgentInfo, rt *router.Router, profiles *providers.Service) []string {
	if profiles == nil || info.Wire == "" {
		legacy := info.Providers
		if len(legacy) == 0 {
			legacy = append([]string{info.Provider}, info.Only...)
		}
		var served []string
		if rt != nil {
			served = rt.Providers(ctx)
		}
		var out []string
		for _, id := range legacy {
			if slices.Contains(served, id) && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		return out
	}
	list, _ := profiles.List(ctx)
	slices.SortStableFunc(list, func(a, b providers.Profile) int {
		rank := func(p providers.Profile) int {
			switch {
			case p.ID == info.Provider:
				return 0
			case p.Default:
				return 1
			}
			return 2
		}
		return rank(a) - rank(b)
	})
	out := []string{}
	for _, p := range list {
		if providers.Speaks(p, info.Wire) && (len(info.Only) == 0 || slices.Contains(info.Only, p.ID)) {
			out = append(out, p.ID)
		}
	}
	return out
}
