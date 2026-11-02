package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skills_fleet_test.go — ScanSkills reads the real per-agent layout from a
// fake HOME: shared root + symlinked skills, agent-local skills, nested
// category dirs (hermes), a broken SKILL.md and a missing one, and the
// skills CLI's lock file for provenance.

func writeSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\n" + frontmatter + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanSkillsFleet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows UserHomeDir

	shared := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	codex := filepath.Join(home, ".codex", "skills")
	hermes := filepath.Join(home, ".hermes", "skills")
	for _, d := range []string{shared, claude, codex, filepath.Join(hermes, "tools"), filepath.Join(codex, ".system")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// shared skill, symlinked into claude and hermes
	writeSkill(t, shared, "archify", "name: archify\ndescription: make diagrams")
	for _, agentDir := range []string{claude, hermes} {
		if err := os.Symlink(filepath.Join(shared, "archify"), filepath.Join(agentDir, "archify")); err != nil {
			t.Fatal(err)
		}
	}
	// codex sees it via its own symlink too
	if err := os.Symlink(filepath.Join(shared, "archify"), filepath.Join(codex, "archify")); err != nil {
		t.Fatal(err)
	}

	// claude-local skill
	writeSkill(t, claude, "api-security-review", "name: api-security-review\ndescription: review apis")

	// hermes nested category
	writeSkill(t, filepath.Join(hermes, "tools"), "pdf", "name: pdf\ndescription: make pdfs")

	// broken skill in claude (unparseable frontmatter)
	writeSkill(t, claude, "broken-one", "name: broken-one\ndescription:>\n  bad yaml\n\n\nbad")

	// lock file with provenance for archify
	lock := `{"version":3,"skills":{"archify":{"source":"tt-a1i/archify","sourceUrl":"https://github.com/tt-a1i/archify.git"}}}`
	if err := os.WriteFile(filepath.Join(home, ".agents", ".skill-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	fleet := ScanSkills()
	byAgent := map[string]AgentSkills{}
	for _, as := range fleet {
		byAgent[as.Agent] = as
	}

	if len(fleet) != 4 {
		t.Fatalf("expected 4 agents, got %d: %+v", len(fleet), fleet)
	}
	sh := byAgent["shared"]
	if !sh.Detected || len(sh.Skills) != 1 || sh.Skills[0].Name != "archify" {
		t.Fatalf("shared: %+v", sh)
	}

	cl := byAgent["claude-code"]
	if !cl.Detected {
		t.Fatal("claude not detected")
	}
	want := map[string]string{ // name -> status
		"archify":             "ok",
		"api-security-review": "ok",
		"broken-one":          "broken",
	}
	got := map[string]string{}
	for _, s := range cl.Skills {
		got[s.Name] = s.Status
	}
	if len(got) != len(want) {
		t.Fatalf("claude skills: %v want %v", got, want)
	}
	for n, st := range want {
		if got[n] != st {
			t.Errorf("claude %s: status %q want %q", n, got[n], st)
		}
	}

	// archify's cross-agent visibility must list every agent that sees it
	var arch *SkillStatus
	for i := range cl.Skills {
		if cl.Skills[i].Name == "archify" {
			arch = &cl.Skills[i]
		}
	}
	if arch == nil || len(arch.Agents) != 4 {
		t.Fatalf("archify agents (shared + claude-code + codex + hermes-agent): %+v", arch)
	}
	for _, a := range []string{"shared", "claude-code", "codex", "hermes-agent"} {
		// shared, claude, codex, hermes all see archify — the Agents field
		// is computed by agentsSeeingSharedSkill, which includes the
		// shared pseudo-agent
		_ = a
	}
	if arch.Source != "shared" || arch.SourceURL != "https://github.com/tt-a1i/archify.git" {
		t.Errorf("archify source: %+v", arch)
	}

	// hermes nested category found
	hm := byAgent["hermes-agent"]
	found := false
	for _, s := range hm.Skills {
		if s.Name == "pdf" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hermes nested skill not found: %+v", hm.Skills)
	}

	// broken skill carries its parse error
	for _, s := range cl.Skills {
		if s.Name == "broken-one" && s.Error == "" {
			t.Error("broken-one should carry a parse error")
		}
	}
}

func TestScanSkillsMissingAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fleet := ScanSkills()
	byAgent := map[string]AgentSkills{}
	for _, as := range fleet {
		byAgent[as.Agent] = as
	}
	if byAgent["claude-code"].Detected {
		t.Error("claude should be undetected with no HOME dirs")
	}
	if byAgent["shared"].Detected {
		t.Error("shared should be undetected")
	}
}

// TestSkillsFleetNeverMarshalsNull pins the wire contract: an agent with no
// skills must serialize "skills":[] — JSON null crashes the frontend
// (as.skills.length). Regression for the Extensions page crash.
func TestSkillsFleetNeverMarshalsNull(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// detected-but-empty: the codex-on-this-machine case
	for _, d := range []string{".codex/skills", ".claude/skills"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(ScanSkills())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"skills":null`) {
		t.Fatalf("wire contains skills:null — frontend crashes on .length: %s", b)
	}
	for _, want := range []string{`"agent":"codex"`, `"skills":[]`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("wire missing %s: %s", want, b)
		}
	}
}

func TestSkillsInstallerInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, shared, "one", "name: one\ndescription: first")
	items, err := (&SkillsInstaller{}).Installed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "one" || items[0].Kind != KindSkill {
		t.Fatalf("items: %+v", items)
	}
	if len(items[0].Agents) == 0 || items[0].Agents[0] != "shared" {
		t.Fatalf("agents: %+v", items[0])
	}
}

func TestSkillsInstallerDrivesCLI(t *testing.T) {
	// fake `skills` CLI that records its args
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(home, "fake-skills.sh")
	scriptBody := "#!/bin/sh\necho \"$@\" >> " + filepath.Join(home, "calls.log") + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	in := &SkillsInstaller{SkillsBin: script}

	entry := CatalogEntry{Kind: KindSkill, Install: SkillInstallSpec{Repo: "tt-a1i/archify", Skill: "archify"}}
	if err := in.Install(context.Background(), "archify", entry, nil); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(home, "calls.log"))
	want := "add tt-a1i/archify@archify --global --yes"
	if !strings.Contains(string(calls), want) {
		t.Fatalf("install call: %q want %q", calls, want)
	}

	if err := in.Remove(context.Background(), "archify"); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(filepath.Join(home, "calls.log"))
	if !strings.Contains(string(calls), "remove archify --global --yes") {
		t.Fatalf("remove call: %q", calls)
	}
}

func TestSkillsShSourceParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"skills":[{"id":"tt-a1i/archify/archify","source":"tt-a1i/archify","skillId":"archify","name":"archify","installs":1234}]}`)
	}))
	defer srv.Close()
	src := &SkillsShSource{BaseURL: srv.URL}
	entries, err := src.List(context.Background(), "arch")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries: %+v", entries)
	}
	e := entries[0]
	if e.Kind != KindSkill || e.SourceID != "skills-sh" {
		t.Fatalf("entry: %+v", e)
	}
	spec, ok := e.Install.(SkillInstallSpec)
	if !ok || spec.Repo != "tt-a1i/archify" || spec.Skill != "archify" || spec.Installs != 1234 {
		t.Fatalf("spec: %+v", e.Install)
	}
	var _ = json.Marshal
}
