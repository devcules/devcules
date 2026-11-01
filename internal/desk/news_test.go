package desk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNews_TavilyRequestAndParse(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"query":"q","results":[{"title":"Big breach","url":"https://www.bleepingcomputer.com/news/x/","content":"  Lots   of words here ","score":0.91,"published_date":"Fri, 31 Oct 2026 08:00:00 GMT"}],"response_time":0.4}`))
	}))
	defer srv.Close()
	TavilyURL = srv.URL
	t.Cleanup(func() { TavilyURL = "https://api.tavily.com/search" })

	items, err := News(context.Background(), srv.Client(), "tvly-abc", "data breach", 8)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tvly-abc" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["topic"] != "news" || gotBody["time_range"] != "day" || gotBody["search_depth"] != "basic" || gotBody["max_results"].(float64) != 8 || gotBody["query"] != "data breach" {
		t.Fatalf("request body = %v", gotBody)
	}
	if len(items) != 1 || items[0].Title != "Big breach" || items[0].Source != "bleepingcomputer.com" || items[0].Summary != "Lots of words here" || items[0].Score != 0.91 {
		t.Fatalf("items = %+v", items)
	}
}

func TestNews_NoKeyAndHTTPError(t *testing.T) {
	if _, err := News(context.Background(), http.DefaultClient, " ", "q", 5); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: err = %v; want ErrNoKey", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"detail":"Unauthorized"}`))
	}))
	defer srv.Close()
	TavilyURL = srv.URL
	t.Cleanup(func() { TavilyURL = "https://api.tavily.com/search" })
	_, err := News(context.Background(), srv.Client(), "bad", "q", 5)
	if err == nil || errors.Is(err, ErrNoKey) {
		t.Fatalf("401 must be a plain error, got %v", err)
	}
}

func TestClipAndHost(t *testing.T) {
	if got := clip("one two three four five six", 12); got != "one two…" {
		t.Fatalf("clip = %q", got)
	}
	if got := clip("## Following a breach, **Drupal** says - fix it `now`", 200); got != "Following a breach, Drupal says fix it now" {
		t.Fatalf("markdown not stripped: %q", got)
	}
	if got := hostOf("http://www.example.org/a/b"); got != "example.org" {
		t.Fatalf("hostOf = %q", got)
	}
}
