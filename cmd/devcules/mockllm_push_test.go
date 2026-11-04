package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// b7Stream posts body to the mock and joins the text pieces it streams.
func b7Stream(t *testing.T, path, body string) (string, []string) {
	t.Helper()
	srv := httptest.NewServer(mockLLM(io.Discard))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var all strings.Builder
	var pieces []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var ev struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
			Delta struct{ Text string } `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		p := ev.Delta.Text
		if len(ev.Choices) > 0 {
			p = ev.Choices[0].Delta.Content
		}
		if p != "" {
			pieces = append(pieces, p)
			all.WriteString(p)
		}
	}
	return all.String(), pieces
}

// TestMockLLMStreamSplitsRunes: streamed chunks cut on rune boundaries,
// so non-ASCII text echoes intact instead of as U+FFFD.
func TestMockLLMStreamSplitsRunes(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		got, pieces := b7Stream(t, path, `{"stream":true,"messages":[{"role":"user","content":"héllo wörld 🙂"}]}`)
		if got != "echo: héllo wörld 🙂" || strings.ContainsRune(got, utf8.RuneError) {
			t.Errorf("%s streamed %q, want the echo intact", path, got)
		}
		for _, p := range pieces {
			if !utf8.ValidString(p) || utf8.RuneCountInString(p) > 4 {
				t.Errorf("%s piece %q: want valid UTF-8 of at most 4 runes", path, p)
			}
		}
	}
}

// TestMockLLMContentBlocks: an Anthropic (or OpenAI array-form) message
// whose content is a list of blocks echoes its text blocks.
func TestMockLLMContentBlocks(t *testing.T) {
	got, _ := b7Stream(t, "/v1/messages", `{"stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hi "},{"type":"image","source":{}},{"type":"text","text":"there"}]}]}`)
	if got != "echo: hi there" {
		t.Fatalf("content blocks echoed %q, want %q", got, "echo: hi there")
	}
}

// TestPushClientRefusesPrivate: push endpoints come from a browser
// session, so the push client must not reach private, loopback or
// link-local addresses (SSRF), and must not follow redirects.
func TestPushClientRefusesPrivate(t *testing.T) {
	c := pushClient()
	if c.Timeout <= 0 || c.CheckRedirect == nil || c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatalf("push client: timeout %s, redirects not refused", c.Timeout)
	}
	for _, u := range []string{"https://10.0.0.5/push", "https://127.0.0.1/push", "https://169.254.169.254/latest", "https://[::1]/push"} {
		resp, err := c.Post(u, "application/octet-stream", strings.NewReader("x"))
		if err == nil {
			resp.Body.Close()
			t.Errorf("POST %s succeeded, want refused", u)
			continue
		}
		if !strings.Contains(err.Error(), "not a public address") {
			t.Errorf("POST %s: %v, want the private-address refusal", u, err)
		}
	}
}

// b7Proxy is a fake forward proxy on loopback (a private address, like
// a corporate or VPC proxy). It answers plain-http forwards with 201
// and refuses CONNECT with 403, and records every target it was asked
// for.
func b7Proxy(t *testing.T) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method == http.MethodConnect {
			seen = append(seen, "CONNECT "+r.Host)
		} else {
			seen = append(seen, r.Method+" "+r.URL.String())
		}
		mu.Unlock()
		if r.Method == http.MethodConnect {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// b7CheckProxied runs the push client against a proxy on a private
// address: a public endpoint reaches the proxy (the proxy's own address
// is not held to the public-address rule), and a private endpoint is
// refused before the proxy ever hears of it.
func b7CheckProxied(t *testing.T, c *http.Client, seen func() []string) {
	t.Helper()
	resp, err := c.Post("http://1.1.1.1/push/abc", "application/octet-stream", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("POST http://1.1.1.1 via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("POST http://1.1.1.1 via proxy = %d, want the proxy's 201", resp.StatusCode)
	}
	resp, err = c.Post("https://1.1.1.1/push/abc", "application/octet-stream", strings.NewReader("x"))
	if err == nil {
		resp.Body.Close()
	} else if strings.Contains(err.Error(), "not a public address") {
		t.Errorf("POST https://1.1.1.1 refused at the proxy hop: %v", err)
	}
	for _, u := range []string{"https://10.0.0.5/push", "http://10.0.0.5/push", "https://[fd00::1]/push", "https://[64:ff9b::a00:5]/push", "https://[::ffff:169.254.169.254]/latest"} {
		resp, err := c.Post(u, "application/octet-stream", strings.NewReader("x"))
		if err == nil {
			resp.Body.Close()
			t.Errorf("POST %s via proxy succeeded, want refused", u)
			continue
		}
		if !strings.Contains(err.Error(), "not a public address") {
			t.Errorf("POST %s via proxy: %v, want the private-address refusal", u, err)
		}
	}
	want := []string{"POST http://1.1.1.1/push/abc", "CONNECT 1.1.1.1:443"}
	if got := seen(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("proxy saw %q, want %q (private targets must never reach it)", got, want)
	}
}

// TestPushClientProxyChecksTarget: with a proxy chooser that proxies
// everything, a hostname is checked by what it resolves to (localhost
// is refused before the proxy).
func TestPushClientProxyChecksTarget(t *testing.T) {
	proxyURL, seen := b7Proxy(t)
	pu, _ := url.Parse(proxyURL)
	c := newPushClient(func(*http.Request) (*url.URL, error) { return pu, nil })
	resp, err := c.Post("https://localhost/push", "application/octet-stream", strings.NewReader("x"))
	if err == nil {
		resp.Body.Close()
		t.Fatal("POST https://localhost via proxy succeeded, want refused")
	}
	if !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("POST https://localhost via proxy: %v, want the private-address refusal", err)
	}
	b7CheckProxied(t, c, seen)
}

// TestPushClientEnvProxy runs pushClient (http.ProxyFromEnvironment)
// with HTTPS_PROXY/HTTP_PROXY pointing at a proxy on a private address.
// ProxyFromEnvironment reads the environment once per process, so the
// checks run in a child copy of this test binary.
func TestPushClientEnvProxy(t *testing.T) {
	if os.Getenv("B7_PUSH_PROXY_CHILD") == "1" {
		t.Skip("child process")
	}
	proxyURL, seen := b7Proxy(t)
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, proxyURL)
	}
	for _, k := range []string{"NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(k, "")
	}
	t.Setenv("B7_PUSH_PROXY_CHILD", "1")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPushClientEnvProxyChild$", "-test.count=1", "-test.v")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestPushClientEnvProxyChild") {
		t.Fatalf("child: %v\n%s", err, out)
	}
	want := []string{"POST http://1.1.1.1/push/abc", "CONNECT 1.1.1.1:443"}
	if got := seen(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("proxy saw %q, want %q", got, want)
	}
}

// TestPushClientEnvProxyChild is TestPushClientEnvProxy's child half.
func TestPushClientEnvProxyChild(t *testing.T) {
	if os.Getenv("B7_PUSH_PROXY_CHILD") != "1" {
		t.Skip("runs only as TestPushClientEnvProxy's child")
	}
	// The proxy log lives in the parent; the child checks errors only.
	b7CheckProxied(t, pushClient(), func() []string {
		return []string{"POST http://1.1.1.1/push/abc", "CONNECT 1.1.1.1:443"}
	})
	// Loopback targets are never proxied: they dial direct and are refused.
	resp, err := pushClient().Post("https://127.0.0.1/push", "application/octet-stream", strings.NewReader("x"))
	if err == nil {
		resp.Body.Close()
		t.Fatal("POST https://127.0.0.1 succeeded, want refused")
	}
	if !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("POST https://127.0.0.1: %v, want the private-address refusal", err)
	}
}
