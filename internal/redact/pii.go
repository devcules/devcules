package redact

import (
	"net/netip"
	"regexp"
)

// piiDetector finds one kind of personal data. check, when set, vets a
// candidate the regex found (a Luhn sum, a real IP address).
type piiDetector struct {
	typ   Type
	re    *regexp.Regexp
	check func(string) bool
}

var piiDetectors = []piiDetector{
	{typ: Email, re: regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?)*\.[A-Za-z]{2,}`)},
	// International (+ and 8-15 digits) or a 10-digit national number
	// in common groupings. Bare digit runs are left alone: they are
	// far more often ids than phone numbers.
	{typ: Phone, re: regexp.MustCompile(`(?:\+[1-9]\d{0,2}[ .\-]?(?:\(\d{1,4}\)[ .\-]?)?\d{2,4}(?:[ .\-]?\d{2,4}){1,4}\b|\(\d{3}\)[ .\-]?\d{3}[ .\-]\d{4}|\b\d{3}[.\-]\d{3}[.\-]\d{4}\b)`), check: phoneDigits},
	{typ: Card, re: regexp.MustCompile(`\b(?:\d[ \-]?){12,18}\d\b`), check: luhn},
	{typ: IPv4, re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), check: publicIPv4},
}

func (d *piiDetector) matches(text string) [][2]int {
	var spans [][2]int
	for _, m := range d.re.FindAllStringIndex(text, -1) {
		if d.check != nil && !d.check(text[m[0]:m[1]]) {
			continue
		}
		if d.typ == IPv4 && dottedNeighbour(text, m[0], m[1]) {
			continue
		}
		spans = append(spans, [2]int{m[0], m[1]})
	}
	return spans
}

// dottedNeighbour reports four dotted numbers that are part of a
// longer dotted run, such as the version 1.2.3.4.5.
func dottedNeighbour(text string, start, end int) bool {
	isDigit := func(i int) bool { return i >= 0 && i < len(text) && text[i] >= '0' && text[i] <= '9' }
	return (start >= 2 && text[start-1] == '.' && isDigit(start-2)) ||
		(end+1 < len(text) && text[end] == '.' && isDigit(end+1))
}

func phoneDigits(s string) bool {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n++
		}
	}
	return n >= 8 && n <= 15
}

// luhn reports a 13 to 19 digit number whose Luhn sum checks out.
func luhn(s string) bool {
	var digits []int
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits = append(digits, int(s[i]-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	for i := range digits {
		d := digits[len(digits)-1-i]
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// publicIPv4 keeps addresses that can identify someone: loopback,
// private, link-local, unspecified and documentation ranges are
// ordinary configuration, not personal data.
func publicIPv4(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return false
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast() {
		return false
	}
	for _, p := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "100.64.0.0/10"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return true
}
