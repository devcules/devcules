package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/store"
)

// assistantsRoot serves GET /api/assistants (list) and the per-assistant
// tree below /api/assistants/{id}/... : POST chat (start or continue a
// conversation) and GET a conversation's messages. The handlers take
// the assistants.Registry serve wires; a missing registry answers 503,
// like the other optional subsystems.
type Assistants struct {
	reg *assistants.Registry
}

// NewAssistants wires the handler over the registry serve built.
// reg may be nil (the routes then answer 503).
func NewAssistants(reg *assistants.Registry) *Assistants {
	return &Assistants{reg: reg}
}

// handler guards the whole tree: an unconfigured assistant registry
// answers 503 the way a nil MCP or relay does.
func (h *Assistants) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.reg == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "assistants not configured")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// root serves GET /api/assistants: the ids and display names the
// picker shows.
func (h *Assistants) root() http.Handler {
	return h.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		list := h.reg.List()
		out := make([]assistantView, 0, len(list))
		for _, a := range list {
			out = append(out, assistantView{ID: a.ID(), Name: assistantName(a.ID())})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		writeJSON(w, http.StatusOK, struct {
			Assistants []assistantView `json:"assistants"`
		}{out})
	}))
}

type assistantView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// one serves /api/assistants/{id}/... : POST .../chat and GET
// .../conversations/{sess}.
func (h *Assistants) one() http.Handler {
	return h.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a, ok := h.reg.Get(id)
		if !ok {
			writeAuthError(w, http.StatusNotFound, "no such assistant")
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/assistants/"+id+"/chat":
			h.chat(w, r, a)
		case r.Method == http.MethodGet && r.PathValue("sess") != "":
			h.read(w, r, a)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// chat runs one turn and answers with the buffered events in order,
// the session id, and the assistant id; by the time it returns the
// model and the gate have fully played out. A redaction block answers
// 422 so the UI can show the blocked-kinds message as typed.
func (h *Assistants) chat(w http.ResponseWriter, r *http.Request, a assistants.Assistant) {
	var in struct {
		Message string `json:"message"`
		Session string `json:"session"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || in.Message == "" {
		writeAuthError(w, http.StatusBadRequest, "a message is required")
		return
	}
	evs, err := a.Chat(r.Context(), in.Session, in.Message)
	if err != nil {
		switch {
		default:
			writeAuthError(w, http.StatusBadGateway, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeAuthError(w, http.StatusNotFound, "no such conversation")
		}
		return
	}
	sess := in.Session
	if sess == "" {
		sess = h.freshOf(a)
	}
	writeJSON(w, http.StatusOK, struct {
		Session   string             `json:"session"`
		Assistant string             `json:"assistant"`
		Events    []assistants.Event `json:"events"`
	}{sess, a.ID(), evs})
}

// freshOf resolves the conversation a fresh chat minted. The Assistants
// we are built over keep the loop's FreshChat when their concrete type
// has it (the built-in does).
func (h *Assistants) freshOf(a assistants.Assistant) string {
	type fresher interface{ FreshChat() string }
	if f, ok := a.(fresher); ok {
		if id := f.FreshChat(); id != "" {
			return id
		}
	}
	return ""
}

// read returns one conversation's messages (GET .../conversations/{sess}).
func (h *Assistants) read(w http.ResponseWriter, r *http.Request, a assistants.Assistant) {
	sess, err := a.Session(r.PathValue("sess"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAuthError(w, http.StatusNotFound, "no such conversation")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// assistantName makes an id readable in the picker.
func assistantName(id string) string {
	switch id {
	case "builtin":
		return "Devcules (built in)"
	case "hermes":
		return "Hermes"
	case "openclaw":
		return "OpenClaw"
	}
	return id
}
