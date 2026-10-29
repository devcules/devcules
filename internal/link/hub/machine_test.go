package hub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
)

func machineHub(keys hub.MachineVerifier) *hub.Hub {
	return hub.New(hub.HashToken(testToken), hub.Options{
		HelloTimeout: 500 * time.Millisecond,
		PingInterval: 30 * time.Second,
		PingTimeout:  5 * time.Second,
		Machines:     keys,
	})
}

func machineHeader() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Machine")
	return h
}

// machineUp dials as a machine, hellos and answers the challenge with
// sign, returning the conn and the frame serve sent after the answer.
func machineUp(t *testing.T, s *linktest.Server, name string, sign func([]byte) []byte) (*websocket.Conn, link.Frame, error) {
	t.Helper()
	conn := dialRaw(t, s, machineHeader())
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "machine", name)); err != nil {
		t.Fatalf("write hello: %s", err)
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read challenge: %s", err)
	}
	f, err := link.Decode(raw)
	if err != nil || f.Type != "challenge" {
		t.Fatalf("first frame = %q (%v), want challenge", f.Type, err)
	}
	var ch hub.Challenge
	if err := json.Unmarshal(f.Data, &ch); err != nil {
		t.Fatalf("challenge data: %s", err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(ch.Nonce)
	if err != nil || len(nonce) != 32 {
		t.Fatalf("nonce = %q, want 32 base64url bytes", ch.Nonce)
	}
	data, _ := json.Marshal(hub.ChallengeAnswer{Sig: base64.StdEncoding.EncodeToString(sign(nonce))})
	b, _ := link.Encode(link.Frame{V: link.Version, Type: "challenge.answer", ID: link.NewID(), Data: data})
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write answer: %s", err)
	}
	_, raw, err = conn.Read(ctx)
	if err != nil {
		return conn, link.Frame{}, err
	}
	next, err := link.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %s", err)
	}
	return conn, next, nil
}

func TestMachineSignsChallenge(t *testing.T) {
	t.Parallel()
	keys := linktest.NewMachineKeys()
	sign := keys.Add("laptop")
	h := machineHub(keys)
	s := linktest.Serve(t, h.Handler())
	conn, welcome, err := machineUp(t, s, "laptop", sign)
	if err != nil {
		t.Fatalf("link: %s", err)
	}
	defer conn.CloseNow()
	if welcome.Type != "welcome" {
		t.Fatalf("after answer = %q, want welcome", welcome.Type)
	}
	pollCount(t, h, 1)
	if got := h.Machines(); len(got) != 1 || got[0] != "laptop" {
		t.Fatalf("Machines() = %v, want [laptop]", got)
	}
	if got := h.Runners(); len(got) != 0 {
		t.Fatalf("Runners() = %v, want none", got)
	}
}

// A machine and a runner on one computer share the hostname; neither
// may replace the other.
func TestMachineAndRunnerShareName(t *testing.T) {
	t.Parallel()
	keys := linktest.NewMachineKeys()
	sign := keys.Add("mac")
	h := machineHub(keys)
	s := linktest.Serve(t, h.Handler())
	rc, _ := linkUp(t, s, "mac")
	defer rc.CloseNow()
	mc, welcome, err := machineUp(t, s, "mac", sign)
	if err != nil || welcome.Type != "welcome" {
		t.Fatalf("machine link: %v %q", err, welcome.Type)
	}
	defer mc.CloseNow()
	pollCount(t, h, 2)
	if got := h.Runners(); len(got) != 1 || got[0] != "mac" {
		t.Fatalf("Runners() = %v, want [mac]", got)
	}
	if got := h.Machines(); len(got) != 1 || got[0] != "mac" {
		t.Fatalf("Machines() = %v, want [mac]", got)
	}
	f := link.Frame{V: link.Version, Type: "term.list", ID: link.NewID(), Data: json.RawMessage(`{}`)}
	// The pipe is synchronous: the machine must be reading before the
	// hub's write can complete.
	got := make(chan string, 1)
	go func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, raw, err := mc.Read(ctx)
		if err != nil {
			got <- "read error: " + err.Error()
			return
		}
		fr, _ := link.Decode(raw)
		got <- fr.ID
	}()
	if err := h.SendTo(hub.MachineAddr("mac"), f); err != nil {
		t.Fatalf("SendTo machine: %s", err)
	}
	if id := <-got; id != f.ID {
		t.Fatalf("machine got %q, want the frame sent to its address", id)
	}
}

func TestMachineAuthRejections(t *testing.T) {
	t.Parallel()
	keys := linktest.NewMachineKeys()
	keys.Add("laptop")
	other := linktest.NewMachineKeys().Add("laptop")
	h := machineHub(keys)
	s := linktest.Serve(t, h.Handler())

	t.Run("wrong key", func(t *testing.T) {
		_, _, err := machineUp(t, s, "laptop", other)
		code, reason := closeReason(t, err)
		if code != websocket.StatusPolicyViolation || reason != "machine auth failed" {
			t.Fatalf("close = %d %q, want 1008 machine auth failed", code, reason)
		}
	})
	t.Run("not enrolled", func(t *testing.T) {
		_, _, err := machineUp(t, s, "ghost", other)
		code, _ := closeReason(t, err)
		if code != websocket.StatusPolicyViolation {
			t.Fatalf("close = %d, want 1008", code)
		}
	})
	t.Run("runner token claims machine", func(t *testing.T) {
		conn := dialRaw(t, s, authHeader())
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "machine", "laptop")); err != nil {
			t.Fatalf("write hello: %s", err)
		}
		_, _, err := conn.Read(ctx)
		code, reason := closeReason(t, err)
		if code != websocket.StatusPolicyViolation || reason != "machines sign in with their key, not the runner token" {
			t.Fatalf("close = %d %q", code, reason)
		}
	})
	t.Run("machine header claims runner", func(t *testing.T) {
		conn := dialRaw(t, s, machineHeader())
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "runner", "laptop")); err != nil {
			t.Fatalf("write hello: %s", err)
		}
		_, _, err := conn.Read(ctx)
		code, _ := closeReason(t, err)
		if code != websocket.StatusPolicyViolation {
			t.Fatalf("close = %d, want 1008", code)
		}
	})
	t.Run("silent machine", func(t *testing.T) {
		conn := dialRaw(t, s, machineHeader())
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "machine", "laptop")); err != nil {
			t.Fatalf("write hello: %s", err)
		}
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatalf("read challenge: %s", err)
		}
		_, _, err := conn.Read(ctx)
		code, reason := closeReason(t, err)
		if code != websocket.StatusPolicyViolation || reason != "machine auth failed" {
			t.Fatalf("close = %d %q, want 1008 machine auth failed", code, reason)
		}
	})
	if n := h.Count(); n != 0 {
		t.Fatalf("Count() = %d after rejections, want 0", n)
	}
}

// A hub built without a verifier refuses machines before the upgrade.
func TestMachineWithoutVerifier(t *testing.T) {
	t.Parallel()
	s := linktest.Serve(t, calmHub().Handler())
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, resp, err := websocket.Dial(ctx, "ws://pipe/link", &websocket.DialOptions{HTTPClient: s.Client(), HTTPHeader: machineHeader()})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial = %v, want 401", err)
	}
}

func TestNameWithSlashRejected(t *testing.T) {
	t.Parallel()
	h := calmHub()
	s := linktest.Serve(t, h.Handler())
	conn := dialRaw(t, s, authHeader())
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "runner", "machine/x")); err != nil {
		t.Fatalf("write hello: %s", err)
	}
	_, _, err := conn.Read(ctx)
	code, reason := closeReason(t, err)
	if code != websocket.StatusPolicyViolation || reason != "no hello" {
		t.Fatalf("close = %d %q, want 1008 no hello", code, reason)
	}
}
