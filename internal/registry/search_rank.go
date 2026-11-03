package registry

// search_rank.go — relevance for registry search results.
//
// The upstream catalogs search their own index, and that index matches
// the *full* server name. Nearly every community MCP server is published
// as io.github.<user>/<server> (publication requires GitHub auth), so a
// query like "github" matches the publisher namespace on all of them: a
// 100-result page came back 85% io.github.* noise and only 15 entries
// that mentioned GitHub at all. The same shape hits skills.sh, whose
// fuzzy index pads a page with merely-related skills.
//
// So the upstream query narrows the set, and this file decides relevance
// locally — the only place that can see the description. A result matches
// when every query token appears in the *local* name (the part after the
// last "/") or in the description. The publisher segment is never
// matched: that is precisely what produced the "it lists everything"
// report.

import (
	"sort"
	"strings"
)

// technicalName reports whether a first segment is a reverse-DNS
// publication namespace. The rule is structural, not a list of TLDs: a
// dotted prefix carries a registry namespace (io.github.alice, com.acme,
// agency.x, ac.inference.sh) — the catalog uses country and generic
// labels, new ones appear daily, and hand-maintaining a TLD set would
// silently stop stripping new ones. A single-label prefix is not a
// namespace, and the publisher is part of the name proper.
func technicalName(publisher string) bool {
	return strings.Contains(publisher, ".")
}

// SplitName turns a catalog name into its publisher and local parts.
// "io.github.alice/github-tools" -> ("io.github.alice", "github-tools");
// "ac.inference.sh/mcp"          -> ("", "mcp") — no slash, no namespace;
// "github"                       -> ("", "github").
func SplitName(name string) (publisher, local string) {
	pub, rest, found := strings.Cut(name, "/")
	if !found {
		return "", name
	}
	if technicalName(pub) {
		return pub, rest
	}
	// A dotted first segment that isn't a namespace is part of the name
	// proper (a domain-shaped product name), so keep it whole.
	return "", name
}

// tokens lowercases and splits a query; nil for a blank one.
func tokens(q string) []string {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(q)))
	return f
}

// matchTier scores one entry against the query tokens: 0 = no match,
// 3 = local name starts with the query, 2 = local name contains every
// token, 1 = description contains every token.
func matchTier(local, description string, toks []string) int {
	if len(toks) == 0 {
		return 1
	}
	l := strings.ToLower(local)
	d := strings.ToLower(description)
	allName, allDesc := true, true
	for _, t := range toks {
		if !strings.Contains(l, t) {
			allName = false
		}
		if !strings.Contains(d, t) {
			allDesc = false
		}
	}
	switch {
	case allName && strings.HasPrefix(l, strings.Join(toks, " ")):
		return 3
	case allName:
		return 2
	case allDesc:
		return 1
	}
	return 0
}

// FilterRelevant keeps only entries whose local name or description
// matches every query token, ranked strongest-first (name prefix, then
// name, then description), preserving upstream order within a tier.
// A blank query returns the entries unchanged: an empty search means
// "show me what's popular", not "filter everything out".
func FilterRelevant(entries []CatalogEntry, query string) []CatalogEntry {
	toks := tokens(query)
	if len(toks) == 0 {
		return entries
	}
	type scored struct {
		entry CatalogEntry
		tier  int
		order int
	}
	kept := make([]scored, 0, len(entries))
	for i, e := range entries {
		_, local := SplitName(e.Name)
		if t := matchTier(local, e.Description, toks); t > 0 {
			kept = append(kept, scored{entry: e, tier: t, order: i})
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].tier != kept[j].tier {
			return kept[i].tier > kept[j].tier
		}
		return kept[i].order < kept[j].order
	})
	out := make([]CatalogEntry, len(kept))
	for i, k := range kept {
		out[i] = k.entry
	}
	return out
}
