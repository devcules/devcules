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
