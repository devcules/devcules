package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// skills_fleet.go — the on-disk truth about Skills across the coding agents
// on this machine. Devcules never records "what we think we installed"; it
// reads the real SKILL.md files from each agent's directory (and the shared
// ~/.agents/skills that the `skills` CLI propagates from), so the page shows
// what each agent genuinely sees right now — including broken ones.

// SkillStatus is one skill as one agent sees it.
type SkillStatus struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Source: "shared" (from ~/.agents/skills, several agents may see it)
	// or "local" (only in this agent's own directory).
	Source string `json:"source"`
	// Status: "ok" | "broken" | "missing" — missing = directory with no
	// readable SKILL.md, broken = SKILL.md whose frontmatter will not parse
	// (the agent skips those; seeing WHY is the point of the status view).
	Status string `json:"status"`
	// Error is the parse error for broken skills, "" otherwise.
	Error string `json:"error,omitempty"`
	// Agents lists every agent that can see this skill (filled in for
	// shared skills; a local skill lists just its own agent).
	Agents []string `json:"agents,omitempty"`
	// SourceURL is the install provenance from ~/.agents/.skill-lock.json
	// (shared skills installed via the `skills` CLI), "" when unknown.
	SourceURL string `json:"source_url,omitempty"`
}

// AgentSkills is the skill inventory of one agent.
type AgentSkills struct {
	Agent    string        `json:"agent"` // claude-code | codex | hermes-agent | shared
	Dir      string        `json:"dir"`   // the directory scanned
	Detected bool          `json:"detected"`
	Skills   []SkillStatus `json:"skills"`
}

// skillAgents lists the agents whose skill directories are scanned, in the
// order they appear on the page. The shared root is first because the
// Agents-visibility map is computed from it.
var skillAgents = []struct {
	id  string
	dir func(home string) string
}{
	{"shared", func(home string) string { return filepath.Join(home, ".agents", "skills") }},
	{"claude-code", func(home string) string { return filepath.Join(home, ".claude", "skills") }},
	{"codex", func(home string) string { return filepath.Join(home, ".codex", "skills") }},
	{"hermes-agent", func(home string) string { return filepath.Join(home, ".hermes", "skills") }},
}

// SharedSkillsDir is the canonical shared skills location (~/.agents/skills),
// exported so installers and tests agree on one path.
func SharedSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// skillLock is the provenance file the `skills` CLI keeps next to the shared
// skills (~/.agents/.skill-lock.json).
type skillLock struct {
	Skills map[string]struct {
		Source    string `json:"source"`
		SourceURL string `json:"sourceUrl"`
	} `json:"skills"`
}

func readSkillLock(home string) skillLock {
	var lock skillLock
	b, err := os.ReadFile(filepath.Join(home, ".agents", ".skill-lock.json"))
	if err == nil {
		_ = json.Unmarshal(b, &lock)
	}
	return lock
}

// parseSkillHeader pulls name/description out of a SKILL.md's YAML
// frontmatter. Returns an error the same way the agents' own loaders fail:
// a file with no frontmatter or unparseable YAML is a skill the agent will
// skip — that is exactly what the status view needs to show.
func parseSkillHeader(b []byte) (name, description string, err error) {
	s := string(b)
	if !strings.HasPrefix(s, "---") {
		return "", "", errors.New("no YAML frontmatter")
	}
	rest := s[3:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		rest = rest[:i]
	} else {
		return "", "", errors.New("frontmatter not closed")
	}
	var head struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rest), &head); err != nil {
		return "", "", fmt.Errorf("YAML parse: %w", err)
	}
	if head.Name == "" {
		return "", "", errors.New("frontmatter has no name")
	}
	return head.Name, head.Description, nil
}

// scanSkillsDir walks one agent's directory and returns every skill-shaped
// entry in it. A skill is a directory containing SKILL.md (real or symlinked
// — the shared-skill pattern); a directory without SKILL.md is a category
// folder (hermes groups skills that way), recursed one level. Always returns
// a non-nil slice so it marshals as [] on the wire, never null.
func scanSkillsDir(root, sharedRoot string) []SkillStatus {
	out := []SkillStatus{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue // .system bundles and lock files
			}
			full := filepath.Join(dir, name)
			// a symlink to a directory: the shared-skill pattern — always a skill
			if e.Type()&fs.ModeSymlink != 0 {
				if st, err := os.Stat(full); err == nil && st.IsDir() {
					out = append(out, readSkill(full, sharedRoot))
				}
				continue
			}
			if !e.IsDir() {
				continue
			}
			// a real directory: a skill if it holds SKILL.md, else a category
			if st, err := os.Stat(filepath.Join(full, "SKILL.md")); err == nil && !st.IsDir() {
				out = append(out, readSkill(full, sharedRoot))
				continue
			}
			if depth < 2 {
				walk(full, depth+1)
			}
		}
	}
	walk(root, 0)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readSkill loads one skill directory's SKILL.md and files its status.
// The dir (a symlink into the shared skills root) is compared against the
// shared root symlink-resolved on BOTH sides: on macOS the temp dir the
// caller built the root from already contains a /var→/private/var hop, so
// matching a resolved skill against an unresolved root never sees "shared".
func readSkill(dir, sharedRoot string) SkillStatus {
	st := SkillStatus{Name: filepath.Base(dir), Source: "local", Status: "ok"}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		root := sharedRoot
		if r, rerr := filepath.EvalSymlinks(sharedRoot); rerr == nil {
			root = r
		}
		if root != "" && strings.HasPrefix(resolved+string(filepath.Separator), root+string(filepath.Separator)) {
			st.Source = "shared"
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		st.Status = "missing"
		st.Error = "no readable SKILL.md"
		return st
	}
	name, desc, err := parseSkillHeader(b)
	if err != nil {
		st.Status = "broken"
		st.Error = err.Error()
		return st
	}
	st.Name, st.Description = name, desc
	return st
}

// ScanSkills reads every known agent's skill directory and returns the fleet
// view: one AgentSkills per agent, plus, for shared skills, the list of
// agents that can see them. Missing directories are reported as
// detected=false rather than skipped, so the page can say "not installed
// here" instead of silently omitting an agent.
func ScanSkills() []AgentSkills {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sharedRoot := filepath.Join(home, ".agents", "skills")
	lock := readSkillLock(home)

	out := make([]AgentSkills, 0, len(skillAgents))
	for _, a := range skillAgents {
		dir := a.dir(home)
		as := AgentSkills{Agent: a.id, Dir: dir, Skills: []SkillStatus{}}
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			as.Detected = true
			as.Skills = scanSkillsDir(dir, sharedRoot)
		}
		out = append(out, as)
	}

	// which agents see each shared skill: a symlink or copy of the same
	// name resolving under the shared root. Local copies of the same name
	// count too — what matters is what the agent will load.
	for i := range out {
		if out[i].Agent == "shared" {
			continue
		}
		for j := range out[i].Skills {
			s := &out[i].Skills[j]
			if s.Source != "shared" {
				s.Agents = []string{out[i].Agent}
				continue
			}
			s.Agents = agentsSeeingSharedSkill(out, s.Name)
			if l, ok := lock.Skills[s.Name]; ok {
				s.SourceURL = l.SourceURL
			}
		}
	}
	return out
}

// agentsSeeingSharedSkill reports every agent whose directory can load the
// named skill (a shared-skill symlink of that name, or the shared agent
// itself).
func agentsSeeingSharedSkill(fleet []AgentSkills, name string) []string {
	var agents []string
	for _, as := range fleet {
		if as.Agent == "shared" {
			for _, s := range as.Skills {
				if s.Name == name {
					agents = append(agents, as.Agent)
					break
				}
			}
			continue
		}
		for _, s := range as.Skills {
			if s.Name == name && (s.Source == "shared" || s.Status == "ok") {
				agents = append(agents, as.Agent)
				break
			}
		}
	}
	return agents
}
