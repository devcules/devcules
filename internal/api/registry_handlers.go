package api

// registry_handlers.go — the extensions registry HTTP surface for
// devcules: browse/inspect/install/remove MCP servers and Skills against
// the local coding agents (Claude Code, Codex) on this machine. Routes
// are path-prefixed on the one mux, with cookie-session auth via
// requireSession and writeJSON/writeErr/wantJSON from this package.
//
// The kind-agnostic core lives in internal/registry; this file only
// decodes {kind, body}, picks the right Source/Inspector/Installer, and
// answers JSON. Fleet handlers read the on-disk truth (~/.claude.json,
// ~/.codex/config.toml, ~/.claude/skills, ~/.agents/skills) — never a
// devcules-side record of what was installed.
//
// There is no "explain" route: answering it would need a model
// gateway the registry does not otherwise depend on.
// Devcules' inference goes through internal/router's provider mesh with a
// different call shape, so until the UI needs it, explain answers 409
// with a pointer instead of a half-wired guess.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/registry"
)

// writeErr answers registry failures as JSON with a machine-readable code
// alongside the message (the Extensions UI switches on the code, e.g.
// not_implemented vs registry_unreachable). Distinct from writeAuthError's
// fixed-string shape; registry errors carry upstream detail worth showing.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Quote(code) + `,"message":` + strconv.Quote(msg) + `}}` + "\n"))
}

// wantJSON decodes a bounded JSON body like the rest of the package does
// (MaxBytesReader + Decode); a bad body answers 400 and returns false.
func wantJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

// registryBag bundles the per-kind Source/Inspector/Installer maps one
// Options value carries, so the handlers stay kind-agnostic (a new kind
// is three map entries and nothing here changes).
type registryBag struct {
	sources    map[registry.ArtifactKind][]registry.Source
	inspectors map[registry.ArtifactKind]registry.Inspector
	installers map[registry.ArtifactKind]registry.Installer

	// claudeBin and codexBin are the CLIs the MCP install/remove paths
	// shell out to (default "claude" / "codex" via PATH). Carried here
	// rather than read off the environment per request.
	claudeBin string
	codexBin  string
}

// newRegistryBag wires the default sources and installers: the official MCP registry for MCP, skills.sh for skills. Agents
// stay kind-routed to a 501 until a Source/Installer exists for them.
func newRegistryBag(claudeBin, codexBin string) *registryBag {
	if claudeBin == "" {
		claudeBin = "claude"
	}
	if codexBin == "" {
		codexBin = "codex"
	}
	return &registryBag{
		sources: map[registry.ArtifactKind][]registry.Source{
			registry.KindMCP:   {registry.NewOfficialMCPSource()},
			registry.KindSkill: {&registry.SkillsShSource{}},
		},
		inspectors: map[registry.ArtifactKind]registry.Inspector{
			registry.KindMCP:   &registry.MCPInspector{},
			registry.KindSkill: &registry.SkillsShInspector{},
		},
		installers: map[registry.ArtifactKind]registry.Installer{
			registry.KindMCP:   &registry.MCPInstaller{ClaudeBin: claudeBin},
			registry.KindSkill: &registry.SkillsInstaller{},
		},
		claudeBin: claudeBin,
		codexBin:  codexBin,
	}
}

// registerRegistry mounts the whole surface under one path prefix:
//
//	GET    /api/registry/{kind}/search?q=…
//	POST   /api/registry/{kind}/inspect        {entry, params}
//	POST   /api/registry/{kind}/install        {name, entry, params}
//	GET    /api/registry/{kind}/installed
//	DELETE /api/registry/{kind}/installed/{name}[?harness=…]
//	POST   /api/registry/explain               (409 until wired to the router)
//	GET    /api/skills/fleet
//	GET    /api/mcp/fleet
//
// Everything rides the browser session (requireSession): this is the
// operator managing their own machine's agents, the same trust level as
// the rest of the desk/settings surface. The writes (install, remove)
// shell out to the agent CLIs, so the prefix also carries the CSRF
// origin check every other write route has; it passes GETs untouched.
// A successful install or remove records an audit row: both rewrite
// what the coding agents will execute.
func registerRegistry(mux *http.ServeMux, svc *auth.Service, bag *registryBag, publicURL string, rec auditor) {
	mux.Handle("/api/registry/", csrf(publicURL, requireSession(svc, registryDispatch(bag, rec))))
	mux.Handle("/api/skills/fleet", requireSession(svc, handleSkillsFleet()))
	mux.Handle("/api/mcp/fleet", requireSession(svc, handleMCPFleet(bag)))
}

// registryDispatch routes /api/registry/{kind}/… by method and trailing
// path. Devcules' mux is path-prefix (not the method-pattern form), so
// the kind and the rest of the route come off the URL here instead of
// r.PathValue.
func registryDispatch(bag *registryBag, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/registry/")
		// /api/registry/explain is the one route with no kind in it.
		if rest == "explain" && r.Method == http.MethodPost {
			handleRegistryExplain(w, r)
			return
		}
		kind, tail, _ := strings.Cut(rest, "/")
		k, ok := parseArtifactKind(w, kind)
		if !ok {
			return
		}

		switch {
		case tail == "search" && r.Method == http.MethodGet:
			handleRegistrySearch(w, r, bag, k)
		case tail == "inspect" && r.Method == http.MethodPost:
			handleRegistryInspect(w, r, bag, k)
		case tail == "install" && r.Method == http.MethodPost:
			handleRegistryInstall(w, r, bag, k, rec)
		case tail == "installed" && r.Method == http.MethodGet:
			handleRegistryInstalled(w, r, bag, k)
		case strings.HasPrefix(tail, "installed/") && r.Method == http.MethodDelete:
			name := strings.TrimPrefix(tail, "installed/")
			handleRegistryRemove(w, r, bag, k, name, rec)
		default:
			http.NotFound(w, r)
		}
	}
}

// parseArtifactKind validates the {kind} path segment. Agents are a
// known future kind, so they answer 501 (a deliberate stub the UI can
// show) instead of 404.
func parseArtifactKind(w http.ResponseWriter, seg string) (registry.ArtifactKind, bool) {
	switch registry.ArtifactKind(seg) {
	case registry.KindMCP:
		return registry.KindMCP, true
	case registry.KindSkill:
		return registry.KindSkill, true
	case registry.KindAgent:
		writeErr(w, http.StatusNotImplemented, "not_implemented", "agents are not installable yet")
		return "", false
	default:
		writeErr(w, http.StatusNotFound, "unknown_kind", "unknown kind: "+seg)
		return "", false
	}
}

func handleRegistrySearch(w http.ResponseWriter, r *http.Request, bag *registryBag, kind registry.ArtifactKind) {
	// The upstream sources can take longer than serve's WriteTimeout
	// (skills.sh has a 15 s client, the official MCP registry none), so
	// the results or the 502 would be lost to it (GOSEC-01).
	w = liftWrite(w)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// ?loose=1 skips the sources' relevance filter — the "no strong
	// matches, show the loose list" escape hatch. Everything else filters.
	loose := r.URL.Query().Get("loose") == "1"
	var out []registry.CatalogEntry
	for _, src := range bag.sources[kind] {
		var (
			entries []registry.CatalogEntry
			err     error
		)
		if ls, ok := src.(registry.LooseSearcher); ok && loose {
			entries, err = ls.ListLoose(r.Context(), q)
		} else {
			entries, err = src.List(r.Context(), q)
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, "registry_unreachable", src.ID()+": "+err.Error())
			return
		}
		out = append(out, entries...)
	}
	if out == nil {
		out = []registry.CatalogEntry{}
	}
	writeJSON(w, http.StatusOK, out)
}

// catalogEntryWire is the install payload's entry shape: Install is
// kind-specific raw JSON, decoded into the right spec by the kind branch.
type catalogEntryWire struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	SourceID    string          `json:"source_id"`
	SourceName  string          `json:"source_name"`
	RepoURL     string          `json:"repo_url"`
	Install     json.RawMessage `json:"install"`
}

func decodeCatalogEntry(kind registry.ArtifactKind, wire catalogEntryWire) (registry.CatalogEntry, error) {
	entry := registry.CatalogEntry{
		ID: wire.ID, Kind: kind, Name: wire.Name, Description: wire.Description,
		SourceID: wire.SourceID, SourceName: wire.SourceName, RepoURL: wire.RepoURL,
	}
	switch kind {
	case registry.KindMCP:
		var spec registry.MCPInstallSpec
		if len(wire.Install) > 0 {
			if err := json.Unmarshal(wire.Install, &spec); err != nil {
				return entry, err
			}
		}
		entry.Install = spec
	case registry.KindSkill:
		var spec registry.SkillInstallSpec
		if len(wire.Install) > 0 {
			if err := json.Unmarshal(wire.Install, &spec); err != nil {
				return entry, err
			}
		}
		entry.Install = spec
	}
	return entry, nil
}

// inspectBody is POST inspect/install shared shape: the catalog entry the
// user picked plus any per-install params (env values, etc.).
type inspectBody struct {
	Entry  catalogEntryWire  `json:"entry"`
	Params map[string]string `json:"params"`
	Name   string            `json:"name"` // install only
}

func handleRegistryInspect(w http.ResponseWriter, r *http.Request, bag *registryBag, kind registry.ArtifactKind) {
	ins := bag.inspectors[kind]
	if ins == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", "no inspector for this kind")
		return
	}
	var body inspectBody
	if !wantJSON(w, r, &body) {
		return
	}
	entry, err := decodeCatalogEntry(kind, body.Entry)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_entry", "entry.install: "+err.Error())
		return
	}
	// A dry-run starts the server (often a cold `npx -y`): past 10 s.
	w = liftWrite(w)
	got, err := ins.Inspect(r.Context(), entry)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "inspect_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func handleRegistryInstall(w http.ResponseWriter, r *http.Request, bag *registryBag, kind registry.ArtifactKind, rec auditor) {
	in := bag.installers[kind]
	if in == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", "no installer for this kind")
		return
	}
	var body inspectBody
	if !wantJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	entry, err := decodeCatalogEntry(kind, body.Entry)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_entry", "entry.install: "+err.Error())
		return
	}
	// The agent CLIs (and a cold `npx skills add`) outlive serve's 10 s.
	w = liftWrite(w)
	if err := in.Install(r.Context(), body.Name, entry, body.Params); err != nil {
		writeErr(w, http.StatusInternalServerError, "install_failed", err.Error())
		return
	}
	rec.record(r, audit.RegistryInstall, strings.TrimSpace(body.Name), installDetail(kind, entry))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleRegistryInstalled(w http.ResponseWriter, r *http.Request, bag *registryBag, kind registry.ArtifactKind) {
	in := bag.installers[kind]
	if in == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", "no installer for this kind")
		return
	}
	items, err := in.Installed(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if items == nil {
		items = []registry.InstalledItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleRegistryRemove uninstalls by name. For MCP it also understands
// ?harness= so one harness can drop a server another keeps (both share
// the name in the catalog view).
func handleRegistryRemove(w http.ResponseWriter, r *http.Request, bag *registryBag, kind registry.ArtifactKind, name string, rec auditor) {
	w = liftWrite(w)
	if kind == registry.KindMCP {
		if harness := r.URL.Query().Get("harness"); harness != "" {
			if err := registry.RemoveMCPTargeted(r.Context(), harness, name, bag.claudeBin, bag.codexBin); err != nil {
				writeErr(w, http.StatusInternalServerError, "remove_failed", err.Error())
				return
			}
			rec.record(r, audit.RegistryRemove, name, map[string]string{"kind": string(kind), "harness": harness})
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}
	in := bag.installers[kind]
	if in == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", "no installer for this kind")
		return
	}
	if err := in.Remove(r.Context(), name); err != nil {
		writeErr(w, http.StatusInternalServerError, "remove_failed", err.Error())
		return
	}
	rec.record(r, audit.RegistryRemove, name, map[string]string{"kind": string(kind)})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// installDetail is the audit row's context for an install: what will
// run, never a credential. The row is permanent (hash-chained) and
// shown on the Activity screen, so:
//   - params (env values such as API keys) are never kept;
//   - a stdio command keeps only its program name (a leading
//     `TOKEN=… ` assignment or a path is dropped; anything shell-like
//     records "(unparsed)"), and its arguments are counted, not kept
//     (servers take tokens as flags);
//   - an http/sse URL keeps only scheme and host: hosted endpoints
//     carry the key in the path (…/mcp/<key>/sse) as well as in the
//     query or userinfo;
//   - a skill repo given as a URL loses its userinfo, query and
//     fragment (the owner/repo path stays).
func installDetail(kind registry.ArtifactKind, entry registry.CatalogEntry) map[string]string {
	d := map[string]string{"kind": string(kind)}
	if entry.ID != "" {
		d["entry"] = entry.ID
	}
	switch spec := entry.Install.(type) {
	case registry.MCPInstallSpec:
		d["type"] = spec.Type
		if spec.Command != "" {
			if prog := programName(spec.Command); prog != "" {
				d["command"] = prog
			}
			d["args"] = strconv.Itoa(len(spec.Args))
		}
		if spec.URL != "" {
			d["url"] = urlOrigin(spec.URL)
		}
	case registry.SkillInstallSpec:
		d["repo"] = scrubRepo(spec.Repo)
		if spec.Skill != "" {
			d["skill"] = spec.Skill
		}
	}
	return d
}

// programName is the base name of the program a command line runs,
// or "(unparsed)". It does not emulate a shell: it accepts only the
// plain form `[NAME=value ...] program [args]` and refuses anything
// else, because the row is permanent and must never hold a credential.
//   - Any shell metacharacter, quote, backslash or line break before the
//     program token gives "(unparsed)": `$`, a backtick, `(`, `)`, `;`,
//     `|`, `&`, `<`, `>`, `'`, `"`, `\`, CR, LF. Those are the forms
//     (quoting, escapes, `$(…)`, `$'…'`, `${…}`, separators) where a
//     shell would split differently from a plain whitespace split.
//   - Otherwise the command is split on spaces and tabs, and leading
//     NAME=value fields (NAME an identifier) are skipped.
//   - The program token must be [A-Za-z0-9._:/\-]+, else "(unparsed)".
//   - Its base name is taken on both / and \, so `C:\tools\uvx.exe`
//     gives `uvx.exe` (claude/codex take the command literally, and the
//     server is built for Windows too).
//
// A blank command gives "" (no command key in the row).
func programName(command string) string {
	rest := command
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return ""
		}
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			end = len(rest)
		}
		field := rest[:end]
		if assignmentField.MatchString(field) && !strings.ContainsAny(field, prefixUnsafe) {
			rest = rest[end:]
			continue
		}
		if strings.ContainsAny(command[:len(command)-len(rest)], prefixUnsafe) || !programToken.MatchString(field) {
			return "(unparsed)"
		}
		if i := strings.LastIndexAny(field, `/\`); i >= 0 {
			field = field[i+1:]
		}
		if field == "" {
			return "(unparsed)"
		}
		return field
	}
}

// prefixUnsafe are the bytes that make a command prefix shell syntax
// rather than plain words (see programName).
const prefixUnsafe = "$`();|&<>'\"\\\r\n"

var (
	assignmentField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	programToken    = regexp.MustCompile(`^[A-Za-z0-9._:/\\-]+$`)
)

// urlOrigin keeps scheme and host only.
func urlOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(unparsed)"
	}
	return u.Scheme + "://" + u.Host
}

// scrubRepo drops userinfo, query and fragment from a repo given as a
// URL, including a scheme-relative `//user:token@host/path`. A scp-style `user:token@host:path` loses the part up to the @
// (userinfo has no slash, which keeps the `owner/repo@skill` form whole).
// The owner/repo shorthand passes as is.
func scrubRepo(repo string) string {
	if u, err := url.Parse(repo); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
		return u.String()
	}
	if i := strings.IndexByte(repo, '@'); i >= 0 && !strings.Contains(repo[:i], "/") {
		repo = repo[i+1:]
	}
	if i := strings.IndexAny(repo, "?#"); i >= 0 {
		repo = repo[:i]
	}
	return repo
}

// handleSkillsFleet answers the on-disk truth about skills across the
// local coding agents (each agent's skills dir plus the shared
// ~/.agents/skills), including broken ones.
func handleSkillsFleet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fleet := registry.ScanSkills()
		if fleet == nil {
			fleet = []registry.AgentSkills{}
		}
		writeJSON(w, http.StatusOK, fleet)
	}
}

// handleMCPFleet merges each harness's configured MCP servers into one
// list with the harnesses each is installed in — the "various MCP
// servers on various coding agents" view.
func handleMCPFleet(bag *registryBag) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, registry.ScanMCPFleet(r.Context(), bag.codexBin))
	}
}

// handleRegistryExplain is deliberately not wired to an inference backend
// yet (see the file header). It answers a clean 409 so the UI can show
// "not available here" instead of a 404 that implies a broken route.
func handleRegistryExplain(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusConflict, "no_inference_backend", "registry explain is not wired to an inference backend in devcules yet")
}
