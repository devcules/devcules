package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// modelsTTL is how long a provider's model list is reused: the list
// changes rarely, and the picker should not cost an upstream call per
// keystroke.
const modelsTTL = 5 * time.Minute

// maxModelPages bounds how many pages of a paged model list are read.
const maxModelPages = 10

// ErrUnknownProvider answers Ready and Models for an id the router
// does not serve.
var ErrUnknownProvider = errors.New("router: unknown provider")

// ErrNoKey says the provider has no API key in the vault yet.
var ErrNoKey = errors.New("router: no API key for this provider")

type modelCache struct {
	mu   sync.Mutex
	at   map[string]time.Time
	list map[string][]string
}

// Ready reports whether provider id has a key in the vault, so a
// launch on it can work. The key itself never leaves the router.
func (rt *Router) Ready(ctx context.Context, id string) (bool, error) {
	if _, ok := rt.providers[id]; !ok {
		return false, ErrUnknownProvider
	}
	key, err := rt.o.Keys.Get(ctx, KeySecret(id))
	return err == nil && len(key) > 0, nil
}

// Models lists the model ids provider id offers, from its GET v1/models
// with the vault key, sorted. Serve makes this call, never the browser,
// and a list is reused for five minutes.
func (rt *Router) Models(ctx context.Context, id string) ([]string, error) {
	p, ok := rt.providers[id]
	if !ok {
		return nil, ErrUnknownProvider
	}
	rt.models.mu.Lock()
	if at, ok := rt.models.at[id]; ok && time.Since(at) < modelsTTL {
		list := rt.models.list[id]
		rt.models.mu.Unlock()
		return list, nil
	}
	rt.models.mu.Unlock()

	key, err := rt.o.Keys.Get(ctx, KeySecret(id))
	if err != nil || len(key) == 0 {
		return nil, ErrNoKey
	}
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	list := []string{}
	seen := map[string]bool{}
	// Anthropic pages its list (has_more, last_id, then ?after_id=);
	// OpenAI and Meta answer in one page and never set has_more.
	after := ""
	for page := 0; page < maxModelPages; page++ {
		ids, more, last, err := rt.modelPage(ctx, p, string(key), after)
		if err != nil {
			return nil, err
		}
		for _, m := range ids {
			if m != "" && !seen[m] {
				seen[m] = true
				list = append(list, m)
			}
		}
		if !more || last == "" || last == after {
			break
		}
		after = last
	}
	sort.Strings(list)
	rt.models.mu.Lock()
	if rt.models.at == nil {
		rt.models.at, rt.models.list = map[string]time.Time{}, map[string][]string{}
	}
	rt.models.at[id], rt.models.list[id] = time.Now(), list
	rt.models.mu.Unlock()
	return list, nil
}

// modelPage reads one page of provider p's GET v1/models: its ids, and
// whether more follow after last.
func (rt *Router) modelPage(ctx context.Context, p Provider, key, after string) (ids []string, more bool, last string, err error) {
	u := p.BaseURL() + "/v1/models"
	if after != "" {
		u += "?limit=1000&after_id=" + url.QueryEscape(after)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false, "", err
	}
	p.Auth(req.Header, key)
	// Anthropic requires a version on every call; the others ignore it.
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	resp, err := rt.o.Client.Do(req)
	if err != nil {
		return nil, false, "", fmt.Errorf("%s did not answer the model list", p.ID())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, "", fmt.Errorf("%s answered the model list with %d", p.ID(), resp.StatusCode)
	}
	// OpenAI, Anthropic and Meta all answer {"data": [{"id": ...}]};
	// Anthropic adds has_more and last_id.
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, false, "", fmt.Errorf("%s sent an unreadable model list", p.ID())
	}
	for _, m := range body.Data {
		ids = append(ids, m.ID)
	}
	return ids, body.HasMore, body.LastID, nil
}

// ForgetModels drops the cached model list of provider id, so the next
// Models call asks the provider again: a new key may see other models.
func (rt *Router) ForgetModels(id string) {
	rt.models.mu.Lock()
	defer rt.models.mu.Unlock()
	delete(rt.models.at, id)
	delete(rt.models.list, id)
}

// Serves reports whether id is a provider the router serves.
func (rt *Router) Serves(id string) bool {
	_, ok := rt.providers[id]
	return ok
}
