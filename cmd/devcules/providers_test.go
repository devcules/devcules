package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/providers"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// TestSeedProviders: the first start writes the three built-ins with
// Meta the default; a later start with DEVCULES_META_BASE_URL set to
// a mock moves Meta there and leaves the others alone.
func TestSeedProviders(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{MetaBaseURL: "https://api.meta.ai", AnthropicBaseURL: "https://api.anthropic.com", OpenAIBaseURL: "https://api.openai.com"}
	profiles := providers.New(s.Providers(), s.Meta())
	if err := seedProviders(ctx, profiles, cfg, log); err != nil {
		t.Fatal(err)
	}
	list, _ := profiles.List(ctx)
	if len(list) != 3 || list[0].ID != "anthropic" || list[0].Format != "anthropic" || list[1].ID != "meta" || !list[1].Default || !list[1].AlsoAnthropic {
		t.Fatalf("seeded = %+v", list)
	}
	cfg.MetaBaseURL = "http://127.0.0.1:7701"
	if err := seedProviders(ctx, providers.New(s.Providers(), s.Meta()), cfg, log); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Providers().Get(ctx, "meta")
	o, _ := s.Providers().Get(ctx, "openai")
	if m.BaseURL != "http://127.0.0.1:7701" || o.BaseURL != "https://api.openai.com" {
		t.Fatalf("pinned meta = %s, openai = %s", m.BaseURL, o.BaseURL)
	}
}
