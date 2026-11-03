package registry

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// skill_install.go — installs/removes skills through the real `skills` CLI
// (https://skills.sh, the tool codex and claude both use), which handles
// per-agent propagation (shared ~/.agents/skills + symlinks into each agent
// that supports skills) — the same "drive the harness's own tool" stance as
// the MCP installer driving `claude mcp`. Listing installed skills does NOT
// shell out: it reads the on-disk truth via ScanSkills.

// SkillsInstaller manages skills via the `skills` CLI (npx skills).
type SkillsInstaller struct {
	// SkillsBin defaults to "skills" if on PATH, else "npx -y skills".
	// May contain args ("npx -y skills"); tests point it at a fake script.
	SkillsBin string
}

func (in *SkillsInstaller) Kind() ArtifactKind { return KindSkill }

func (in *SkillsInstaller) bin() (string, []string, error) {
	if in.SkillsBin != "" {
		// "npx -y skills" is a command plus its leading arguments. Split
		// it into words: `sh -c` would hand the subcommand to the shell as
		// $0 and drop it, so `skills` ran with no arguments at all.
		f := strings.Fields(in.SkillsBin)
		if len(f) == 0 {
			return "", nil, fmt.Errorf("skills binary is blank")
		}
		return f[0], f[1:], nil
	}
	if _, err := exec.LookPath("skills"); err == nil {
		return "skills", nil, nil
	}
	if _, err := exec.LookPath("npx"); err == nil {
		return "npx", []string{"-y", "skills"}, nil
	}
	return "", nil, fmt.Errorf("neither `skills` nor `npx` found on PATH")
}

// Install adds a skill with `skills add owner/repo[@skill] --global --yes`.
// The CLI picks the agents; Devcules does not guess per-agent targets.
func (in *SkillsInstaller) Install(ctx context.Context, name string, entry CatalogEntry, params map[string]string) error {
	spec, ok := entry.Install.(SkillInstallSpec)
	if !ok {
		return fmt.Errorf("entry has no skill install spec")
	}
	pkg := spec.Repo
	if spec.Skill != "" {
		pkg = spec.Repo + "@" + spec.Skill
	}
	bin, pre, err := in.bin()
	if err != nil {
		return err
	}
	args := append(append([]string{}, pre...), "add", pkg, "--global", "--yes")
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("skills add %s: %s", pkg, lastLines(out, 8))
	}
	return nil
}

// Remove uninstalls with `skills remove <name> --global --yes`.
func (in *SkillsInstaller) Remove(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	bin, pre, err := in.bin()
	if err != nil {
		return err
	}
	args := append(append([]string{}, pre...), "remove", name, "--global", "--yes")
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("skills remove %s: %s", name, lastLines(out, 8))
	}
	return nil
}

// Installed reads the on-disk truth — every skill directory under the shared
// root and each agent's own dir, deduplicated by name, with the agents that
// see each — rather than trusting the CLI's output (which varies by version).
func (in *SkillsInstaller) Installed(ctx context.Context) ([]InstalledItem, error) {
	fleet := ScanSkills()
	byName := map[string]*InstalledItem{}
	order := []string{}
	for _, as := range fleet {
		for _, s := range as.Skills {
			it, ok := byName[s.Name]
			if !ok {
				it = &InstalledItem{Name: s.Name, Kind: KindSkill, Detail: s.Description}
				byName[s.Name] = it
				order = append(order, s.Name)
			}
			if len(it.Agents) == 0 {
				it.Agents = []string{}
			}
			agent := as.Agent
			// shared skills: agents were computed in ScanSkills; local
			// ones list just this agent
			if s.Agents != nil {
				it.Agents = mergeAgents(it.Agents, s.Agents)
			} else {
				it.Agents = mergeAgents(it.Agents, []string{agent})
			}
			if s.Status != "ok" && it.Detail != "⚠ "+s.Error {
				if s.Status == "broken" || s.Status == "missing" {
					if it.Detail == "" || it.Detail == s.Description {
						it.Detail = "⚠ " + s.Error
					}
				}
			}
		}
	}
	out := make([]InstalledItem, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func mergeAgents(dst []string, add []string) []string {
	seen := map[string]bool{}
	for _, a := range dst {
		seen[a] = true
	}
	for _, a := range add {
		if !seen[a] {
			dst = append(dst, a)
			seen[a] = true
		}
	}
	return dst
}

// lastLines trims the CLI's spinner/box noise out of error output.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", "\n"), "\n")
	var keep []string
	for _, l := range lines {
		l = strings.Trim(l, "│ ◒◐◓◑∴")
		if strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, " | ")
}
