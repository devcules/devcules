package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// modelsTTL is how long a provider's model list is reused: the list
// changes rarely, and the picker should not cost an upstream call per
// keystroke.
const modelsTTL = 5 * time.Minute

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL()+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	p.Auth(req.Header, string(key))
	// Anthropic requires a version on every call; the others ignore it.
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	resp, err := rt.o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s did not answer the model list", id)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered the model list with %d", id, resp.StatusCode)
	}
	// OpenAI, Anthropic and Meta all answer {"data": [{"id": ...}]}.
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%s sent an unreadable model list", id)
	}
	list := make([]string, 0, len(body.Data))
	seen := map[string]bool{}
	for _, m := range body.Data {
		if m.ID != "" && !seen[m.ID] {
			seen[m.ID] = true
			list = append(list, m.ID)
		}
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
