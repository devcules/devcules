package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TavilyURL is a var for tests.
var TavilyURL = "https://api.tavily.com/search"

// ErrNoKey tells the widget to point at Secrets instead of showing an error.
var ErrNoKey = errors.New("no Tavily key: store one in Secrets as TAVILY_KEY")

type NewsItem struct {
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Source    string  `json:"source"`
	Summary   string  `json:"summary"`
	Published string  `json:"published,omitempty"`
	Score     float64 `json:"score"`
}

// News runs one Tavily news search for the last day. basic depth = 1 credit.
func News(ctx context.Context, hc *http.Client, apiKey, query string, max int) ([]NewsItem, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrNoKey
	}
	if max <= 0 || max > 20 {
		max = 8
	}
	body, _ := json.Marshal(map[string]any{
		"query":          query,
		"topic":          "news",
		"time_range":     "day",
		"max_results":    max,
		"search_depth":   "basic",
		"include_answer": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TavilyURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("tavily: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var raw struct {
		Results []struct {
			Title     string  `json:"title"`
			URL       string  `json:"url"`
			Content   string  `json:"content"`
			Score     float64 `json:"score"`
			Published string  `json:"published_date"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("tavily: decode: %w", err)
	}
	out := make([]NewsItem, 0, len(raw.Results))
	for _, r := range raw.Results {
		out = append(out, NewsItem{
			Title: r.Title, URL: r.URL, Source: hostOf(r.URL),
			Summary: clip(r.Content, 240), Published: r.Published, Score: r.Score,
		})
	}
	return out, nil
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	u = strings.TrimPrefix(u, "www.")
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	return u
}

// clip collapses whitespace, drops the markdown Tavily's snippets carry
// (headings, emphasis, list bullets, quotes, code ticks) and cuts at a word.
func clip(s string, n int) string {
	s = strings.NewReplacer("**", "", "__", "", "`", "", "\r", " ").Replace(s)
	words := strings.Fields(s)
	kept := words[:0]
	for _, w := range words {
		t := strings.TrimLeft(w, "#*>-•")
		if t == "" {
			continue
		}
		kept = append(kept, t)
	}
	s = strings.Join(kept, " ")
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
