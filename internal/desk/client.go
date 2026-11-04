package desk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// AllowPrivateHosts is flipped on by tests that run fakes on loopback.
var AllowPrivateHosts = false

// NewClient returns an outbound HTTP client whose dialer refuses loopback,
// private, link-local and unspecified addresses, on every hop (redirects
// dial again), so a user-supplied feed URL cannot be used to reach the
// control-plane, the IVM's services or cloud metadata (SSRF).
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := PublicAddrs(ctx, host)
			if err != nil {
				return nil, err
			}
			// connect to the address we validated, not a re-resolved one
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to %s refused", req.URL.Scheme)
			}
			return nil
		},
	}
}

// PublicAddrs resolves host and answers its addresses, or an error
// unless every one of them is public (see NewClient; AllowPrivateHosts
// lifts the rule for tests).
func PublicAddrs(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no address for %s", host)
	}
	for _, ip := range ips {
		if !AllowPrivateHosts && !isPublicIP(ip.IP) {
			return nil, fmt.Errorf("refusing to connect to %s (%s): not a public address", host, ip.IP)
		}
	}
	return ips, nil
}

// nat64 is the NAT64 well-known prefix 64:ff9b::/96 (RFC 6052): its
// last four bytes are an IPv4 address the translator connects to.
var nat64 = net.IP{0, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}

func isPublicIP(ip net.IP) bool {
	// IPv6 forms that carry an IPv4 address in their last four bytes:
	// NAT64 is as public as the address it embeds; IPv4-compatible
	// ::a.b.c.d is deprecated (RFC 4291 2.5.5.1) and never a real
	// destination, so all of ::/96 is refused. (::ffff:a.b.c.d is
	// unwrapped by To4 below.)
	if len(ip) == net.IPv6len && ip.To4() == nil {
		switch {
		case ip[:12].Equal(net.IPv6zero[:12]):
			return false
		case ip[:12].Equal(nat64):
			return isPublicIP(ip[12:16])
		}
	}

	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	// IPv4-mapped IPv6 and the carrier-grade NAT / documentation ranges
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // 100.64/10 (Tailscale lives here too)
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0/24
			return false
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19): // benchmarking
			return false
		}
	}
	return true
}
