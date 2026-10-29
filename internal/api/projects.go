package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/machines"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/relay"
)

// linkTarget reads which link a terminal call is for: ?machine=<name>
// (a machine's shell, addressed by its hub address) or
// ?runner=<name> (empty picks the only runner).
func linkTarget(r *http.Request) string {
	q := r.URL.Query()
	if m := q.Get("machine"); m != "" {
		if !machines.ValidName(m) {
			return hub.MachineAddr("-invalid-")
		}
		return hub.MachineAddr(m)
	}
	return q.Get("runner")
}

// projectTarget is the relay address of a project's runner or machine.
func projectTarget(p store.Project) string {
	if p.Kind == "machine" {
		return hub.MachineAddr(p.Target)
	}
	return p.Target
}

var projectNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$`)

// projectJSON is a project as the Projects tab sees it.
type projectJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Dir       string `json:"dir"`
	Repo      string `json:"repo,omitempty"`
	CreatedAt string `json:"createdAt"`
}

func toProjectJSON(p store.Project) projectJSON {
	return projectJSON{ID: p.ID, Name: p.Name, Kind: p.Kind, Target: p.Target, Dir: p.Dir, Repo: p.Repo, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
}

// newProjectID is "p_" plus 20 lowercase base36 characters.
func newProjectID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	for i, v := range b {
		b[i] = alphabet[int(v)%len(alphabet)]
	}
	return "p_" + string(b)
}

// repoDir is where a clone lands when no directory is given: the
// repository's base name without ".git", inside the workspace.
func repoDir(repo string) string {
	base := repo
	if i := strings.LastIndexAny(base, "/:"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".git")
	if base == "" || base == "." || base == ".." {
		return ""
	}
	return base
}

// projectsRoot serves /api/projects: GET lists them, POST adds one
// (session plus CSRF). Adding with a repo clones it at once in a new
// terminal of the project, so progress and any credential prompt are
// visible; adding a directory just records it.
func projectsRoot(repo store.ProjectsRepo, rl *relay.Relay, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "projects not configured")
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, err := repo.List(r.Context())
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "project list failed")
				return
			}
			out := []projectJSON{}
			for _, p := range list {
				out = append(out, toProjectJSON(p))
			}
			// The live runners and machines ride along so the add form
			// can offer them.
			runners, machs := []string{}, []string{}
			if rl != nil {
				runners, machs = rl.Links()
			}
			writeJSON(w, http.StatusOK, map[string]any{"projects": out, "runners": runners, "machines": machs})
		case http.MethodPost:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				addProject(w, r, repo, rl, rec)
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func addProject(w http.ResponseWriter, r *http.Request, repo store.ProjectsRepo, rl *relay.Relay, rec auditor) {
	var body struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Dir    string `json:"dir"`
		Repo   string `json:"repo"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, `body must be {"name", "kind": "runner"|"machine", "target", "dir", "repo"}`)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Dir = strings.TrimSuffix(strings.TrimSpace(body.Dir), "/")
	body.Repo = strings.TrimSpace(body.Repo)
	if body.Dir == "" && body.Repo != "" {
		body.Dir = repoDir(body.Repo)
	}
	if body.Name == "" {
		body.Name = path.Base(body.Dir)
	}
	switch {
	case !projectNameRE.MatchString(body.Name):
		writeAuthError(w, http.StatusBadRequest, "name: letters, digits, space, '.', '_' or '-', up to 64")
		return
	case body.Kind != "runner" && body.Kind != "machine":
		writeAuthError(w, http.StatusBadRequest, `kind: want "runner" or "machine"`)
		return
	case !machines.ValidName(body.Target):
		writeAuthError(w, http.StatusBadRequest, "target: want the runner or machine name")
		return
	case body.Repo != "" && !terminals.ValidCloneURL(body.Repo):
		writeAuthError(w, http.StatusBadRequest, "repo: want an https://, ssh:// or git@host:path URL")
		return
	case !terminals.ValidDir(body.Dir):
		writeAuthError(w, http.StatusBadRequest, "dir: want an absolute path, or one inside the workspace")
		return
	}
	p := store.Project{ID: newProjectID(), Name: body.Name, Kind: body.Kind, Target: body.Target, Dir: body.Dir, Repo: body.Repo, CreatedAt: time.Now().UTC()}
	// A clone needs the target online, and an open that fails should
	// not leave a half-made project behind: open first, record second.
	termID := ""
	if p.Repo != "" {
		if rl == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "no runner linked")
			return
		}
		id, rerr := rl.OpenAt(r.Context(), projectTarget(p), relay.Place{Dir: p.Dir, Project: p.ID, Clone: p.Repo})
		if rerr != nil {
			writeAuthError(w, rerr.Status, rerr.Msg)
			return
		}
		termID = id
		rec.record(r, audit.TerminalOpen, id, map[string]string{"project": p.ID})
	}
	if err := repo.Create(r.Context(), p); err != nil {
		if errors.Is(err, store.ErrExists) {
			writeAuthError(w, http.StatusConflict, "a project with this name exists")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "project create failed")
		return
	}
	rec.record(r, audit.ProjectAdd, p.ID, map[string]string{"name": p.Name, "kind": p.Kind, "target": p.Target, "dir": p.Dir, "repo": p.Repo})
	writeJSON(w, http.StatusCreated, map[string]any{"project": toProjectJSON(p), "terminal": termID})
}

// projectOne serves /api/projects/{id}: DELETE forgets the project
// (its files and terminals stay where they are), and POST
// /api/projects/{id}/terminals opens a shell in its directory.
func projectOne(repo store.ProjectsRepo, rl *relay.Relay, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "projects not configured")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		id, sub, _ := strings.Cut(rest, "/")
		if !terminals.ValidProject(id) || (sub != "" && sub != "terminals") {
			writeAuthError(w, http.StatusNotFound, "unknown project")
			return
		}
		p, err := repo.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeAuthError(w, http.StatusNotFound, "unknown project")
				return
			}
			writeAuthError(w, http.StatusInternalServerError, "project read failed")
			return
		}
		if sub == "" {
			if !requireMethod(w, r, http.MethodDelete) {
				return
			}
			if err := repo.Delete(r.Context(), id); err != nil {
				writeAuthError(w, http.StatusInternalServerError, "project delete failed")
				return
			}
			rec.record(r, audit.ProjectRemove, id, map[string]string{"name": p.Name})
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		if rl == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "no runner linked")
			return
		}
		// Reopening a cloned project whose checkout went missing clones
		// again; the runner skips the clone when the directory exists.
		termID, rerr := rl.OpenAt(r.Context(), projectTarget(p), relay.Place{Dir: p.Dir, Project: p.ID, Clone: p.Repo})
		if rerr != nil {
			writeAuthError(w, rerr.Status, rerr.Msg)
			return
		}
		rec.record(r, audit.TerminalOpen, termID, map[string]string{"project": p.ID})
		writeJSON(w, http.StatusCreated, map[string]string{"id": termID})
	})
}
