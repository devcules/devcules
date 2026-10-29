package redact

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"regexp/syntax"
	"strings"
	"testing"
	"unicode"
)

// sample builds a random string that re's syntax tree accepts. It is
// a best effort: anchors and word boundaries are ignored here and
// checked by the caller with the real regexp.
func sample(rng *rand.Rand, re *syntax.Regexp, b *strings.Builder) {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if re.Flags&syntax.FoldCase != 0 && rng.IntN(2) == 0 {
				r = unicode.SimpleFold(r)
			}
			b.WriteRune(r)
		}
	case syntax.OpCharClass:
		b.WriteRune(pickClass(rng, re.Rune))
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		b.WriteByte(alnum[rng.IntN(len(alnum))])
	case syntax.OpCapture:
		sample(rng, re.Sub[0], b)
	case syntax.OpConcat:
		for _, s := range re.Sub {
			sample(rng, s, b)
		}
	case syntax.OpAlternate:
		sample(rng, re.Sub[rng.IntN(len(re.Sub))], b)
	case syntax.OpStar:
		repeat(rng, re.Sub[0], 0, 3, b)
	case syntax.OpPlus:
		repeat(rng, re.Sub[0], 1, 3, b)
	case syntax.OpQuest:
		repeat(rng, re.Sub[0], 0, 1, b)
	case syntax.OpRepeat:
		hi := re.Max
		if hi < 0 {
			hi = re.Min + 3
		}
		repeat(rng, re.Sub[0], re.Min, hi, b)
	}
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func repeat(rng *rand.Rand, re *syntax.Regexp, lo, hi int, b *strings.Builder) {
	n := lo
	if hi > lo {
		n += rng.IntN(hi - lo + 1)
	}
	for range n {
		sample(rng, re, b)
	}
}

// pickClass picks a printable ASCII rune from a class's ranges,
// falling back to the first rune when the class has none.
func pickClass(rng *rand.Rand, ranges []rune) rune {
	var ascii []rune
	for i := 0; i+1 < len(ranges); i += 2 {
		for r := ranges[i]; r <= ranges[i+1] && r < 0x7f; r++ {
			if r >= 0x21 {
				ascii = append(ascii, r)
			}
		}
	}
	if len(ascii) == 0 {
		if len(ranges) > 0 && ranges[0] == ' ' {
			return ' '
		}
		return ranges[0]
	}
	return ascii[rng.IntN(len(ascii))]
}

// TestEveryRuleDetects proves each Gitleaks rule survives conversion:
// a generated sample is found by the redactor and masked as SECRET.
func TestEveryRuleDetects(t *testing.T) {
	r := newTestRedactor(t)
	p := Policy{Secret: Mask, Email: Off, Phone: Off, Card: Off, IPv4: Off}
	for i := range r.rules {
		ru := &r.rules[i]
		t.Run(ru.id, func(t *testing.T) {
			tree, err := syntax.Parse(ru.re.String(), syntax.Perl)
			if err != nil {
				t.Fatal(err)
			}
			tree = tree.Simplify()
			rng := rand.New(rand.NewPCG(uint64(i), 7))
			for try := range 400 {
				var b strings.Builder
				sample(rng, tree, &b)
				text := " " + b.String() + " "
				// Real text names the service near its token; a sample
				// that lacks every keyword gets the first one as context.
				if len(ru.keywords) > 0 && !containsAny(strings.ToLower(text), ru.keywords) {
					text = ru.keywords[0] + text
				}
				if len(ru.matches(text, strings.ToLower(text))) == 0 {
					continue
				}
				out, findings, err := r.NewTestSession(p).Redact(text)
				if err != nil {
					t.Fatal(err)
				}
				if len(findings) == 0 || findings[0].Type != Secret {
					t.Fatalf("try %d: rule matched but the redactor found %v", try, findings)
				}
				if !strings.Contains(out, "REDACTED_SECRET_") {
					t.Fatalf("try %d: no placeholder in %q", try, out)
				}
				return
			}
			t.Fatalf("no generated sample matched %s in 400 tries", ru.id)
		})
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestRulesLoad(t *testing.T) {
	rules, err := loadRules()
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) < 200 {
		t.Fatalf("loaded %d rules, want the full Gitleaks set (200+)", len(rules))
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r.id] {
			t.Fatalf("duplicate rule id %s", r.id)
		}
		seen[r.id] = true
	}
}

func TestSecretSpan(t *testing.T) {
	re := regexp.MustCompile(`(key)=(\w+)?(x)?`)
	m := re.FindStringSubmatchIndex("key=abc")
	for _, tc := range []struct {
		group      int
		start, end int
	}{{2, 4, 7}, {0, 0, 3}, {9, 0, 3}} {
		s, e := secretSpan(m, tc.group)
		if s != tc.start || e != tc.end {
			t.Errorf("group %d: got %d..%d, want %d..%d", tc.group, s, e, tc.start, tc.end)
		}
	}
	m = regexp.MustCompile(`k=\w+`).FindStringSubmatchIndex("k=abc")
	if s, e := secretSpan(m, 0); s != 0 || e != 5 {
		t.Fatalf("no groups: got %d..%d, want the whole match", s, e)
	}
}

func TestShannon(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
	}{{"", 0}, {"aaaa", 0}, {"abab", 1}, {"abcd", 2}} {
		if got := shannon(tc.in); fmt.Sprintf("%.3f", got) != fmt.Sprintf("%.3f", tc.want) {
			t.Errorf("shannon(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestEntropyAndAllowlists(t *testing.T) {
	r := rule{id: "t", re: regexp.MustCompile(`tok_([a-z0-9]{8})`), keywords: []string{"tok_"}, entropy: 2.5, secretGroup: 1}
	if got := r.matches("tok_aaaaaaaa", "tok_aaaaaaaa"); got != nil {
		t.Fatalf("low entropy matched: %v", got)
	}
	if got := r.matches("tok_k3x9q2m7", "tok_k3x9q2m7"); len(got) != 1 || got[0] != [2]int{4, 12} {
		t.Fatalf("high entropy: got %v, want [4 12]", got)
	}
	if got := r.matches("TOKEN k3x9q2m7", "token k3x9q2m7"); got != nil {
		t.Fatalf("keyword prefilter let a non-match through: %v", got)
	}

	r = rule{id: "t", re: regexp.MustCompile(`tok_([a-z0-9]{10})`), secretGroup: 1}
	text := "x = tok_example123\nkeep tok_k3x9q2m7zz"
	r.allow = []allowlist{{stopwords: []string{"example"}}}
	if got := r.matches(text, text); len(got) != 1 || text[got[0][0]:got[0][1]] != "k3x9q2m7zz" {
		t.Fatalf("stopword: got %v", got)
	}
	r.allow = []allowlist{{regexes: []*regexp.Regexp{regexp.MustCompile(`^k3`)}}}
	if got := r.matches(text, text); len(got) != 1 || text[got[0][0]:got[0][1]] != "example123" {
		t.Fatalf("secret target: got %v", got)
	}
	r.allow = []allowlist{{target: "match", regexes: []*regexp.Regexp{regexp.MustCompile(`^tok_e`)}}}
	if got := r.matches(text, text); len(got) != 1 || text[got[0][0]:got[0][1]] != "k3x9q2m7zz" {
		t.Fatalf("match target: got %v", got)
	}
	r.allow = []allowlist{{target: "line", regexes: []*regexp.Regexp{regexp.MustCompile(`^keep `)}}}
	if got := r.matches(text, text); len(got) != 1 || text[got[0][0]:got[0][1]] != "example123" {
		t.Fatalf("line target: got %v", got)
	}
}
