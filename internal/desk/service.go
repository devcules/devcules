package desk

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/vault"
)

// Sources is the JSON document the Sources drawer edits and the desk
// routes read. Secrets (tokens) live as values here because the whole
// document is sealed in the vault; the API never echoes them back —
// PUT replaces, GET answers with masked flags (has_token, …).
type Sources struct {
	Location string `json:"location,omitempty"`
	// ICSFeeds: [{"label":"Work","url":"https://…ics"}] — Google/Outlook
	// published calendars, no OAuth.
	ICSFeeds []ICSFeed `json:"ics_feeds,omitempty"`
	NewsSecurity string `json:"news_security_query,omitempty"`
	NewsTech     string `json:"news_tech_query,omitempty"`
	NewsCustom   string `json:"news_custom_query,omitempty"`
	TavilyKey    string `json:"tavily_key,omitempty"`
	// Tickets: pick a provider. Jira uses Email+Token+Site+JQL; Asana
	// Token+Project(workspace gid); Linear Token(+Project team filter).
	TicketProvider string `json:"ticket_provider,omitempty"`
	TicketSite     string `json:"ticket_site,omitempty"`
	TicketEmail    string `json:"ticket_email,omitempty"`
	TicketToken    string `json:"ticket_token,omitempty"`
	TicketJQL      string `json:"ticket_jql,omitempty"`
	TicketProject  string `json:"ticket_project,omitempty"`
	// Masked mirrors, never input: GET answers set only these. The Clear
	// flags are the one way the drawer removes a stored token — a blank
	// value keeps it (so a PUT never round-trips a secret), but
	// X_clear:true wipes it.
	HasTavily     bool `json:"has_tavily,omitempty"`
	HasTicketTok  bool `json:"has_ticket_token,omitempty"`
	ClearTavily   bool `json:"tavily_clear,omitempty"`
	ClearTicket   bool `json:"ticket_clear,omitempty"`
}

type ICSFeed struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Prefs is the desk layout document: widget list and per-breakpoint
// layouts, exactly what the webapp's deskPrefs.js module reads/writes.
type Prefs struct {
	Version int                      `json:"version"`
	Widgets []json.RawMessage        `json:"widgets"`
	Layouts map[string]json.RawMessage `json:"layouts"`
	Seen    map[string]string        `json:"seen,omitempty"`
}

const (
	sourcesKey = "desk/sources"
	prefsKey   = "desk/prefs"
)

// Service answers the Desk routes. Read paths cache per-source so the
// 30s tile refresh never hammers the provider; a stale cached answer is
// served with stale:true rather than an error when the provider is down.
type Service struct {
	vault *vault.Vault
	hc    *http.Client
	feedHC *http.Client

	mu       sync.Mutex
	sources  *Sources // memo after first vault read
	prefs    *Prefs
	weatherC *Cache[json.RawMessage]
	newsC    *Cache[json.RawMessage]
	ticketC  *Cache[json.RawMessage]
	meetC    *Cache[json.RawMessage]
}

func NewService(v *vault.Vault, hc, feedHC *http.Client) *Service {
	return &Service{
		vault: v, hc: hc, feedHC: feedHC,
		weatherC: NewCache[json.RawMessage](30*time.Minute, 5*time.Minute),
		newsC:    NewCache[json.RawMessage](6*time.Hour, 20*time.Minute),
		ticketC:  NewCache[json.RawMessage](10*time.Minute, 2*time.Minute),
		meetC:    NewCache[json.RawMessage](10*time.Minute, 2*time.Minute),
	}
}

// NewHardenedClient returns the SSRF-guarded client (re-exported for the
// serve wiring, which holds no other reason to import net).
func NewHardenedClient(timeout time.Duration) *http.Client { return NewClient(timeout) }

func (s *Service) readSources(ctx context.Context) (Sources, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sources != nil {
		return *s.sources, nil
	}
	raw, err := s.vault.Get(ctx, sourcesKey)
	var out Sources
	if errors.Is(err, vault.ErrNotFound) {
		s.sources = &out
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("desk sources unreadable: %w", err)
	}
	s.sources = &out
	return out, nil
}

// ReadSources exposes the full document to the API layer (which masks
// it before the browser); readSources is the internal cached variant.
func (s *Service) ReadSources(ctx context.Context) (Sources, error) { return s.readSources(ctx) }

// Masked answers the document the browser may see: every secret field
// dropped to a presence flag, never the value. A whitespace-only token
// counts as absent — it can never authorise a call, so it never claims to.
func (s Sources) Masked() Sources {
	out := s
	out.HasTavily = strings.TrimSpace(out.TavilyKey) != ""
	out.HasTicketTok = strings.TrimSpace(out.TicketToken) != ""
	out.TavilyKey = ""
	out.TicketToken = ""
	return out
}

// WriteSources replaces the whole document (the drawer always sends a
// full form) and busts the memo and every cache.
func (s *Service) WriteSources(ctx context.Context, src Sources) error {
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	if err := s.vault.Put(ctx, sourcesKey, raw); err != nil {
		return err
	}
	s.mu.Lock()
	s.sources = &src
	s.prefs = nil
	s.mu.Unlock()
	s.weatherC.Forget("w")
	s.newsC.Forget("security")
	s.newsC.Forget("tech")
	s.newsC.Forget("custom")
	s.ticketC.Forget("t")
	s.meetC.Forget("m")
	return nil
}

func (s *Service) ReadPrefs(ctx context.Context) (Prefs, error) {
	s.mu.Lock()
	if s.prefs != nil {
		p := *s.prefs
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()
	raw, err := s.vault.Get(ctx, prefsKey)
	if errors.Is(err, vault.ErrNotFound) {
		return Prefs{Version: 2}, nil
	}
	if err != nil {
		return Prefs{}, err
	}
	var p Prefs
	if err := json.Unmarshal(raw, &p); err != nil {
		return Prefs{}, fmt.Errorf("desk prefs unreadable: %w", err)
	}
	if p.Version == 0 {
		p.Version = 2
	}
	s.mu.Lock()
	s.prefs = &p
	s.mu.Unlock()
	return p, nil
}

func (s *Service) WritePrefs(ctx context.Context, p Prefs) error {
	p.Version = 2
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := s.vault.Put(ctx, prefsKey, raw); err != nil {
		return err
	}
	s.mu.Lock()
	s.prefs = &p
	s.mu.Unlock()
	return nil
}

// Weather answers the Now tile for the stored location.
func (s *Service) Weather(ctx context.Context, refresh bool) (json.RawMessage, error) {
	src, err := s.readSources(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(src.Location) == "" {
		return json.RawMessage(`{"configured":false}`), nil
	}
	entry := s.weatherC.Get(ctx, "w", refresh, func(ctx context.Context) (json.RawMessage, error) {
		place, err := Geocode(ctx, s.hc, src.Location)
		if err != nil {
			return nil, err
		}
		w, err := Forecast(ctx, s.hc, place)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"configured": true, "location": place.Name, "weather": w})
	})
	if entry.Value == nil && entry.Error != "" {
		return nil, errors.New(entry.Error)
	}
	return withStale(entry.Value, entry.Stale), nil
}

// News answers one feed: security, tech or custom. Tavily when a key is
// stored; otherwise the configured query falls back to public RSS
// (Google News search) so the tile is never dead without a key.
func (s *Service) News(ctx context.Context, which string, refresh bool) (json.RawMessage, error) {
	src, err := s.readSources(ctx)
	if err != nil {
		return nil, err
	}
	var query string
	switch which {
	case "security":
		query = src.NewsSecurity
	case "tech":
		query = src.NewsTech
	case "custom":
		query = src.NewsCustom
	default:
		return nil, fmt.Errorf("unknown news feed %q", which)
	}
	entry := s.newsC.Get(ctx, which, refresh, func(ctx context.Context) (json.RawMessage, error) {
		var items []NewsItem
		var err error
		usedTavily := false
		if key := strings.TrimSpace(src.TavilyKey); key != "" {
			usedTavily = true
			items, err = News(ctx, s.hc, key, query, 10)
			if err != nil {
				// A stored key that no longer works (expired, typo) should
				// not dead-end the tile: fall back to public search.
				items, err = RSSNews(ctx, s.hc, query, 10)
				usedTavily = false
			}
		} else {
			items, err = RSSNews(ctx, s.hc, query, 10)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"configured": query != "", "items": items, "tavily": usedTavily})
	})
	if entry.Value == nil && entry.Error != "" {
		return nil, errors.New(entry.Error)
	}
	return withStale(entry.Value, entry.Stale), nil
}

// Tickets answers the (single) tickets tile from the configured source.
func (s *Service) Tickets(ctx context.Context, refresh bool) (json.RawMessage, error) {
	src, err := s.readSources(ctx)
	if err != nil {
		return nil, err
	}
	if src.TicketProvider == "" {
		return json.RawMessage(`{"configured":false,"items":[]}`), nil
	}
	entry := s.ticketC.Get(ctx, "t", refresh, func(ctx context.Context) (json.RawMessage, error) {
		items, err := Tickets(ctx, s.hc, TicketQuery{
			Provider: src.TicketProvider, Site: src.TicketSite, Email: src.TicketEmail,
			Token: src.TicketToken, JQL: src.TicketJQL, Project: src.TicketProject,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"configured": true, "provider": src.TicketProvider, "items": items})
	})
	if entry.Value == nil && entry.Error != "" {
		return nil, errors.New(entry.Error)
	}
	return withStale(entry.Value, entry.Stale), nil
}

// Meetings merges every stored ICS feed over the next 7 days, each
// meeting stamped with its feed label so the tile can say "Work".  The
// calendar grid reads FeedMeetings over a wider window instead: this
// narrow list is the "next up" telescope, the grid wants the horizon.
func (s *Service) Meetings(ctx context.Context, refresh bool) (json.RawMessage, error) {
	from := time.Now().Add(-2 * time.Hour)
	to := time.Now().Add(7 * 24 * time.Hour)
	return s.FeedMeetings(ctx, from, to, refresh, 25)
}

// FeedMeetings answers the ICS feeds expanded over [from, to], sorted,
// capped at limit (0 = no cap). Read-only sources; a feed that fails is
// skipped unless every feed failed.
func (s *Service) FeedMeetings(ctx context.Context, from, to time.Time, refresh bool, limit int) (json.RawMessage, error) {
	src, err := s.readSources(ctx)
	if err != nil {
		return nil, err
	}
	if len(src.ICSFeeds) == 0 {
		return json.RawMessage(`{"configured":false,"items":[]}`), nil
	}
	// The cache key carries the window, so a week scroll and a month
	// scroll don't trample each other.
	key := fmt.Sprintf("m/%d/%d", from.Unix(), to.Unix())
	entry := s.meetC.Get(ctx, key, refresh, func(ctx context.Context) (json.RawMessage, error) {
		var all []Meeting
		var firstErr error
		for _, f := range src.ICSFeeds {
			ms, err := FetchMeetings(ctx, s.feedHC, f.URL, from, to)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, m := range ms {
				m.Feed = f.Label
				all = append(all, m)
			}
		}
		if len(all) == 0 && firstErr != nil {
			return nil, firstErr
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Start.Before(all[j].Start) })
		if limit > 0 && len(all) > limit {
			all = all[:limit]
		}
		return json.Marshal(map[string]any{"configured": true, "items": all})
	})
	if entry.Value == nil && entry.Error != "" {
		return nil, errors.New(entry.Error)
	}
	return withStale(entry.Value, entry.Stale), nil
}

// withStale marks a cached answer when the underlying fetch failed and
// this is an older value: the tile's LED turns "stale", not "error".
func withStale(v json.RawMessage, stale bool) json.RawMessage {
	if v == nil {
		return nil
	}
	if !stale {
		return v
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(v, &m); err != nil {
		return v
	}
	m["stale"] = json.RawMessage("true")
	out, err := json.Marshal(m)
	if err != nil {
		return v
	}
	return out
}

// ---- RSS fallback (no key): Google News search, parsed as RSS. ----

type rssDoc struct {
	Channel struct {
		Items []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			Source  string `xml:"source"`
			PubDate string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

// RSSNews runs a free Google News search RSS for the query and shapes
// it like the Tavily answer, so a key-less tile still shows headlines.
func RSSNews(ctx context.Context, hc *http.Client, query string, max int) ([]NewsItem, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		q = "technology"
	}
	u := "https://news.google.com/rss/search?q=" + urlq(q) + "&hl=en-US&gl=US&ceid=US:en"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rss: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rss: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var doc rssDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("rss: decode: %w", err)
	}
	out := make([]NewsItem, 0, max)
	for _, it := range doc.Channel.Items {
		if len(out) >= max {
			break
		}
		pub, _ := time.Parse(time.RFC1123Z, it.PubDate)
		out = append(out, NewsItem{Title: it.Title, URL: it.Link, Source: it.Source, Published: pub.Format(time.RFC3339)})
	}
	return out, nil
}

func urlq(s string) string {
	r := strings.NewReplacer(" ", "+")
	return r.Replace(s)
}
