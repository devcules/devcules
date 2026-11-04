package desk

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	bad := []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.9", "169.254.169.254", "100.116.38.35", "0.0.0.0", "::1", "fe80::1", "fd00::1"}
	for _, s := range bad {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "140.82.121.4", "2606:4700::1111"} {
		if !isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must be public", s)
		}
	}
}

func TestNewClient_RefusesLoopbackUnlessAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	old := AllowPrivateHosts
	t.Cleanup(func() { AllowPrivateHosts = old })

	AllowPrivateHosts = false
	c := NewClient(3 * time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	if _, err := c.Do(req); err == nil {
		t.Fatal("dialing a loopback server must be refused")
	}
	AllowPrivateHosts = true
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("allowed: %v", err)
	}
	resp.Body.Close()
}

// TestIsPublicIPEmbeddedIPv4: NAT64 (64:ff9b::/96) is judged by the
// IPv4 address it embeds, and IPv4-compatible ::a.b.c.d (deprecated) is
// never public, so neither form reaches a private IPv4 host.
func TestIsPublicIPEmbeddedIPv4(t *testing.T) {
	for _, s := range []string{
		"64:ff9b::10.0.0.5", "64:ff9b::7f00:1", "64:ff9b::169.254.169.254", "64:ff9b::192.168.1.1", "64:ff9b::100.100.100.100", "64:ff9b::",
		"::10.0.0.5", "::127.0.0.1", "::169.254.169.254", "::8.8.8.8", "::0.0.0.2",
	} {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
	for _, s := range []string{"64:ff9b::8.8.8.8", "64:ff9b::8c52:7904", "64:ff9c::10.0.0.5", "2001:db8:64:ff9b::1"} {
		if !isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must be public", s)
		}
	}
}

// TestPublicAddrs: the resolver check NewClient dials through, exported
// for clients that hand the target to a proxy instead of dialing it.
func TestPublicAddrs(t *testing.T) {
	old := AllowPrivateHosts
	t.Cleanup(func() { AllowPrivateHosts = old })
	AllowPrivateHosts = false
	for _, h := range []string{"10.0.0.5", "127.0.0.1", "::1", "64:ff9b::a00:5", "::10.0.0.5"} {
		if _, err := PublicAddrs(context.Background(), h); err == nil {
			t.Errorf("PublicAddrs(%s) passed, want refused", h)
		}
	}
	ips, err := PublicAddrs(context.Background(), "8.8.8.8")
	if err != nil || len(ips) != 1 || !ips[0].IP.Equal(net.ParseIP("8.8.8.8")) {
		t.Errorf("PublicAddrs(8.8.8.8) = %v, %v", ips, err)
	}
}
