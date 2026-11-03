package relay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/terminals"
)

// runCallsRunner is a fake runner for the request/reply calls the
// stream tests do not touch: agents, browse, rename and agent opens.
func runCallsRunner(t *testing.T, s *linktest.Server, ctx context.Context, name string) chan terminals.Open {
	t.Helper()
	child, stop := context.WithCancel(ctx)
	c := dial.New("ws://pipe/link", name, "test", relayTestToken, dial.Options{
		PingInterval: 30 * time.Second, PingTimeout: 30 * time.Second,
		MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
		HTTPClient: s.Client(), Logger: discardLogger(),
	})
	opened := make(chan terminals.Open, 4)
	reply := func(req link.Frame, typ string, v any) {
		data, _ := json.Marshal(v)
		c.Send(link.Frame{V: link.Version, Type: typ, ID: req.ID, Data: data})
	}
	c.On(terminals.TypeOpen, func(_ context.Context, req link.Frame) {
		var o terminals.Open
		_ = json.Unmarshal(req.Data, &o)
		opened <- o
		answerOK(c, req)
	})
	c.On(terminals.TypeRename, func(_ context.Context, req link.Frame) { answerOK(c, req) })
	c.On(terminals.TypeAgents, func(_ context.Context, req link.Frame) {
		reply(req, terminals.TypeAgentList, terminals.AgentList{Agents: []terminals.AgentInfo{{ID: "codex", Name: "Codex", Installed: true}}})
	})
	c.On(terminals.TypeBrowse, func(_ context.Context, req link.Frame) {
		var b terminals.Browse
		_ = json.Unmarshal(req.Data, &b)
		switch b.Path {
		case "/gone":
			reply(req, terminals.TypeError, terminals.Failure{Reason: "directory not found"})
		case "/locked":
			reply(req, terminals.TypeError, terminals.Failure{Reason: "permission denied"})
		case "/odd":
			reply(req, terminals.TypeOK, struct{}{})
		case "/garbled":
			c.Send(link.Frame{V: link.Version, Type: terminals.TypeBrowsed, ID: req.ID, Data: json.RawMessage(`{"path":"/x","extra":1}`)})
		default:
			reply(req, terminals.TypeBrowsed, terminals.Browsed{Path: "/srv"})
		}
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		<-done
	})
	for deadline := time.Now().Add(10 * time.Second); !c.Linked() && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	return opened
}

func TestRequestCalls(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	opened := runCallsRunner(t, s, ctx, "box")
	waitLinked(t, h, 1)

	if runners, _ := r.Links(); len(runners) != 1 || runners[0] != "box" {
		t.Fatalf("Links = %v", runners)
	}

	id, rerr := r.OpenAgentAt(ctx, "box", terminals.AgentLaunch{ID: "codex", Token: "dvl_tok", Provider: "openai"}, Place{Dir: "/srv/shop", Project: "p_shop", Name: "review"})
	if rerr != nil || !terminals.ValidID(id) {
		t.Fatalf("OpenAgentAt = %q %+v", id, rerr)
	}
	o := <-opened
	if o.Agent == nil || o.Agent.ID != "codex" || o.Dir != "/srv/shop" || o.Project != "p_shop" || o.Name != "review" {
		t.Fatalf("open frame = %+v", o)
	}
	if _, rerr := r.OpenAgent(ctx, "box", terminals.AgentLaunch{ID: "Bad Id"}); rerr == nil || rerr.Status != 400 {
		t.Fatalf("a bad agent launch = %+v, want 400", rerr)
	}

	list, rerr := r.Agents(ctx, "box")
	if rerr != nil || len(list) != 1 || list[0].ID != "codex" {
		t.Fatalf("Agents = %+v %+v", list, rerr)
	}

	got, rerr := r.Browse(ctx, "box", "")
	if rerr != nil || got.Path != "/srv" || got.Entries == nil {
		t.Fatalf("Browse = %+v %+v, want the path and a non-nil list", got, rerr)
	}
	for dir, want := range map[string]int{"/gone": 404, "/locked": 502, "/odd": 502, "/garbled": 502, "../up": 400} {
		if _, rerr := r.Browse(ctx, "box", dir); rerr == nil || rerr.Status != want {
			t.Errorf("Browse(%q) = %+v, want %d", dir, rerr, want)
		}
	}

	if rerr := r.Rename(ctx, "box", id, "api server"); rerr != nil {
		t.Fatalf("Rename = %+v", rerr)
	}
	if rerr := r.Rename(ctx, "box", "nothex", "x"); rerr == nil || rerr.Status != 400 {
		t.Fatalf("bad id = %+v", rerr)
	}
	if rerr := r.Rename(ctx, "box", id, "bad\tname"); rerr == nil || rerr.Status != 400 {
		t.Fatalf("bad name = %+v", rerr)
	}

	// Every call needs a live runner.
	for name, call := range map[string]func() *Error{
		"agents": func() *Error { _, e := r.Agents(ctx, "nope"); return e },
		"browse": func() *Error { _, e := r.Browse(ctx, "nope", ""); return e },
		"rename": func() *Error { return r.Rename(ctx, "nope", id, "x") },
		"agent": func() *Error {
			_, e := r.OpenAgent(ctx, "nope", terminals.AgentLaunch{ID: "codex", Token: "t"})
			return e
		},
	} {
		if e := call(); e == nil {
			t.Errorf("%s on an unknown runner succeeded", name)
		}
	}
}
