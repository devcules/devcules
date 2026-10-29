package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/api"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/machines"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/web"
)

// TestMachineEnrol drives `devcules machine enrol` against serve's
// real API over an in-memory pipe: the key is made once, the code is
// spent, and the identity lands next to the key.
func TestMachineEnrol(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "serve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	iss := machines.NewIssuer(s)
	svc := machines.NewService(s)
	h := hub.New(hub.HashToken("t"), hub.Options{Machines: svc})
	mux := api.Handler(api.Options{Store: s, Auth: auth.New(s), Web: web.Embedded(), Enrol: iss, MachineStore: svc, Machines: machrelay.New(h)})
	pipe := linktest.Serve(t, mux)

	cfg := config.Config{DataDir: filepath.Join(t.TempDir(), "machine"), ServeURL: "http://pipe"}
	code, err := iss.Mint(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := runMachineEnrol(cfg, strings.ToLower(code), "lab-top", &stdout, &stderr, pipe.Client()); got != exitOK {
		t.Fatalf("enrol exit = %d, stderr = %q", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), `enrolled as "lab-top"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	var ident machineIdentity
	raw, err := os.ReadFile(machineIdentPath(cfg))
	if err != nil || json.Unmarshal(raw, &ident) != nil || ident.Name != "lab-top" || ident.Serve != "http://pipe" {
		t.Fatalf("identity = %q (%v)", raw, err)
	}
	for _, p := range []string{machineKeyPath(cfg), machineIdentPath(cfg)} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v (%v), want 0600", p, st.Mode().Perm(), err)
		}
	}
	if m, err := svc.GetMachineByName(ctx, "lab-top"); err != nil || len(m.PubKey) != 32 {
		t.Fatalf("enrolled machine = %+v (%v)", m, err)
	}
	// The code is spent; serve's reason reaches the operator.
	stderr.Reset()
	if got := runMachineEnrol(cfg, code, "lab-top", &stdout, &stderr, pipe.Client()); got != exitError {
		t.Fatalf("reused code exit = %d", got)
	}
	if !strings.Contains(stderr.String(), "enrolment refused: enrolment code unknown, used or expired") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	// Enrolling again reuses the key on disk rather than making one.
	before, _ := os.ReadFile(machineKeyPath(cfg))
	code2, _ := iss.Mint(ctx, "lab")
	if got := runMachineEnrol(cfg, code2, "lab-two", &stdout, &stderr, pipe.Client()); got != exitOK {
		t.Fatalf("second enrol exit = %d, stderr = %q", got, stderr.String())
	}
	if after, _ := os.ReadFile(machineKeyPath(cfg)); !bytes.Equal(before, after) {
		t.Fatal("enrolling again replaced the machine key")
	}
}

func TestMachineKeyRefusesLooseMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "machine.key")
	if _, err := loadOrCreateMachineKey(p); err != nil {
		t.Fatalf("create: %s", err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateMachineKey(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("loose key err = %v, want chmod advice", err)
	}
}

func TestMachineEnrolArgs(t *testing.T) {
	for _, c := range []struct {
		args       []string
		code, name string
		bad        bool
	}{
		{args: []string{"ABCD-EFGH-JKMN"}, code: "ABCD-EFGH-JKMN"},
		{args: []string{"ABCD", "--name", "lab"}, code: "ABCD", name: "lab"},
		{args: []string{"--name=lab", "ABCD"}, code: "ABCD", name: "lab"},
		{args: []string{}, bad: true},
		{args: []string{"A", "B"}, bad: true},
		{args: []string{"A", "--force"}, bad: true},
	} {
		code, name, err := enrolArgs(c.args)
		if (err != nil) != c.bad || code != c.code || name != c.name {
			t.Errorf("enrolArgs(%q) = %q %q %v", c.args, code, name, err)
		}
	}
}
