package redact

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

//go:embed rules/gitleaks.json
var gitleaksJSON []byte

// rule is one Gitleaks detection rule, compiled.
type rule struct {
	id          string
	re          *regexp.Regexp
	keywords    []string
	entropy     float64
	secretGroup int
	allow       []allowlist
}

// allowlist drops findings that are known not to be secrets: template
// variables, placeholder values, documented examples. Path filters in
// the source data are left out, since redaction scans text, not files.
type allowlist struct {
	regexes   []*regexp.Regexp
	target    string // "secret" (default), "match" or "line"
	stopwords []string
}

// allows reports whether a finding is allowlisted. secret is the
// captured value, match the whole regex match, line the line it sits
// on.
func (a *allowlist) allows(secret, match, line string) bool {
	subject := secret
	switch a.target {
	case "match":
		subject = match
	case "line":
		subject = line
	}
	for _, re := range a.regexes {
		if re.MatchString(subject) {
			return true
		}
	}
	lower := strings.ToLower(secret)
	for _, w := range a.stopwords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

type allowlistJSON struct {
	Regexes     []string `json:"regexes"`
	RegexTarget string   `json:"regexTarget"`
	Stopwords   []string `json:"stopwords"`
}

func (j allowlistJSON) compile(owner string) (allowlist, error) {
	a := allowlist{target: j.RegexTarget}
	for _, src := range j.Regexes {
		re, err := regexp.Compile(src)
		if err != nil {
			return a, fmt.Errorf("redact: %s allowlist regex does not compile: %w", owner, err)
		}
		a.regexes = append(a.regexes, re)
	}
	for _, w := range j.Stopwords {
		a.stopwords = append(a.stopwords, strings.ToLower(w))
	}
	return a, nil
}

// loadRules parses and compiles the embedded rule data. A rule that
// does not compile is a build defect, reported with its id. The global
// allowlist is appended to every rule's own.
func loadRules() ([]rule, error) {
	var data struct {
		Allowlist allowlistJSON `json:"allowlist"`
		Rules     []struct {
			ID          string          `json:"id"`
			Regex       string          `json:"regex"`
			Keywords    []string        `json:"keywords"`
			Entropy     float64         `json:"entropy"`
			SecretGroup int             `json:"secretGroup"`
			Allowlists  []allowlistJSON `json:"allowlists"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(gitleaksJSON, &data); err != nil {
		return nil, fmt.Errorf("redact: rules/gitleaks.json does not parse: %w", err)
	}
	global, err := data.Allowlist.compile("global")
	if err != nil {
		return nil, err
	}
	out := make([]rule, 0, len(data.Rules))
	for _, r := range data.Rules {
		re, err := regexp.Compile(r.Regex)
		if err != nil {
			return nil, fmt.Errorf("redact: rule %s does not compile: %w", r.ID, err)
		}
		kw := make([]string, len(r.Keywords))
		for i, k := range r.Keywords {
			kw[i] = strings.ToLower(k)
		}
		allow := []allowlist{global}
		for _, a := range r.Allowlists {
			c, err := a.compile(r.ID)
			if err != nil {
				return nil, err
			}
			allow = append(allow, c)
		}
		out = append(out, rule{id: r.ID, re: re, keywords: kw, entropy: r.Entropy, secretGroup: r.SecretGroup, allow: allow})
	}
	return out, nil
}

// matches reports the secret spans rule r finds in text. lower is
// text lowercased once by the caller, for the keyword prefilter.
func (r *rule) matches(text, lower string) [][2]int {
	if len(r.keywords) > 0 {
		hit := false
		for _, k := range r.keywords {
			if strings.Contains(lower, k) {
				hit = true
				break
			}
		}
		if !hit {
			return nil
		}
	}
	var spans [][2]int
	for _, m := range r.re.FindAllStringSubmatchIndex(text, -1) {
		start, end := secretSpan(m, r.secretGroup)
		if start < 0 || start == end {
			continue
		}
		secret := text[start:end]
		if r.entropy > 0 && shannon(secret) < r.entropy {
			continue
		}
		if r.allowed(secret, text[m[0]:m[1]], lineAround(text, m[0], m[1])) {
			continue
		}
		spans = append(spans, [2]int{start, end})
	}
	return spans
}

func (r *rule) allowed(secret, match, line string) bool {
	for i := range r.allow {
		if r.allow[i].allows(secret, match, line) {
			return true
		}
	}
	return false
}

// lineAround returns the line(s) holding text[start:end].
func lineAround(text string, start, end int) string {
	if i := strings.LastIndexByte(text[:start], '\n'); i >= 0 {
		start = i + 1
	} else {
		start = 0
	}
	if i := strings.IndexByte(text[end:], '\n'); i >= 0 {
		end += i
	} else {
		end = len(text)
	}
	return text[start:end]
}

// secretSpan picks the secret out of one match: the rule's secret
// group when it names one, else the first group that matched, else the
// whole match. Surrounding context such as "api_key=" stays in place.
func secretSpan(m []int, group int) (int, int) {
	if group > 0 && 2*group+1 < len(m) {
		return m[2*group], m[2*group+1]
	}
	for g := 1; 2*g+1 < len(m); g++ {
		if m[2*g] >= 0 && m[2*g+1] > m[2*g] {
			return m[2*g], m[2*g+1]
		}
	}
	return m[0], m[1]
}

// shannon is the Shannon entropy of s in bits per byte.
func shannon(s string) float64 {
	if s == "" {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}
