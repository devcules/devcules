package machframes

import (
	"encoding/json"
	"strings"
	"testing"
)

// The protocol package holds only types; the tests pin the constants
// and the JSON shapes both ends marshal, so a renamed field breaks
// here instead of on a machine link.
func TestFrameTypes(t *testing.T) {
	want := map[string]string{
		"screen": TypeScreen, "tree": TypeTree, "words": TypeWords,
		"input": TypeInput, "ground": TypeGround, "budget": TypeBudget,
		"verify": TypeVerify,
		"ok": TypeOK, "error": TypeFailure, "frame": TypeFrame,
		"treeout": TypeTreeOut, "wordsout": TypeWordsOut,
		"groundout": TypeGroundOut, "verifyout": TypeVerifyOut,
	}
	for name, got := range want {
		if !strings.HasPrefix(got, "mach.") {
			t.Errorf("Type(%s) = %q, want the mach. prefix", name, got)
		}
	}
	if TypeScreen == TypeTree || TypeOK == TypeFailure {
		t.Fatal("frame types collide")
	}
}

func TestPayloadShapes(t *testing.T) {
	// the JSON keys are the wire: these marshal exactly as the
	// machine helper and the relay decode them.
	b, err := json.Marshal(Screen{MaxBytes: 2048})
	if err != nil || string(b) != `{"maxBytes":2048}` {
		t.Fatalf("screen: %s (%v)", b, err)
	}
	b, err = json.Marshal(Input{Kind: "tap", X: 3, Y: 4})
	if err != nil || string(b) != `{"kind":"tap","x":3,"y":4}` {
		t.Fatalf("input: %s (%v)", b, err)
	}
	b, err = json.Marshal(Input{Kind: "key", Key: "enter"})
	if err != nil || string(b) != `{"kind":"key","key":"enter"}` {
		t.Fatalf("input key: %s (%v)", b, err)
	}
	b, err = json.Marshal(Budget{Remaining: 40})
	if err != nil || string(b) != `{"remaining":40}` {
		t.Fatalf("budget: %s (%v)", b, err)
	}
	b, err = json.Marshal(Failure{Reason: "budget exhausted"})
	if err != nil || string(b) != `{"reason":"budget exhausted"}` {
		t.Fatalf("failure: %s (%v)", b, err)
	}
	if BudgetMax != 40 {
		t.Fatalf("the plan's action budget is 40, got %d", BudgetMax)
	}
}
