// Package registry provides a kind-agnostic core for browsing, inspecting,
// and installing "extensions" for Claude Code -- MCP servers first, with
// Skills and Agents meant to plug into the same three small interfaces
// later (Source, Inspector, Installer) rather than needing their own page
// or data model.
package registry

import "context"

// ArtifactKind identifies what kind of extension a CatalogEntry describes.
type ArtifactKind string

const (
	KindMCP   ArtifactKind = "mcp"
	KindSkill ArtifactKind = "skill"
	KindAgent ArtifactKind = "agent"
)

// CatalogEntry is one browsable item from a registry Source. Install is
// kind-specific and opaque to everything except that kind's Installer --
// for MCP it's {"type","command","args","env"}; a future Skill/Agent
// source would put a repo/file reference there instead.
type CatalogEntry struct {
	ID          string       `json:"id"` // source-qualified, stable across a search
	Kind        ArtifactKind `json:"kind"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	SourceID    string       `json:"source_id"`
	SourceName  string       `json:"source_name"`
	RepoURL     string       `json:"repo_url,omitempty"`
	Install     any          `json:"install"`
}

// ToolInfo is one tool an MCP server declared via tools/list.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Inspection previews what installing an entry would actually grant --
// for MCP, the real tool list discovered by running the server; for a
// skill/agent, the file content itself. Warning is set (Tools/Content
// left empty) when inspection couldn't complete, e.g. the server needs
// OAuth, timed out, or the binary isn't installed -- never a hard error,
// since "can't preview" shouldn't block browsing the rest of the catalog.
type Inspection struct {
	Summary string     `json:"summary"`
	Tools   []ToolInfo `json:"tools,omitempty"`
	Content string     `json:"content,omitempty"`
	Warning string     `json:"warning,omitempty"`
}

// InstalledItem is one currently-configured extension, read live from
// wherever its kind actually stores config -- never from Devcules' own
// database, so there's no way for Devcules' view to drift from reality.
type InstalledItem struct {
	Name   string       `json:"name"`
	Kind   ArtifactKind `json:"kind"`
	Detail string       `json:"detail,omitempty"`
	// Agents lists the coding agents this item is visible to (skills;
	// MCP servers are installed per-harness so this stays empty for them).
	Agents []string `json:"agents,omitempty"`
}

// Source lists/searches entries of ONE kind from one place (a registry
// API, a curated repo, ...). A new registry for an existing kind, or a
// brand new kind, is just a new Source implementation.
type Source interface {
	ID() string
	Kind() ArtifactKind
	List(ctx context.Context, query string) ([]CatalogEntry, error)
}

// LooseSearcher is implemented by a Source that can answer the same query
// WITHOUT its local relevance filter. The UI uses it only for the "no
// strong matches" escape hatch (show the upstream's loose list), so the
// default path can stay strict without making a zero-result search a dead
// end. A Source that doesn't implement it simply has no escape hatch.
type LooseSearcher interface {
	ListLoose(ctx context.Context, query string) ([]CatalogEntry, error)
}

// Inspector previews an entry before install.
type Inspector interface {
	Kind() ArtifactKind
	Inspect(ctx context.Context, entry CatalogEntry) (Inspection, error)
}

// Installer performs the actual install/uninstall for one kind, and
// reports what's currently installed by reading the real config/files --
// never a Devcules-side record of "what we think we installed".
type Installer interface {
	Kind() ArtifactKind
	Install(ctx context.Context, name string, entry CatalogEntry, params map[string]string) error
	Remove(ctx context.Context, name string) error
	Installed(ctx context.Context) ([]InstalledItem, error)
}
