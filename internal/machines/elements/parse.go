package elements

import (
	"fmt"
	"strconv"
	"strings"
)

// parse decodes the flat TSV every walker prints (scripts/darwin.swift,
// linux.py, windows.ps1 — the same shape on purpose, so one parser
// serves every OS):
//
//	size\tW\tH        native screen pixels (may be missing)
//	front\tapp        the frontmost application
//	window\ttitle     its focused window
//	el\trole\tx\ty\tw\th\tenabled\tfocused\ttitle\tvalue
//	note\ttext        why the tree is empty: permission, truncation, …
//
// Walkers and parser ship together, so an unexpected line is a bug in
// one of them: it errors with the line quoted instead of quietly
// dropping data.
func parse(raw []byte) (Tree, error) {
	var t Tree
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		switch f[0] {
		case "size":
			if len(f) != 3 {
				return t, badLine(line)
			}
			w, errW := strconv.Atoi(f[1])
			h, errH := strconv.Atoi(f[2])
			if errW != nil || errH != nil {
				return t, badLine(line)
			}
			t.ScreenW, t.ScreenH = w, h
		case "front":
			if len(f) != 2 {
				return t, badLine(line)
			}
			t.Front = f[1]
		case "window":
			if len(f) != 2 {
				return t, badLine(line)
			}
			t.Window = f[1]
		case "el":
			if len(f) != 10 {
				return t, badLine(line)
			}
			var n [4]int
			for i, s := range f[2:6] {
				v, err := strconv.Atoi(s)
				if err != nil {
					return t, badLine(line)
				}
				n[i] = v
			}
			t.Elements = append(t.Elements, Element{
				Role:    f[1],
				X:       n[0],
				Y:       n[1],
				W:       n[2],
				H:       n[3],
				Enabled: f[6] == "1",
				Focused: f[7] == "1",
				Title:   f[8],
				Value:   f[9],
			})
		case "note":
			if len(f) != 2 {
				return t, badLine(line)
			}
			t.Note = f[1]
		default:
			return t, badLine(line)
		}
	}
	return t, nil
}

// tree parses one walker payload and applies the element cap.
// Truncation always leaves a note, so an agent never mistakes a
// capped read for everything there was.
func tree(raw []byte, max int) (Tree, error) {
	t, err := parse(raw)
	if err != nil {
		return Tree{}, err
	}
	if max = limit(max); len(t.Elements) > max {
		t.Elements = t.Elements[:max]
		if t.Note == "" {
			t.Note = fmt.Sprintf("truncated at %d elements", max)
		}
	}
	return t, nil
}

// limit clamps the caller's element cap to the package defaults.
func limit(max int) int {
	switch {
	case max <= 0:
		return Default
	case max > Max:
		return Max
	default:
		return max
	}
}

// badLine errors with the line and the fix: parser and walkers change
// together, so this is always a Devcules bug, never user setup.
func badLine(line string) error {
	return fmt.Errorf("elements: unexpected walker line %q — the parser and the walkers ship together; fix both", line)
}
