package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockLLM(t *testing.T) {
	var seen bytes.Buffer
	h := mockLLM(&seen)
	call := func(path, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if auth {
			r.Header.Set("Authorization", "Bearer k")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("/v1/chat/completions", `{}`, false); w.Code != 401 {
		t.Fatalf("no key = %d", w.Code)
	}
	msgs := `"messages":[{"role":"user","content":"hello there"}]`
	if w := call("/v1/chat/completions", `{`+msgs+`}`, true); !strings.Contains(w.Body.String(), `"content":"echo: hello there"`) {
		t.Fatalf("chat = %s", w.Body)
	}
	if w := call("/v1/chat/completions", `{"stream":true,`+msgs+`}`, true); !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") || !strings.Contains(w.Body.String(), `"content":"echo"`) {
		t.Fatalf("chat stream = %s", w.Body)
	}
	if w := call("/v1/messages", `{`+msgs+`}`, true); !strings.Contains(w.Body.String(), `"text":"echo: hello there"`) {
		t.Fatalf("messages = %s", w.Body)
	}
	if w := call("/v1/messages", `{"stream":true,`+msgs+`}`, true); !strings.Contains(w.Body.String(), "event: message_stop") {
		t.Fatalf("messages stream = %s", w.Body)
	}
	if w := call("/v1/models", ``, true); !strings.Contains(w.Body.String(), "mock-model") {
		t.Fatalf("models = %s", w.Body)
	}
	if w := call("/v1/other", `{}`, true); w.Code != 404 {
		t.Fatalf("other = %d", w.Code)
	}
	if got := strings.Count(seen.String(), `"hello there"`); got != 4 {
		t.Fatalf("recorded %d texts, want 4:\n%s", got, seen.String())
	}
}

func TestMockLLMLoopbackOnly(t *testing.T) {
	t.Setenv("MOCKLLM_LISTEN", "0.0.0.0:7701")
	var stderr bytes.Buffer
	if code := runMockLLM(&stderr); code != exitError || !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("exit %d %q", code, stderr.String())
	}
}
