package registry

import (
	"context"
	"os/exec"
	"testing"
)

func requireClaudeCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not available")
	}
}

// Real integration test against the actual `claude mcp` subcommand (the
// only thing this Installer is allowed to touch ~/.claude.json through).
// Uses a harmless no-op command ("true") so nothing is actually run beyond
// what add-json/remove themselves need to do.
func TestMCPInstallerLifecycle(t *testing.T) {
	requireClaudeCLI(t)
	in := &MCPInstaller{Scope: "user"}
	name := "devcules-test-probe-registry"
	t.Cleanup(func() { _ = in.Remove(context.Background(), name) })

	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{
		Type: "stdio", Command: "true", Args: []string{},
		EnvVars: []MCPEnvVarHint{{Name: "FOO", Required: true}},
	}}
	if err := in.Install(context.Background(), name, entry, map[string]string{"FOO": "bar"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	items, err := in.Installed(context.Background())
	if err != nil {
		t.Fatalf("Installed: %v", err)
	}
	var found *InstalledItem
	for i := range items {
		if items[i].Name == name {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatalf("expected %q in Installed(), got %+v", name, items)
	}
	if found.Detail != "true" || found.Kind != KindMCP {
		t.Fatalf("unexpected item: %+v", found)
	}

	if err := in.Remove(context.Background(), name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	items, _ = in.Installed(context.Background())
	for _, it := range items {
		if it.Name == name {
			t.Fatalf("expected %q to be gone after Remove, still present: %+v", name, items)
		}
	}
}

func TestMCPInstallerRejectsEmptyName(t *testing.T) {
	in := &MCPInstaller{}
	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{Type: "stdio", Command: "true"}}
	if err := in.Install(context.Background(), "  ", entry, nil); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

func TestMCPInstallerRejectsNonMCPEntry(t *testing.T) {
	in := &MCPInstaller{}
	entry := CatalogEntry{Kind: KindMCP, Install: "not a spec"}
	if err := in.Install(context.Background(), "x", entry, nil); err == nil {
		t.Fatal("expected an error for a non-MCP install spec")
	}
}
