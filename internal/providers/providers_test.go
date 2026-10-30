package providers

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/terminals"
)

func newService(t *testing.T) (*Service, store.Store) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s.Providers(), s.Meta()), s
}

var builtins = []Profile{
	{ID: "meta", Name: "Meta", Format: FormatOpenAI, AlsoAnthropic: true, BaseURL: "https://api.meta.ai", Default: true},
	{ID: "anthropic", Name: "Anthropic", Format: FormatAnthropic, BaseURL: "https://api.anthropic.com"},
}

func TestCheckBaseURL(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		" https://api.studio.nebius.com/v1/ ": "https://api.studio.nebius.com/v1",
		"http://localhost:8000/v1":            "http://localhost:8000/v1",
		"http://vllm.localhost:8000":          "http://vllm.localhost:8000",
		"http://127.0.0.1:8000/v1":            "http://127.0.0.1:8000/v1",
		"http://192.168.1.20:8000/v1":         "http://192.168.1.20:8000/v1",
		"http://10.0.0.5":                     "http://10.0.0.5",
		"http://[::1]:8000":                   "http://[::1]:8000",
		"https://10.0.0.5/v1":                 "https://10.0.0.5/v1",
	} {
		if got, err := CheckBaseURL(raw); err != nil || got != want {
			t.Errorf("CheckBaseURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for raw, why := range map[string]string{
		"":                                        "paste",
		"api.example.com/v1":                      "https://",
		"ftp://api.example.com":                   "https://",
		"http://api.example.com/v1":               "use https",
		"http://8.8.8.8/v1":                       "use https",
		"https://user:pw@api.example.com":         "no user",
		"https://api.example.com/v1?key=x":        "no ? query",
		"https://api.example.com/v1#x":            "no ? query",
		"https://api.example.com/v1?":             "no ? query",
		"http://169.254.169.254/latest/meta-data": "link-local",
		"https://169.254.169.254":                 "link-local",
		"http://[fe80::1]:80":                     "link-local",
		"http://0.0.0.0:8000":                     "link-local",
		"https://224.0.0.1":                       "link-local",
		"https://" + strings.Repeat("a", 2050):    "paste",
	} {
		_, err := CheckBaseURL(raw)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), why) {
			t.Errorf("CheckBaseURL(%q) = %v, want ErrInvalid naming %q", raw, err, why)
		}
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	good := Profile{ID: "nebius", Name: " Nebius ", Format: FormatOpenAI, BaseURL: "https://api.studio.nebius.com/v1/", Model: " gpt-x "}
	p, err := Check(good)
	if err != nil || p.Name != "Nebius" || p.Model != "gpt-x" || p.API != APIAuto || p.BaseURL != "https://api.studio.nebius.com/v1" {
		t.Fatalf("Check = %+v, %v", p, err)
	}
	// Anthropic profiles have no OpenAI API choice and are not "also".
	p, _ = Check(Profile{ID: "claude", Name: "C", Format: FormatAnthropic, BaseURL: "https://a.test", API: APIChat, AlsoAnthropic: true})
	if p.API != APIAuto || p.AlsoAnthropic {
		t.Fatalf("anthropic = %+v", p)
	}
	for _, bad := range []Profile{
		{ID: "Bad-Id", Name: "x", Format: FormatOpenAI, BaseURL: "https://a.test"},
		{ID: "x", Name: "", Format: FormatOpenAI, BaseURL: "https://a.test"},
		{ID: "x", Name: "a\nb", Format: FormatOpenAI, BaseURL: "https://a.test"},
		{ID: "x", Name: "x", Format: "gemini", BaseURL: "https://a.test"},
		{ID: "x", Name: "x", Format: FormatOpenAI, BaseURL: "https://a.test", Model: "a b"},
		{ID: "x", Name: "x", Format: FormatOpenAI, BaseURL: "https://a.test", API: "batch"},
		{ID: "x", Name: "x", Format: FormatOpenAI, BaseURL: "http://a.test"},
	} {
		if _, err := Check(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Check(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestSlug(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Nebius AI Gateway":      "nebiusaigateway",
		"local vLLM":             "localvllm",
		"42 Labs":                "labs",
		"!!!":                    "provider",
		strings.Repeat("ab", 40): strings.Repeat("ab", 16),
	} {
		if got := Slug(name); got != want || !terminals.ValidAgentID(got) {
			t.Errorf("Slug(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSeedOnceAndPin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	if err := svc.Seed(ctx, builtins); err != nil {
		t.Fatal(err)
	}
	if ids := svc.IDs(ctx); strings.Join(ids, ",") != "anthropic,meta" {
		t.Fatalf("IDs = %v", ids)
	}
	if d, ok := svc.Default(ctx); !ok || d.ID != "meta" {
		t.Fatalf("default = %+v %v", d, ok)
	}
	// A built-in the owner removed is not written again.
	if err := svc.Remove(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Seed(ctx, builtins); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "anthropic"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reseeded: %v", err)
	}
	// The environment's base URL wins on every start.
	if changed, err := svc.Pin(ctx, "meta", "http://127.0.0.1:7701"); !changed || err != nil {
		t.Fatalf("Pin = %v %v", changed, err)
	}
	if p, _ := svc.Get(ctx, "meta"); p.BaseURL != "http://127.0.0.1:7701" || !p.Default {
		t.Fatalf("pinned = %+v", p)
	}
	if changed, _ := svc.Pin(ctx, "meta", "http://127.0.0.1:7701"); changed {
		t.Fatal("same URL pinned twice")
	}
	if changed, err := svc.Pin(ctx, "anthropic", "http://x"); changed || err != nil {
		t.Fatalf("Pin removed = %v %v", changed, err)
	}
}

func TestAddEditRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	first, err := svc.Add(ctx, Profile{Name: "Nebius", Format: FormatOpenAI, BaseURL: "https://api.studio.nebius.com/v1"})
	if err != nil || first.ID != "nebius" || !first.Default {
		t.Fatalf("first = %+v, %v: want the id from the name and the default", first, err)
	}
	second, err := svc.Add(ctx, Profile{Name: "Nebius", Format: FormatOpenAI, BaseURL: "https://other.test/v1", API: APIChat})
	if err != nil || second.ID != "nebius2" || second.Default {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if _, err := svc.Add(ctx, Profile{Name: "x", Format: FormatOpenAI, BaseURL: "http://evil.test"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad add = %v", err)
	}
	// The cached list follows every write.
	if p, ok := svc.Lookup(ctx, "nebius2"); !ok || p.BaseURL() != "https://other.test" {
		t.Fatalf("Lookup = %v %v", p, ok)
	}
	edited, err := svc.Edit(ctx, Profile{ID: "nebius2", Name: "Other", Format: FormatOpenAI, BaseURL: "https://other.test/v1", Model: "m1", Default: true})
	if err != nil || edited.Name != "Other" || edited.Default || edited.CreatedAt.IsZero() {
		t.Fatalf("Edit = %+v, %v: the default flag must stay", edited, err)
	}
	if _, err := svc.Edit(ctx, Profile{ID: "nope", Name: "x", Format: FormatOpenAI, BaseURL: "https://x.test"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("edit missing = %v", err)
	}
	if _, err := svc.Edit(ctx, Profile{ID: "nebius2", Name: "x", Format: "nope", BaseURL: "https://x.test"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad edit = %v", err)
	}
	if err := svc.Remove(ctx, "nebius"); !errors.Is(err, ErrDefault) {
		t.Fatalf("remove default = %v", err)
	}
	if err := svc.SetDefault(ctx, "nebius2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(ctx, "nebius"); err != nil {
		t.Fatalf("remove = %v", err)
	}
	if err := svc.Remove(ctx, "nebius2"); !errors.Is(err, ErrLast) {
		t.Fatalf("remove last = %v", err)
	}
	if err := svc.Remove(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove missing = %v", err)
	}
	if _, ok := svc.Lookup(ctx, "nebius"); ok {
		t.Fatal("a removed profile still resolves")
	}
}

func TestEndpointAndSpeaks(t *testing.T) {
	t.Parallel()
	oa := Profile{ID: "vllm", Format: FormatOpenAI, BaseURL: "http://127.0.0.1:8000/v1"}
	an := Profile{ID: "claude", Format: FormatAnthropic, BaseURL: "https://api.anthropic.com"}
	e := Endpoint(oa)
	h := http.Header{}
	e.Auth(h, "k1")
	if e.ID() != "vllm" || e.BaseURL() != "http://127.0.0.1:8000" || h.Get("Authorization") != "Bearer k1" || h.Get("x-api-key") != "" {
		t.Fatalf("openai endpoint %s %s %v", e.ID(), e.BaseURL(), h)
	}
	h = http.Header{}
	Endpoint(an).Auth(h, "k2")
	if h.Get("x-api-key") != "k2" || h.Get("anthropic-version") != "2023-06-01" || h.Get("Authorization") != "" {
		t.Fatalf("anthropic auth %v", h)
	}
	h = http.Header{"Anthropic-Version": {"2024-01-01"}}
	Endpoint(an).Auth(h, "k2")
	if h.Get("anthropic-version") != "2024-01-01" {
		t.Fatal("the client's version was replaced")
	}
	meta := Profile{Format: FormatOpenAI, AlsoAnthropic: true}
	chat := Profile{Format: FormatOpenAI, API: APIChat}
	resp := Profile{Format: FormatOpenAI, API: APIResponses}
	for _, c := range []struct {
		p    Profile
		wire string
		want bool
	}{
		{an, terminals.WireMessages, true}, {an, terminals.WireResponses, false},
		{meta, terminals.WireMessages, true}, {meta, terminals.WireResponses, true},
		{oa, terminals.WireMessages, false}, {oa, terminals.WireResponses, true},
		{chat, terminals.WireResponses, false}, {resp, terminals.WireResponses, true},
		{oa, "grpc", false},
	} {
		if Speaks(c.p, c.wire) != c.want {
			t.Errorf("Speaks(%+v, %q) != %v", c.p, c.wire, c.want)
		}
	}
}
