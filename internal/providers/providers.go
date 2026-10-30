package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/terminals"
)

// Profile is one saved provider.
type Profile = store.ProviderProfile

// Formats a profile speaks.
const (
	FormatOpenAI    = "openai"
	FormatAnthropic = "anthropic"
)

// Which OpenAI API agents use on a profile.
const (
	APIAuto      = "auto"
	APIChat      = "chat"
	APIResponses = "responses"
)

// seededKey marks, in the meta repo, that the built-in profiles were
// written once: a built-in the owner removed stays removed.
const seededKey = "providers.seeded"

// ErrInvalid wraps a profile the owner must fix; its text says how.
var ErrInvalid = errors.New("providers: invalid profile")

// ErrDefault refuses removing the default profile.
var ErrDefault = errors.New("providers: the default provider cannot be removed: make another one the default first")

// ErrLast refuses removing the only profile.
var ErrLast = errors.New("providers: the only provider cannot be removed")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Service reads and writes profiles and is the router's Catalog. It
// keeps the list in memory and reloads it after every change it makes.
type Service struct {
	repo store.ProvidersRepo
	meta store.MetaRepo
	now  func() time.Time

	mu   sync.Mutex
	list []Profile // nil: load on next use
}

// New returns the service over a store's providers and meta repos.
func New(repo store.ProvidersRepo, meta store.MetaRepo) *Service {
	return &Service{repo: repo, meta: meta, now: time.Now}
}

// Seed writes the built-in profiles the first time serve starts with
// this table: each missing one is created, and none is written again
// after that. Keys saved under the same ids keep working.
func (s *Service) Seed(ctx context.Context, builtins []Profile) error {
	if _, err := s.meta.Get(ctx, seededKey); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	for _, p := range builtins {
		p.CreatedAt = s.now().UTC()
		if err := s.repo.Create(ctx, p); err != nil && !errors.Is(err, store.ErrExists) {
			return err
		}
	}
	s.forget()
	return s.meta.Set(ctx, seededKey, "1")
}

// Pin sets a built-in profile's base URL from serve's environment on
// every start (DEVCULES_META_BASE_URL and its kin): an operator's
// explicit setting beats an edit in Settings. A removed profile stays
// removed. It reports whether anything changed.
func (s *Service) Pin(ctx context.Context, id, base string) (bool, error) {
	p, err := s.repo.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil || p.BaseURL == base {
		return false, err
	}
	p.BaseURL = base
	defer s.forget()
	return true, s.repo.Update(ctx, p)
}

func (s *Service) forget() {
	s.mu.Lock()
	s.list = nil
	s.mu.Unlock()
}

// List returns every profile, sorted by name.
func (s *Service) List(ctx context.Context) ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.list == nil {
		list, err := s.repo.List(ctx)
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []Profile{}
		}
		s.list = list
	}
	return slices.Clone(s.list), nil
}

// Get returns one profile; a missing id wraps store.ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Profile, error) {
	list, err := s.List(ctx)
	if err != nil {
		return Profile{}, err
	}
	if i := slices.IndexFunc(list, func(p Profile) bool { return p.ID == id }); i >= 0 {
		return list[i], nil
	}
	return Profile{}, fmt.Errorf("providers: %q: %w", id, store.ErrNotFound)
}

// Default returns the default profile, if one is marked.
func (s *Service) Default(ctx context.Context) (Profile, bool) {
	list, _ := s.List(ctx)
	if i := slices.IndexFunc(list, func(p Profile) bool { return p.Default }); i >= 0 {
		return list[i], true
	}
	return Profile{}, false
}

// Add validates and saves a new profile. An empty id comes from the
// name; a taken one gets a number. The first profile is the default.
func (s *Service) Add(ctx context.Context, p Profile) (Profile, error) {
	if p.ID == "" {
		p.ID = Slug(p.Name)
	}
	p, err := Check(p)
	if err != nil {
		return Profile{}, err
	}
	list, err := s.List(ctx)
	if err != nil {
		return Profile{}, err
	}
	base := p.ID
	for n := 2; slices.ContainsFunc(list, func(o Profile) bool { return o.ID == p.ID }); n++ {
		suffix := fmt.Sprint(n)
		p.ID = base[:min(len(base), 32-len(suffix))] + suffix
	}
	if len(list) == 0 {
		p.Default = true
	}
	p.CreatedAt = s.now().UTC()
	defer s.forget()
	if err := s.repo.Create(ctx, p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Edit validates and saves new fields for an existing profile; its id,
// default flag and creation time stay.
func (s *Service) Edit(ctx context.Context, p Profile) (Profile, error) {
	old, err := s.Get(ctx, p.ID)
	if err != nil {
		return Profile{}, err
	}
	if p, err = Check(p); err != nil {
		return Profile{}, err
	}
	p.Default, p.CreatedAt = old.Default, old.CreatedAt
	defer s.forget()
	if err := s.repo.Update(ctx, p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// SetDefault makes id the default profile.
func (s *Service) SetDefault(ctx context.Context, id string) error {
	defer s.forget()
	return s.repo.SetDefault(ctx, id)
}

// Remove deletes a profile that is neither the default nor the last.
func (s *Service) Remove(ctx context.Context, id string) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	list, _ := s.List(ctx)
	switch {
	case len(list) == 1:
		return ErrLast
	case p.Default:
		return ErrDefault
	}
	defer s.forget()
	return s.repo.Delete(ctx, id)
}

// Lookup is the router's per-call resolution.
func (s *Service) Lookup(ctx context.Context, id string) (router.Provider, bool) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, false
	}
	return Endpoint(p), true
}

// IDs lists every profile id, sorted.
func (s *Service) IDs(ctx context.Context) []string {
	list, _ := s.List(ctx)
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.ID)
	}
	slices.Sort(out)
	return out
}

// Speaks reports whether an agent CLI that speaks wire can use p:
// Anthropic Messages on an Anthropic profile or one that also serves
// it, the Responses API on an OpenAI profile not set to chat only.
func Speaks(p Profile, wire string) bool {
	switch wire {
	case terminals.WireMessages:
		return p.Format == FormatAnthropic || p.AlsoAnthropic
	case terminals.WireResponses:
		return p.Format == FormatOpenAI && p.API != APIChat
	}
	return false
}

// endpoint is a profile as the router reaches it.
type endpoint struct {
	id, base, format string
}

// Endpoint returns the router provider for p, saved or not.
func Endpoint(p Profile) router.Provider {
	return endpoint{id: p.ID, base: Origin(p.BaseURL), format: p.Format}
}

func (e endpoint) ID() string      { return e.id }
func (e endpoint) BaseURL() string { return e.base }

// Auth: an Anthropic endpoint takes the key as x-api-key and needs a
// version (the client's own is kept); an OpenAI one takes a bearer
// token, which Meta also accepts on v1/messages.
func (e endpoint) Auth(h http.Header, key string) {
	if e.format == FormatAnthropic {
		h.Set("x-api-key", key)
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
		return
	}
	h.Set("Authorization", "Bearer "+key)
}

// Origin is the base the router appends v1/... to: the saved URL
// without a trailing /v1.
func Origin(base string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
}

// Check validates and tidies a profile's fields (not its id's
// uniqueness): every error wraps ErrInvalid with what to fix.
func Check(p Profile) (Profile, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Model = strings.TrimSpace(p.Model)
	switch {
	case !terminals.ValidAgentID(p.ID):
		return Profile{}, invalid("id: 1-32 lowercase letters and digits, starting with a letter")
	case p.Name == "" || len(p.Name) > 64 || strings.ContainsFunc(p.Name, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return Profile{}, invalid("name: 1-64 characters, no control characters")
	case p.Format != FormatOpenAI && p.Format != FormatAnthropic:
		return Profile{}, invalid(`format: "openai" or "anthropic"`)
	case p.Model != "" && !terminals.ValidModel(p.Model):
		return Profile{}, invalid("model: a model id such as gpt-5.1 or claude-sonnet-4-5 (letters, digits and . _ : / -)")
	}
	switch p.API {
	case "":
		p.API = APIAuto
	case APIAuto, APIChat, APIResponses:
	default:
		return Profile{}, invalid(`api: "auto", "chat" or "responses"`)
	}
	if p.Format == FormatAnthropic {
		p.API, p.AlsoAnthropic = APIAuto, false
	}
	base, err := CheckBaseURL(p.BaseURL)
	if err != nil {
		return Profile{}, err
	}
	p.BaseURL = base
	return p, nil
}

// CheckBaseURL applies the base URL rules and returns the URL trimmed
// of spaces and trailing slashes: https, or http only for localhost
// and loopback or private LAN addresses; no userinfo, query or
// fragment; never an unspecified, multicast or link-local address
// (169.254.169.254 is a cloud metadata service).
func CheckBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" || len(raw) > 2048 {
		return "", invalid("base URL: paste the provider's API address, like https://api.example.com/v1")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", invalid("base URL: an https:// address, like https://api.example.com/v1")
	}
	if u.User != nil {
		return "", invalid("base URL: no user or password in the address: the key goes in the API key field")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return "", invalid("base URL: no ? query or # fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return "", invalid("base URL: %s is not a provider address (link-local and metadata addresses are refused)", host)
	}
	if u.Scheme == "http" {
		local := host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && (ip.IsLoopback() || ip.IsPrivate()))
		if !local {
			return "", invalid("base URL: use https; plain http is only for localhost or a private LAN address (a local vLLM)")
		}
	}
	return raw, nil
}

// Slug makes a profile id from a name: its letters and digits,
// lowercased, starting with a letter, at most 32 long.
func Slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9' && b.Len() > 0:
			b.WriteRune(r)
		}
		if b.Len() == 32 {
			break
		}
	}
	if b.Len() == 0 {
		return "provider"
	}
	return b.String()
}
