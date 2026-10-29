package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store/sqlite"
)

type envKey string

func (k envKey) Get(context.Context, string) ([]byte, error) { return []byte(k), nil }

// TestLiveRoundTrip calls the real Meta Model API through the router.
// It runs only with META_API_KEY set (META_MODEL picks the model,
// default muse-spark-1.3): the model is asked to repeat a made-up AWS
// key, which must leave masked and come back restored.
func TestLiveRoundTrip(t *testing.T) {
	key := os.Getenv("META_API_KEY")
	if key == "" {
		t.Skip("META_API_KEY not set: the live Meta Model API test is skipped")
	}
	model := os.Getenv("META_MODEL")
	if model == "" {
		model = "muse-spark-1.3"
	}
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, _ := redact.New(bytes.Repeat([]byte{3}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	tokens := launch.New(s)
	token, _, _ := tokens.Mint(ctx, "live test", "meta", time.Hour)
	rt, err := router.New(router.Options{Providers: []router.Provider{New(os.Getenv("DEVCULES_META_BASE_URL"))}, Keys: envKey(key), Tokens: tokens, Redactor: r, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(rt)
	defer srv.Close()

	fake := "AKIA" + "Z7QWERTY4UIOP3XZ"
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": "Repeat this text exactly and add nothing else: key=" + fake}},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/llm/meta/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Choices []struct {
			Message struct{ Content string }
		}
	}
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw.Bytes(), &out); err != nil || len(out.Choices) == 0 {
		t.Fatalf("reply %s: %v", raw, err)
	}
	got := out.Choices[0].Message.Content
	if strings.Contains(got, "REDACTED_SECRET_") || !strings.Contains(got, fake) {
		t.Fatalf("reply %q: want the key restored", got)
	}
	if st := r.Stats()[redact.Secret]; st.Masked != 1 {
		t.Fatalf("masked = %d, want 1", st.Masked)
	}
}
