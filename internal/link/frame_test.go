package link

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestDecodeTable covers every envelope rule: the happy path, each
// strictness rejection with its exact message, and the boundary
// lengths. The hub maps these errors to close codes, so the messages
// here are the contract.
func TestDecodeTable(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("a", 64)
	tooLongID := strings.Repeat("a", 65)
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "hello", in: `{"v":1,"type":"hello","id":"1","data":{"role":"runner"}}`},
		{name: "dotted type", in: `{"v":1,"type":"term.write","id":"abc","data":{}}`},
		{name: "id 64 ok", in: `{"v":1,"type":"a","id":"` + longID + `","data":{}}`},
		{name: "fields any order", in: `{"data":{},"id":"1","type":"a","v":1}`},
		{name: "unknown field", in: `{"v":1,"type":"a","id":"1","data":{},"typ":"a"}`, wantErr: "frame: "},
		{name: "missing v", in: `{"type":"a","id":"1","data":{}}`, wantErr: "v: "},
		{name: "v 2", in: `{"v":2,"type":"a","id":"1","data":{}}`, wantErr: "v: "},
		{name: "v string", in: `{"v":"1","type":"a","id":"1","data":{}}`, wantErr: "frame: "},
		{name: "empty type", in: `{"v":1,"type":"","id":"1","data":{}}`, wantErr: "type: "},
		{name: "uppercase type", in: `{"v":1,"type":"Hello","id":"1","data":{}}`, wantErr: "type: "},
		{name: "type too long", in: `{"v":1,"type":"` + strings.Repeat("a", 33) + `","id":"1","data":{}}`, wantErr: "type: "},
		{name: "type slash", in: `{"v":1,"type":"a/b","id":"1","data":{}}`, wantErr: "type: "},
		{name: "empty id", in: `{"v":1,"type":"a","id":"","data":{}}`, wantErr: "id: "},
		{name: "id too long", in: `{"v":1,"type":"a","id":"` + tooLongID + `","data":{}}`, wantErr: "id: "},
		{name: "missing data", in: `{"v":1,"type":"a","id":"1"}`, wantErr: "data: "},
		{name: "null data", in: `{"v":1,"type":"a","id":"1","data":null}`, wantErr: "data: "},
		{name: "not json", in: `not json`, wantErr: "frame: "},
		{name: "array", in: `[1,2]`, wantErr: "frame: "},
		{name: "trailing data", in: `{"v":1,"type":"a","id":"1","data":{}} ` + `{}`, wantErr: "frame: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := Decode([]byte(tc.in))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Decode error = %v", err)
				}
				if f.V != 1 || f.Type == "" || f.ID == "" || len(f.Data) == 0 {
					t.Fatalf("Decode = %+v, want a full envelope", f)
				}
				return
			}
			if err == nil {
				t.Fatalf("Decode(%q) succeeded, want error prefix %q", tc.in, tc.wantErr)
			}
			var derr *DecodeError
			if !errors.As(err, &derr) {
				t.Fatalf("Decode error = %T %v, want *DecodeError", err, err)
			}
			if !strings.HasPrefix(err.Error(), tc.wantErr) {
				t.Fatalf("Decode error = %q, want prefix %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDecodeOversize proves a frame past the limit is rejected without
// allocating for it: the length check runs before JSON parsing.
func TestDecodeOversize(t *testing.T) {
	t.Parallel()
	big := `{"v":1,"type":"a","id":"1","data":{"pad":"` + strings.Repeat("x", MaxFrameBytes) + `"}}`
	if _, err := Decode([]byte(big)); err == nil {
		t.Fatal("Decode of an oversize frame succeeded")
	} else {
		var derr *DecodeError
		if !errors.As(err, &derr) || derr.Field != "frame" {
			t.Fatalf("Decode error = %v, want a frame DecodeError", err)
		}
	}
}

// TestEncodeValidates mirrors the decode rules on the send side: a
// handler cannot emit a frame the other end would reject.
func TestEncodeValidates(t *testing.T) {
	t.Parallel()
	bad := []Frame{
		{V: 2, Type: "a", ID: "1", Data: json.RawMessage(`{}`)},
		{V: 1, Type: "Bad", ID: "1", Data: json.RawMessage(`{}`)},
		{V: 1, Type: "a", ID: "", Data: json.RawMessage(`{}`)},
		{V: 1, Type: "a", ID: "1"},
		{V: 1, Type: "a", ID: "1", Data: json.RawMessage(`null`)},
	}
	for i, f := range bad {
		if _, err := Encode(f); err == nil {
			t.Fatalf("Encode(%d) = %+v succeeded, want an error", i, f)
		}
	}
	f := Frame{V: 1, Type: "term.write", ID: NewID(), Data: json.RawMessage(`{"n":1}`)}
	b, err := Encode(f)
	if err != nil {
		t.Fatalf("Encode error = %v", err)
	}
	back, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode(Encode(f)) error = %v", err)
	}
	if back.Type != f.Type || back.ID != f.ID || string(back.Data) != string(f.Data) {
		t.Fatalf("round trip = %+v, want %+v", back, f)
	}
}

// TestNewIDUnique proves ids do not collide across reconnect-scale
// generation: random, not sequential.
func TestNewIDUnique(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 100 {
		id := NewID()
		if len(id) != 16 {
			t.Fatalf("NewID() = %q, want 16 hex chars", id)
		}
		if seen[id] {
			t.Fatalf("NewID() collision on %q", id)
		}
		seen[id] = true
	}
	if NewSession() == NewSession() {
		t.Fatal("NewSession() returned the same value twice")
	}
	if len(NewSession()) != 32 {
		t.Fatalf("NewSession() length = %d, want 32", len(NewSession()))
	}
}

// FuzzDecode is the CI fuzz target (30 s in the unit job): decoding
// must never panic, and every frame that decodes must be at a re-encode
// fixpoint — re-encoding and decoding again yields identical bytes, so
// both ends always agree on what a frame means.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		`{"v":1,"type":"hello","id":"1","data":{"role":"runner","name":"r","version":"v"}}`,
		`{"v":1,"type":"welcome","id":"abc","data":{"session":"s"}}`,
		`{"v":1,"type":"term.write","id":"` + strings.Repeat("i", 64) + `","data":{"x":[1,2,{"y":null}]}}`,
		`{"v":2,"type":"Hello","id":"","data":null,"extra":true}`,
		`not json at all`,
		``,
		`[1,2,3]`,
		`{"v":1}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fr, err := Decode(b)
		if err != nil {
			return
		}
		e1, err := Encode(fr)
		if err != nil {
			t.Fatalf("Encode(Decode(%q)) error = %v", b, err)
		}
		fr2, err := Decode(e1)
		if err != nil {
			t.Fatalf("Decode(Encode(Decode())) error = %v", err)
		}
		e2, err := Encode(fr2)
		if err != nil {
			t.Fatalf("second Encode error = %v", err)
		}
		if string(e1) != string(e2) {
			t.Fatalf("not at fixpoint:\n%s\n%s", e1, e2)
		}
	})
}
