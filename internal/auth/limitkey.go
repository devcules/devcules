package auth

import "net/netip"

// limitKey is the key the per-client sign-in limits count under. An
// IPv6 client is keyed by its /64, the smallest block one subscriber
// is handed, so rotating addresses inside it buys no extra guesses,
// begins or ceremonies. IPv4 (and IPv4-mapped IPv6) keeps the full
// address; anything unparseable is used as given. Audit rows keep the
// full address: only the limiters group.
func limitKey(clientIP string) string {
	addr, err := netip.ParseAddr(clientIP)
	if err != nil {
		return clientIP
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return clientIP
	}
	return prefix.String()
}
