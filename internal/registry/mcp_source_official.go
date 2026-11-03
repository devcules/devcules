package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// OfficialMCPSourceID is the canonical, community-run MCP registry -- the
// reference index the MCP project itself operates. "Canonical" is not the
// same as "vetted safe": anyone can publish here, the same as npm. Devcules
// treats every entry as untrusted code until a human reviews the Inspect
// result and chooses to install it.
const OfficialMCPSourceID = "mcp-official"

const officialRegistryBaseURL = "https://registry.modelcontextprotocol.io"

// OfficialMCPSource lists servers from registry.modelcontextprotocol.io.
type OfficialMCPSource struct {
	BaseURL string // overridable for tests
	HTTP    *http.Client
}

func NewOfficialMCPSource() *OfficialMCPSource {
	return &OfficialMCPSource{BaseURL: officialRegistryBaseURL, HTTP: http.DefaultClient}
}

func (s *OfficialMCPSource) ID() string         { return OfficialMCPSourceID }
func (s *OfficialMCPSource) Kind() ArtifactKind { return KindMCP }

// registryResponse mirrors the fields of registry.modelcontextprotocol.io's
// GET /v0/servers response that Devcules actually uses -- deliberately
// partial (the live schema carries many more fields Devcules has no use
// for), and tolerant of the schema's own version drift (json.Unmarshal
// simply leaves unknown/missing fields at their zero value).
type registryResponse struct {
	Servers []registryServerEntry `json:"servers"`
}

type registryServerEntry struct {
	Server registryServer `json:"server"`
}

type registryServer struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Version     string             `json:"version"`
	Repository  registryRepository `json:"repository"`
	Packages    []registryPackage  `json:"packages"`
	Remotes     []registryRemote   `json:"remotes"`
}

type registryRepository struct {
	URL string `json:"url"`
}

type registryPackage struct {
	RegistryType         string             `json:"registryType"` // "npm" | "pypi" | "oci" | ...
	Identifier           string             `json:"identifier"`
	Version              string             `json:"version"`
	RuntimeHint          string             `json:"runtimeHint"` // "npx" | "uvx" | ...
	RuntimeArguments     []registryArgument `json:"runtimeArguments"`
	PackageArguments     []registryArgument `json:"packageArguments"`
	EnvironmentVariables []registryEnvVar   `json:"environmentVariables"`
	Transport            registryTransport  `json:"transport"`
}

type registryArgument struct {
	Type  string `json:"type"` // "positional" | "named"
	Value string `json:"value"`
	Name  string `json:"name"`
}

type registryEnvVar struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsRequired  bool   `json:"isRequired"`
	IsSecret    bool   `json:"isSecret"`
	Default     string `json:"default"`
}

type registryTransport struct {
	Type string `json:"type"` // "stdio" | "streamable-http" | "sse"
}

type registryRemote struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// MCPEnvVarHint is a required/optional env var a package declared, carried
// through to the frontend so the install form can show real field names
// and descriptions instead of a blank key/value editor.
type MCPEnvVarHint struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	Default     string `json:"default,omitempty"`
}

// MCPInstallSpec is the opaque Install payload for a KindMCP CatalogEntry --
// everything MCPInstaller needs to build the "claude mcp add-json" command,
// and everything MCPInspector needs to dry-run the server.
type MCPInstallSpec struct {
	Type    string          `json:"type"` // "stdio" | "http" | "sse"
	Command string          `json:"command,omitempty"`
	Args    []string        `json:"args,omitempty"`
	URL     string          `json:"url,omitempty"`
	EnvVars []MCPEnvVarHint `json:"env_vars,omitempty"`
}

func (s *OfficialMCPSource) List(ctx context.Context, query string) ([]CatalogEntry, error) {
	entries, err := s.list(ctx, query)
	if err != nil {
		return nil, err
	}
	return FilterRelevant(entries, query), nil
}

// ListLoose answers the same query without the relevance filter — the
// "no strong matches, show me the loose list" escape hatch.
func (s *OfficialMCPSource) ListLoose(ctx context.Context, query string) ([]CatalogEntry, error) {
	return s.list(ctx, query)
}

// list fetches and decodes the upstream page, unfiltered.
func (s *OfficialMCPSource) list(ctx context.Context, query string) ([]CatalogEntry, error) {
	base := s.BaseURL
	if base == "" {
		base = officialRegistryBaseURL
	}
	u, err := url.Parse(base + "/v0/servers")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("version", "latest")
	if query != "" {
		q.Set("search", query)
		// The registry caps a page at 100 and its search matches the
		// publisher namespace too, so most of a page can be io.github.*
		// noise. Ask for a full page and let FilterRelevant below cut it
		// to what actually matches the term.
		q.Set("limit", "100")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	hc := s.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("registry returned %d", resp.StatusCode)
	}
	var out registryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	entries := make([]CatalogEntry, 0, len(out.Servers))
	for _, e := range out.Servers {
		srv := e.Server
		spec, ok := deriveInstallSpec(srv)
		if !ok {
			continue // nothing installable found (no packages, no remotes) -- skip
		}
		entries = append(entries, CatalogEntry{
			ID:          OfficialMCPSourceID + ":" + srv.Name,
			Kind:        KindMCP,
			Name:        srv.Name,
			Description: srv.Description,
			SourceID:    OfficialMCPSourceID,
			SourceName:  "Official MCP Registry",
			RepoURL:     srv.Repository.URL,
			Install:     spec,
		})
	}
	return entries, nil
}

// deriveInstallSpec picks the first workable package (stdio, npx/uvx-run)
// or remote (http/sse) entry and turns it into an MCPInstallSpec. Prefers
// a local stdio package when both exist, since that's what the dry-run
// Inspector can actually exercise without needing the user's own OAuth.
func deriveInstallSpec(srv registryServer) (MCPInstallSpec, bool) {
	for _, pkg := range srv.Packages {
		if pkg.Transport.Type != "" && pkg.Transport.Type != "stdio" {
			continue
		}
		command := pkg.RuntimeHint
		if command == "" {
			switch pkg.RegistryType {
			case "npm":
				command = "npx"
			case "pypi":
				command = "uvx"
			default:
				continue // unknown runtime -- can't derive a command safely
			}
		}
		args := []string{}
		for _, a := range pkg.RuntimeArguments {
			if a.Value != "" {
				args = append(args, a.Value)
			}
		}
		args = append(args, pkg.Identifier)
		for _, a := range pkg.PackageArguments {
			if a.Value != "" {
				args = append(args, a.Value)
			}
		}
		envVars := make([]MCPEnvVarHint, 0, len(pkg.EnvironmentVariables))
		for _, ev := range pkg.EnvironmentVariables {
			envVars = append(envVars, MCPEnvVarHint{
				Name: ev.Name, Description: ev.Description,
				Required: ev.IsRequired, Secret: ev.IsSecret, Default: ev.Default,
			})
		}
		return MCPInstallSpec{Type: "stdio", Command: command, Args: args, EnvVars: envVars}, true
	}
	for _, rem := range srv.Remotes {
		t := rem.Type
		if t == "streamable-http" {
			t = "http"
		}
		if t != "http" && t != "sse" {
			continue
		}
		return MCPInstallSpec{Type: t, URL: rem.URL}, true
	}
	return MCPInstallSpec{}, false
}
