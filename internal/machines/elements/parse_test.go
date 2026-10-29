package elements

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixtures are real walker output shapes, one per OS, checked into
// testdata: the parser is pinned to what every walker prints.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestParseDarwinFixture(t *testing.T) {
	got, err := tree(fixture(t, "darwin.tsv"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Front != "Terminal" || got.ScreenW != 2560 || got.ScreenH != 1440 {
		t.Fatalf("header: %+v", got)
	}
	if len(got.Elements) != 4 {
		t.Fatalf("elements: %d", len(got.Elements))
	}
	btn := got.Elements[2]
	if btn.Role != "button" || btn.Title != "Commit" || !btn.Enabled || btn.Focused {
		t.Fatalf("button: %+v", btn)
	}
	if cx, cy := btn.Centre(); cx != 1000 || cy != 730 {
		t.Fatalf("centre: %d,%d", cx, cy)
	}
	if got.Elements[1].Value != "" { // password values never leave the walker
		t.Fatalf("password value leaked: %q", got.Elements[1].Value)
	}
}

func TestParseLinuxFixture(t *testing.T) {
	got, err := tree(fixture(t, "linux.tsv"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Front != "gnome-text-editor" || got.Window != "notes.txt" {
		t.Fatalf("header: %+v", got)
	}
	if len(got.Elements) != 3 {
		t.Fatalf("elements: %d", len(got.Elements))
	}
	if got.Elements[0].Role != "field" || got.Elements[0].Value != "hello world" {
		t.Fatalf("field: %+v", got.Elements[0])
	}
}

func TestParseWindowsFixture(t *testing.T) {
	got, err := tree(fixture(t, "windows.tsv"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Front != "Notepad" {
		t.Fatalf("header: %+v", got)
	}
	if len(got.Elements) != 3 {
		t.Fatalf("elements: %d", len(got.Elements))
	}
	if got.Elements[2].Role != "button" || got.Elements[2].Title != "Save" {
		t.Fatalf("button: %+v", got.Elements[2])
	}
}

func TestParseTruncatedNote(t *testing.T) {
	got, err := tree(fixture(t, "darwin.tsv"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Elements) != 2 {
		t.Fatalf("cap not applied: %d", len(got.Elements))
	}
	if got.Note != "truncated at 2 elements" {
		t.Fatalf("note: %q", got.Note)
	}
}

func TestParseKeepsWalkerNote(t *testing.T) {
	got, err := tree([]byte("note\tno accessibility permission: grant it in System Settings\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Elements) != 0 || got.Note == "" {
		t.Fatalf("note lost: %+v", got)
	}
}

func TestParseBadLine(t *testing.T) {
	if _, err := tree([]byte("wat\tnope\n"), 0); err == nil {
		t.Fatal("expected error for unknown line kind")
	}
}

func TestLimit(t *testing.T) {
	for in, want := range map[int]int{0: Default, -5: Default, 50: 50, 99999: Max} {
		if got := limit(in); got != want {
			t.Errorf("limit(%d) = %d, want %d", in, got, want)
		}
	}
}

// scriptedDriver mimics any OS driver's wiring with a fake runner, so
// install → invoke → parse is exercised on every platform: the same
// helpers the real drivers use, with the process swapped out.
type scriptedDriver struct {
	dir   string
	run   command
	calls []string
}

func newScripted(t *testing.T, run command) *scriptedDriver {
	t.Helper()
	dir, err := newDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := install(dir, "walker.txt", "script"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return &scriptedDriver{dir: dir, run: run}
}

func (d *scriptedDriver) Tree(ctx context.Context, max int) (Tree, error) {
	out, err := d.run(ctx, "walker", d.dir+"/walker.txt", itoa(limit(max)))
	if err != nil {
		return Tree{}, err
	}
	d.calls = append(d.calls, "walker")
	return tree(out, max)
}

// itoa renders small ints without strconv, keeping this file's deps
// to the ones it already uses.
func itoa(n int) string { return fmt.Sprint(n) }

func TestDriverWiring(t *testing.T) {
	payload := fixture(t, "linux.tsv")
	var calls int
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != "walker" {
			t.Errorf("unexpected command %q", name)
		}
		return payload, nil
	}
	d := newScripted(t, run)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := d.Tree(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("walker ran %d times", calls)
	}
	if len(got.Elements) != 3 {
		t.Fatalf("elements: %d", len(got.Elements))
	}
}

// TestTimeoutIsBounded proves a wedged engine call fails at the
// driver's timeout instead of hanging the helper.
func TestTimeoutIsBounded(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done() // wedge until the context fires
		return nil, ctx.Err()
	}
	d := newScripted(t, run)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := d.Tree(ctx, 10); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("unbounded: took %v", time.Since(start))
	}
}
