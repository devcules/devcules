package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// skill_source_skills_sh.go — the skills.sh catalog as a Source: the same
// browse → inspect → install flow the MCP registry has, for Skills, with
// skills.sh as the canonical index (it is where the `skills` CLI searches
// too). Anyone can publish there, exactly like the MCP registry — the page
// keeps the same "not vetted, inspect first" stance.

// SkillInstallSpec is the kind-specific Install payload for skills.
type SkillInstallSpec struct {
	// Repo is owner/repo on GitHub ("mattpocock/skills").
	Repo string `json:"repo"`
	// Skill is the skill's folder name in the repo ("code-review"), "" = repo root.
	Skill string `json:"skill,omitempty"`
	// Installs is the skills.sh install count, carried for sorting/context.
	Installs int `json:"installs,omitempty"`
}

// SkillsShSource searches https://skills.sh (the catalog the `skills` CLI
// itself searches) for installable skills.
type SkillsShSource struct {
	// BaseURL overrides https://skills.sh (tests).
	BaseURL string
	Client  *http.Client
}

func (s *SkillsShSource) ID() string         { return "skills-sh" }
func (s *SkillsShSource) Kind() ArtifactKind { return KindSkill }
func (s *SkillsShSource) Name() string       { return "skills.sh" }

func (s *SkillsShSource) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return "https://skills.sh"
}

func (s *SkillsShSource) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// List searches skills.sh. skills.sh rejects queries under 2 characters
// (400 "Query must be at least 2 characters"), so an empty query searches
// the universal term "skill": the fuzzy index answers it with the most
// installed entries, which is the popular listing an empty search means.
func (s *SkillsShSource) List(ctx context.Context, query string) ([]CatalogEntry, error) {
	out, err := s.list(ctx, query)
	if err != nil {
		return nil, err
	}
	// skills.sh's fuzzy index pads a page with merely-related skills (a
	// "pdf" query returned docx and canvas-design), so the same local
	// relevance rule the MCP source uses applies here. The empty-query
	// fallback searches "skill", whose results are a popularity list
	// rather than a term match — leave those unfiltered.
	if strings.TrimSpace(query) == "" {
		return out, nil
	}
	return FilterRelevant(out, query), nil
}

// ListLoose answers the same query without the relevance filter.
func (s *SkillsShSource) ListLoose(ctx context.Context, query string) ([]CatalogEntry, error) {
	return s.list(ctx, query)
}

func (s *SkillsShSource) list(ctx context.Context, query string) ([]CatalogEntry, error) {
	u, err := url.Parse(s.base() + "/api/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if strings.TrimSpace(query) == "" {
		q.Set("q", "skill")
	} else {
		q.Set("q", query)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("skills.sh unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("skills.sh returned %d", resp.StatusCode)
	}
	var doc struct {
		Skills []struct {
			ID       string `json:"id"` // "owner/repo/skill"
			Source   string `json:"source"`
			SkillID  string `json:"skillId"`
			Name     string `json:"name"`
			Installs int    `json:"installs"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("skills.sh response: %w", err)
	}
	out := make([]CatalogEntry, 0, len(doc.Skills))
	for _, sk := range doc.Skills {
		// id is "source/skillId" where source is "owner/repo"
		repo := sk.Source
		skill := sk.SkillID
		if repo == "" || skill == "" {
			continue
		}
		out = append(out, CatalogEntry{
			ID:          "skills-sh:" + sk.ID,
			Kind:        KindSkill,
			Name:        sk.Name,
			Description: fmt.Sprintf("%s — from %s on skills.sh (%s installs)", sk.Name, repo, humanCount(sk.Installs)),
			SourceID:    s.ID(),
			SourceName:  "skills.sh",
			RepoURL:     "https://github.com/" + repo,
			Install:     SkillInstallSpec{Repo: repo, Skill: skill, Installs: sk.Installs},
		})
	}
	return out, nil
}

// skillFileFromRepo fetches a skill's SKILL.md

func humanCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%dk", n/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// SkillsShInspector previews a skill by fetching its SKILL.md from GitHub
// (raw.githubusercontent.com) before install — the same "show the real thing,
// not marketing text" stance as the MCP inspector running the server.
type SkillsShInspector struct {
	Client *http.Client
}

func (in *SkillsShInspector) Kind() ArtifactKind { return KindSkill }

func (in *SkillsShInspector) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Inspect fetches the skill's SKILL.md from the repo so the user reads the
// actual instructions the agent will receive — frontmatter description,
// body outline — before anything is installed.
func (in *SkillsShInspector) Inspect(ctx context.Context, entry CatalogEntry) (Inspection, error) {
	spec, ok := entry.Install.(SkillInstallSpec)
	if !ok {
		return Inspection{}, fmt.Errorf("entry has no skill install spec")
	}
	name, desc, body, err := in.fetchSkillMD(ctx, spec)
	if err != nil {
		return Inspection{Warning: err.Error()}, nil // can't preview ≠ can't browse
	}
	summary := "Skill " + name + " from " + spec.Repo
	if desc != "" {
		summary += ": " + desc
	}
	out := Inspection{Summary: summary, Content: body}
	if len(body) > 4000 {
		out.Content = body[:4000] + "\n…(truncated)"
	}
	return out, nil
}

func (in *SkillsShInspector) fetchSkillMD(ctx context.Context, spec SkillInstallSpec) (name, desc, body string, err error) {
	paths := []string{}
	if spec.Skill != "" {
		paths = append(paths, spec.Skill+"/SKILL.md", "skills/"+spec.Skill+"/SKILL.md")
	} else {
		paths = append(paths, "SKILL.md", "skills/SKILL.md")
	}
	var b []byte
	var lastErr error
	for _, p := range paths {
		u := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", spec.Repo, p)
		req, reqErr := http.NewRequestWithContext(ctx, "GET", u, nil)
		if reqErr != nil {
			continue
		}
		resp, respErr := in.client().Do(req)
		if respErr != nil {
			lastErr = respErr
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = fmt.Errorf("github: %s %s", strings.ToLower(http.StatusText(resp.StatusCode)), u)
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		b, err = raw, nil
		break
	}
	if len(b) == 0 {
		if lastErr == nil {
			lastErr = errors.New("SKILL.md not found in repo")
		}
		return "", "", "", lastErr
	}
	n, d, parseErr := parseSkillHeader(b)
	if parseErr != nil {
		return "", "", string(b), nil // body is still useful without frontmatter
	}
	return n, d, string(b), nil
}

// skillHeaderForSpec re-uses the fleet scanner's parser for the inspector.
var _ = parseSkillHeader
