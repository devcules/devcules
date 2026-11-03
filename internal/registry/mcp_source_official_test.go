package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleRegistryResponse = `{
  "servers": [
    {
      "server": {
        "name": "io.github.example/fs-server",
        "description": "Read and write files.",
        "version": "1.0.0",
        "repository": {"url": "https://github.com/example/fs-server"},
        "packages": [
          {
            "registryType": "npm",
            "identifier": "fs-mcp-server",
            "runtimeHint": "npx",
            "runtimeArguments": [{"type": "positional", "value": "-y"}],
            "transport": {"type": "stdio"},
            "environmentVariables": [
              {"name": "ROOT_PATH", "description": "Root dir", "isRequired": true},
              {"name": "API_KEY", "description": "Auth token", "isRequired": true, "isSecret": true}
            ]
          }
        ]
      }
    },
    {
      "server": {
        "name": "io.github.example/remote-server",
        "description": "A remote-only server.",
        "version": "2.0.0",
        "remotes": [{"type": "streamable-http", "url": "https://example.com/mcp"}]
      }
    },
    {
      "server": {
        "name": "io.github.example/nothing-installable",
        "description": "Has neither packages nor remotes.",
        "version": "1.0.0"
      }
    }
  ]
}`

func TestOfficialMCPSourceListParsesStdioPackage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != "latest" {
			t.Errorf("expected version=latest query param, got %q", r.URL.RawQuery)
		}
		w.Write([]byte(sampleRegistryResponse))
	}))
	defer srv.Close()

	src := &OfficialMCPSource{BaseURL: srv.URL, HTTP: srv.Client()}
	// Empty query = no relevance filter: every installable entry comes
	// through (the one with neither packages nor remotes is skipped).
	entries, err := src.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 installable entries (the one with neither packages nor remotes skipped), got %d: %+v", len(entries), entries)
	}

	var fsEntry *CatalogEntry
	for i := range entries {
		if entries[i].Name == "io.github.example/fs-server" {
			fsEntry = &entries[i]
		}
	}
	if fsEntry == nil || fsEntry.Kind != KindMCP {
		t.Fatalf("unexpected entry: %+v", entries)
	}
	spec, ok := fsEntry.Install.(MCPInstallSpec)
	if !ok {
		t.Fatalf("expected Install to be MCPInstallSpec, got %T", fsEntry.Install)
	}
	if spec.Type != "stdio" || spec.Command != "npx" {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	if len(spec.Args) != 2 || spec.Args[0] != "-y" || spec.Args[1] != "fs-mcp-server" {
		t.Fatalf("unexpected args: %+v", spec.Args)
	}
	if len(spec.EnvVars) != 2 || !spec.EnvVars[0].Required || !spec.EnvVars[1].Secret {
		t.Fatalf("unexpected env var hints: %+v", spec.EnvVars)
	}
}

func TestOfficialMCPSourceListParsesRemoteServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRegistryResponse))
	}))
	defer srv.Close()

	src := &OfficialMCPSource{BaseURL: srv.URL, HTTP: srv.Client()}
	entries, err := src.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var remote *CatalogEntry
	for i := range entries {
		if entries[i].Name == "io.github.example/remote-server" {
			remote = &entries[i]
		}
	}
	if remote == nil {
		t.Fatalf("remote-only server not found in entries: %+v", entries)
	}
	spec := remote.Install.(MCPInstallSpec)
	if spec.Type != "http" || spec.URL != "https://example.com/mcp" {
		t.Fatalf("unexpected remote spec: %+v", spec)
	}
}

func TestOfficialMCPSourceListPropagatesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	src := &OfficialMCPSource{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := src.List(context.Background(), "x"); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
