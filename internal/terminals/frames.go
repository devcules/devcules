package terminals

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Frame types on the link. serve → runner requests end in a verb;
// runner → serve answers correlate by the request's frame id.
// Payloads below share the envelope from internal/link.
const (
	TypeOpen   = "term.open"
	TypeAttach = "term.attach"
	TypeInput  = "term.input"
	TypeResize = "term.resize"
	TypeDetach = "term.detach"
	TypeClose  = "term.close"
	TypeList   = "term.list"
	TypeAgents = "term.agents"
	// TypeBrowse asks for the subdirectories of one directory, for the
	// folder picker that adds a project.
	TypeBrowse = "term.browse"

	TypeOK     = "term.ok"
	TypeError  = "term.error"
	TypeOutput = "term.output"
	TypeExit   = "term.exit"
	TypeListed = "term.listed"
	// TypeAgentList answers term.agents: the agent CLIs on the runner.
	TypeAgentList = "term.agentlist"
	// TypeBrowsed answers term.browse.
	TypeBrowsed = "term.browsed"
)

// MaxDirEntries bounds one directory listing: a picker never needs a
// page of thousands, and the frame stays small.
const MaxDirEntries = 500

// Browse asks for the subdirectories of Path: absolute, or relative to
// the runner's workspace; empty means the workspace itself.
type Browse struct {
	Path string `json:"path,omitempty"`
}

// DirEntry is one subdirectory. Git marks a repository (it holds .git);
// Hidden marks a dot-name, which the picker folds away by default.
type DirEntry struct {
	Name   string `json:"name"`
	Git    bool   `json:"git,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Browsed answers term.browse: the resolved absolute path, its parent
// (empty at the root), the workspace and home for shortcuts, whether
// the directory is itself a repository, and its subdirectories sorted
// by name. Truncated says the listing stopped at MaxDirEntries.
type Browsed struct {
	Path      string     `json:"path"`
	Parent    string     `json:"parent,omitempty"`
	Workspace string     `json:"workspace"`
	Home      string     `json:"home,omitempty"`
	Git       bool       `json:"git,omitempty"`
	Entries   []DirEntry `json:"entries"`
	Truncated bool       `json:"truncated,omitempty"`
}

// Limits. MaxTerminals bounds tmux sessions per runner; dimensions
// keep one attach from requesting an absurd window; MaxInputBytes
// bounds one decoded input write (the envelope adds its own 4 MiB).
const (
	MaxTerminals  = 16
	MinCols       = 1
	MaxCols       = 500
	MinRows       = 1
	MaxRows       = 500
	MaxInputBytes = 64 << 10
)

// idRE accepts the 16 hex chars serve mints. Both ends validate:
// a crafted id must never reach a shell command line.
var idRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidID reports whether id is a serve-minted terminal or stream id.
func ValidID(id string) bool { return idRE.MatchString(id) }

// NewID mints a random 16-hex-character id from crypto/rand.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SessionName maps a terminal id to its tmux session. The prefix keeps
// devcules sessions greppable (`tmux ls | grep ^dvc-`) and collision
// -free against human sessions on a shared box.
func SessionName(id string) string { return "dvc-" + id }

// ValidSize reports whether cols/rows are a sane window: positive and
// bounded, so a browser cannot request a 0x0 or gigapixel canvas.
func ValidSize(cols, rows int) bool {
	return cols >= MinCols && cols <= MaxCols && rows >= MinRows && rows <= MaxRows
}

// Open asks the runner to create the terminal's tmux session. With
// Agent set, the session runs that coding agent instead of a shell.
// Dir starts it in a directory (relative paths resolve against the
// runner's workspace), Project tags it with a project id for the list,
// and Clone (with Dir) first runs `git clone` into Dir in the
// terminal, so its progress and any credential prompt are visible.
type Open struct {
	Term    string       `json:"term"`
	Agent   *AgentLaunch `json:"agent,omitempty"`
	Dir     string       `json:"dir,omitempty"`
	Project string       `json:"project,omitempty"`
	Clone   string       `json:"clone,omitempty"`
	// Name labels the terminal in lists ("api server"); empty shows
	// the agent or the short id.
	Name string `json:"name,omitempty"`
}

// AgentLaunch names the agent to start and carries its per-launch
// token: the runner builds the command, environment and config files
// itself, since only it knows how it reaches serve. The token is never
// logged on either side.
type AgentLaunch struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Model string `json:"model,omitempty"`
	// Provider is the router provider the agent calls; empty is its
	// own. The launch token is minted for this provider.
	Provider string `json:"provider,omitempty"`
}

// AgentInfo describes one agent CLI as the runner sees it. Install is
// the documented install command, shown to the owner, never run.
type AgentInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// Wire is the model API the CLI speaks (WireMessages or
	// WireResponses): serve offers it every provider profile that
	// serves that API.
	Wire string `json:"wire,omitempty"`
	// Only, when set, limits it to these providers: Muse Code only
	// speaks to Meta.
	Only []string `json:"only,omitempty"`
	// Providers are the provider profiles it can use now, its own
	// first. Serve fills it from the saved profiles; a runner from
	// before profiles sends a fixed list here.
	Providers []string `json:"providers,omitempty"`
	Installed bool     `json:"installed"`
	Version   string   `json:"version,omitempty"`
	Install   string   `json:"install,omitempty"`
}

// The model APIs agent CLIs speak.
const (
	// WireMessages is Anthropic's v1/messages (Claude Code).
	WireMessages = "messages"
	// WireResponses is OpenAI's v1/responses (Codex, Muse Code).
	WireResponses = "responses"
)

// AgentList answers term.agents.
type AgentList struct {
	Agents []AgentInfo `json:"agents"`
}

// Attach asks the runner to start an attach client for one browser:
// each browser gets its own stream, so phone and desktop share one
// terminal through separate attaches.
type Attach struct {
	Term   string `json:"term"`
	Stream string `json:"stream"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// Input carries browser keystrokes to the attach client, base64
// because link frames are JSON.
type Input struct {
	Stream string `json:"stream"`
	B64    string `json:"b64"`
}

// Resize forwards a browser window change to the attach client.
type Resize struct {
	Stream string `json:"stream"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// Detach stops one attach client; the tmux session keeps running for
// the next attach.
type Detach struct {
	Stream string `json:"stream"`
}

// Close kills the tmux session and every attach on it.
type Close struct {
	Term string `json:"term"`
}

// Failure answers a request the runner refused. Reason is a fixed
// string chosen at the call site, never command output: stderr can
// hold paths.
type Failure struct {
	Reason string `json:"reason"`
}

// Output streams attach-client bytes to the browser, base64 like
// input. Batched on the runner (32 KiB or 10 ms) so a `cat` of a big
// file does not become a frame per read.
type Output struct {
	Stream string `json:"stream"`
	B64    string `json:"b64"`
}

// Exit marks an attach client that ended (the session was killed, the
// browser detached): the stream is over, the terminal may not be.
type Exit struct {
	Stream string `json:"stream"`
}

// TermInfo describes one tmux session for the list. Agent is the
// agent id when the session runs one.
type TermInfo struct {
	Term    string `json:"term"`
	Created int64  `json:"created"`
	Agent   string `json:"agent,omitempty"`
	Project string `json:"project,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Name    string `json:"name,omitempty"`
}

// Listed answers term.list from the runner's live tmux: serve keeps
// no terminal state of its own, so a serve restart loses nothing.
type Listed struct {
	Terms []TermInfo `json:"terms"`
}

var (
	projectRE = regexp.MustCompile(`^p_[a-z0-9]{1,32}$`)
	// cloneRE admits the URL shapes git clones from: https, ssh and
	// scp-like user@host:path. A leading "-" could pass as a git flag,
	// so the first character is a letter.
	cloneRE   = regexp.MustCompile(`^(https://|ssh://|git@)[A-Za-z0-9][A-Za-z0-9._~:/?#@!$&'()*+,;=%-]{0,500}$`)
	agentIDRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)
	modelRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,99}$`)
	tokenRE   = regexp.MustCompile(`^[!-~]{1,512}$`)
)

// ValidAgentID reports a well-formed agent id ("musecode").
func ValidAgentID(id string) bool { return agentIDRE.MatchString(id) }

// ValidModel reports a well-formed model id: it lands in a config
// file and a command line, so it is a plain token.
func ValidModel(m string) bool { return modelRE.MatchString(m) }

// ValidName reports a terminal label: 1-40 characters, no control
// characters, no surrounding space, and no tab (the runner's list
// separates fields with tabs).
func ValidName(name string) bool {
	if name == "" || len(name) > 40 || strings.TrimSpace(name) != name {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ValidProject reports a project id ("p_" plus lowercase base36).
func ValidProject(id string) bool { return projectRE.MatchString(id) }

// ValidCloneURL reports a git URL a runner may clone from.
func ValidCloneURL(u string) bool { return cloneRE.MatchString(u) }

// ValidDir reports a terminal start directory: an absolute or
// workspace-relative clean path, at most 512 bytes, with no control
// characters and no way out of the workspace through "..".
func ValidDir(dir string) bool {
	if dir == "" || len(dir) > 512 || strings.ContainsFunc(dir, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if path.Clean(dir) != strings.TrimSuffix(dir, "/") && dir != "/" {
		return false
	}
	return !strings.HasPrefix(dir, "..") && !strings.Contains(dir, "/../")
}

// CheckOpen validates term.open.
func CheckOpen(o Open) error {
	if !ValidID(o.Term) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	if o.Dir != "" && !ValidDir(o.Dir) {
		return fmt.Errorf("dir: want a clean absolute or workspace-relative path")
	}
	if o.Project != "" && !ValidProject(o.Project) {
		return fmt.Errorf("project: want p_ and lowercase letters or digits")
	}
	if o.Name != "" && !ValidName(o.Name) {
		return fmt.Errorf("name: want 1-40 characters with no control characters")
	}
	if o.Clone != "" && (o.Dir == "" || !ValidCloneURL(o.Clone)) {
		return fmt.Errorf("clone: want an https, ssh or git@ URL and a dir")
	}
	if a := o.Agent; a != nil {
		if !ValidAgentID(a.ID) {
			return fmt.Errorf("agent: want a lowercase id")
		}
		if !tokenRE.MatchString(a.Token) {
			return fmt.Errorf("agent: want a token")
		}
		if a.Model != "" && !ValidModel(a.Model) {
			return fmt.Errorf("agent: bad model id")
		}
		if a.Provider != "" && !ValidAgentID(a.Provider) {
			return fmt.Errorf("agent: bad provider id")
		}
	}
	return nil
}

// CheckAttach validates term.attach.
func CheckAttach(a Attach) error {
	if !ValidID(a.Term) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	if !ValidID(a.Stream) {
		return fmt.Errorf("stream: want 16 hex chars")
	}
	if !ValidSize(a.Cols, a.Rows) {
		return fmt.Errorf("size: want cols %d-%d, rows %d-%d", MinCols, MaxCols, MinRows, MaxRows)
	}
	return nil
}

// DecodeInput validates term.input and returns the raw bytes: id
// shape plus a 64 KiB decoded ceiling, so a browser cannot wedge the
// runner's writer with one giant frame.
func DecodeInput(in Input) ([]byte, error) {
	if !ValidID(in.Stream) {
		return nil, fmt.Errorf("stream: want 16 hex chars")
	}
	b, err := base64.StdEncoding.DecodeString(in.B64)
	if err != nil {
		return nil, fmt.Errorf("b64: invalid base64")
	}
	if len(b) > MaxInputBytes {
		return nil, fmt.Errorf("b64: decoded %d bytes exceeds %d", len(b), MaxInputBytes)
	}
	return b, nil
}

// CheckResize validates term.resize.
func CheckResize(r Resize) error {
	if !ValidID(r.Stream) {
		return fmt.Errorf("stream: want 16 hex chars")
	}
	if !ValidSize(r.Cols, r.Rows) {
		return fmt.Errorf("size: want cols %d-%d, rows %d-%d", MinCols, MaxCols, MinRows, MaxRows)
	}
	return nil
}

// CheckStream validates stream-only requests (detach).
func CheckStream(stream string) error {
	if !ValidID(stream) {
		return fmt.Errorf("stream: want 16 hex chars")
	}
	return nil
}

// CheckClose validates term.close.
func CheckClose(c Close) error {
	if !ValidID(c.Term) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	return nil
}

// DecodeOutput validates term.output and returns the raw bytes for
// the browser queue. No size ceiling here: the runner batches at
// 32 KiB and the envelope caps the frame at 4 MiB.
func DecodeOutput(o Output) ([]byte, error) {
	if !ValidID(o.Stream) {
		return nil, fmt.Errorf("stream: want 16 hex chars")
	}
	b, err := base64.StdEncoding.DecodeString(o.B64)
	if err != nil {
		return nil, fmt.Errorf("b64: invalid base64")
	}
	return b, nil
}
