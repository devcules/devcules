package registry

import (
	"testing"
)

// search_rank_test.go — the "search lists everything" regression. A real
// upstream page for "github" came back 85% io.github.* entries, most with
// nothing to do with GitHub; these pin that they no longer survive.

func entry(name, desc string) CatalogEntry {
	return CatalogEntry{Name: name, Description: desc}
}

func names(entries []CatalogEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func TestSplitNameDropsTechnicalNamespaceOnly(t *testing.T) {
	cases := []struct{ in, pub, local string }{
		// publication namespace: publisher dropped, local name kept
		{"io.github.alice/github-tools", "io.github.alice", "github-tools"},
		{"com.acme/tools", "com.acme", "tools"},
		// alphanumeric handle after a TLD prefix is still a namespace
		// (this is the exact shape that flooded the github search)
		{"io.github.000safah000-ai/blackhawk-mcp", "io.github.000safah000-ai", "blackhawk-mcp"},
		// domain-shaped publisher with a TLD first label splits too
		{"ac.inference.sh/mcp", "ac.inference.sh", "mcp"},
		{"github", "", "github"},
		// a single-label prefix is a publisher name, not a namespace
		{"foo/tools", "", "foo/tools"},
		{"foo.bar", "", "foo.bar"},
	}
	for _, c := range cases {
		pub, local := SplitName(c.in)
		if pub != c.pub || local != c.local {
			t.Errorf("SplitName(%q) = (%q,%q), want (%q,%q)", c.in, pub, local, c.pub, c.local)
		}
	}
}

func TestFilterRelevantDropsPublisherNamespaceNoise(t *testing.T) {
	// The real shape of the bad page: mostly io.github.* entries whose
	// local names say nothing about github, plus the genuine hits.
	in := []CatalogEntry{
		entry("io.github.000safah000-ai/blackhawk-mcp", "connect AI to Google Sheets"),
		entry("io.github.0200project/base-transaction-decoder", "Decode Base txs for agents"),
		entry("io.github.02inf/opencomment-ai", "Research saved LinkedIn contacts"),
		entry("ai.smithery/smithery-ai-github", "Access the GitHub API"),
		entry("com.mcparmory/github", "Manage repositories, users, releases"),
		entry("io.github.alice/github-tools", "unrelated blurb"),
	}
	got := names(FilterRelevant(in, "github"))
	for _, n := range got {
		if n == "io.github.000safah000-ai/blackhawk-mcp" ||
			n == "io.github.0200project/base-transaction-decoder" ||
			n == "io.github.02inf/opencomment-ai" {
			t.Fatalf("namespace-only match survived: %q (got %v)", n, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %v, want the 3 real github entries", got)
	}
}

func TestFilterRelevantRanksNameAboveDescription(t *testing.T) {
	in := []CatalogEntry{
		entry("com.a2awire/data-trending-repository", "Trending GitHub repos"), // description hit
		entry("com.mcparmory/github", "Manage repositories"),                   // name hit
	}
	got := FilterRelevant(in, "github")
	if len(got) != 2 || got[0].Name != "com.mcparmory/github" {
		t.Fatalf("name match must rank first, got %v", names(got))
	}
}

func TestFilterRelevantMatchesDescriptionOnly(t *testing.T) {
	in := []CatalogEntry{
		entry("acme/db-tools", "Run postgres queries safely"),
		entry("acme/github", "Manage repos"),
	}
	got := FilterRelevant(in, "postgres")
	if len(got) != 1 || got[0].Name != "acme/db-tools" {
		t.Fatalf("a description-only match must survive, got %v", names(got))
	}
}

func TestFilterRelevantRequiresEveryToken(t *testing.T) {
	in := []CatalogEntry{
		entry("acme/github-tools", "github helpers"),
		entry("acme/slack-tools", "slack helpers"),
	}
	if got := FilterRelevant(in, "github slack"); len(got) != 0 {
		t.Fatalf("no entry has both tokens, got %v", names(got))
	}
	if got := FilterRelevant(in, "acme github"); len(got) != 1 {
		t.Fatalf("publisher token must not match, got %v", names(got))
	}
}

func TestFilterRelevantBlankQueryKeepsPopularityList(t *testing.T) {
	in := []CatalogEntry{entry("a/one", ""), entry("b/two", "")}
	if got := FilterRelevant(in, "   "); len(got) != 2 {
		t.Fatalf("empty query must not filter, got %v", names(got))
	}
}

func TestFilterRelevantKeepsUpstreamOrderWithinTier(t *testing.T) {
	in := []CatalogEntry{
		entry("a/github-one", ""),
		entry("b/github-two", ""),
		entry("c/github-three", ""),
	}
	got := names(FilterRelevant(in, "github"))
	if got[0] != "a/github-one" || got[1] != "b/github-two" || got[2] != "c/github-three" {
		t.Fatalf("upstream order lost within a tier: %v", got)
	}
}
