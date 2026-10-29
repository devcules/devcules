package ground

import (
	"fmt"
	"strconv"
	"strings"
)

// Match is one object SAM found: its source-pixel box and the decoded
// mask raster behind it, plus the click point Ground computed.
type Match struct {
	ID     int    `json:"id"`
	X1     int    `json:"x1"`
	Y1     int    `json:"y1"`
	X2     int    `json:"x2"`
	Y2     int    `json:"y2"`
	Width  int    `json:"maskW"`
	Height int    `json:"maskH"`
	Raster []byte `json:"-"`
	ClickX int    `json:"clickX"`
	ClickY int    `json:"clickY"`
}

// Centre is the mask's largest contiguous region midpoint, in source
// pixels — a valid input target against the frame that was grounded.

// parse decodes one SAM output line:
//
//	<0f>id<|box;x1=..;y1=..;x2=..;y2=..;w=..;h=..|><|mask;x=0;y=0;data=H,W,~payload|>,id<|box;...|><|mask;...|>
//
// The grammar is documented (Meta Model API, reading segmentation
// output): a frame marker, then comma-separated records of an object
// id, one box token and one mask token. A record whose tokens do not
// parse is skipped, not fatal: the model's other matches still work.
func parse(line string) ([]Match, error) {
	if line == "" {
		// Zero matches is a valid outcome: the model's contract says an
		// empty output lane completes normally.
		return nil, nil
	}
	rest, ok := cutFrameMarker(line)
	if !ok {
		return nil, fmt.Errorf("ground: no frame marker in %q", truncate(line))
	}
	var out []Match
	for rest != "" {
		rec, tail, found := cutRecord(rest)
		rest = tail
		if found {
			m, ok, err := parseRecord(rec)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// cutRecord splits one object record off the front of s: digits, a
// box token, a mask token. Records are comma-separated, but the mask
// payload itself carries commas (its data value is H,W,payload), so a
// record boundary is a comma followed by an object id and a token —
// never a comma inside a token body, where tokens end at |>.
func cutRecord(s string) (rec, rest string, found bool) {
	// tokens end at the first |>; scan record-length as: id, then
	// pairs of <|...|> tokens until the next boundary or end.
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	tokens := 0
	for tokens < 2 && strings.HasPrefix(s[i:], "<|") {
		end := strings.Index(s[i:], "|>")
		if end < 0 {
			return s, "", false
		}
		i += end + 2
		tokens++
	}
	// after two tokens (box, mask), a comma starts the next record
	if i == len(s) {
		return s, "", true
	}
	if s[i] == ',' {
		return s[:i], s[i+1:], true
	}
	return s, "", false
}

// cutFrameMarker strips the leading <Nf> marker. Images are always <0f>.
func cutFrameMarker(line string) (string, bool) {
	if !strings.HasPrefix(line, "<") {
		return "", false
	}
	i := strings.Index(line, ">")
	if i < 0 || !strings.HasSuffix(line[:i], "f") {
		return "", false
	}
	return line[i+1:], true
}

// parseRecord reads one `id<|box;..|><|mask;..|>` record. ok is false
// for a record that does not match the grammar at all (skipped); an
// error is reserved for a mask that parses but cannot decode, which
// is never the model's fault to hide.
func parseRecord(rec string) (Match, bool, error) {
	// Object id: digits up to the first token.
	j := strings.Index(rec, "<|")
	if j <= 0 {
		return Match{}, false, nil
	}
	id, err := strconv.Atoi(strings.TrimSpace(rec[:j]))
	if err != nil {
		return Match{}, false, nil
	}
	rest := rec[j:]
	// One box token and one mask token, box first.
	boxTok, rest, ok := cutToken(rest, "box")
	if !ok {
		return Match{}, false, nil
	}
	maskTok, _, ok := cutToken(rest, "mask")
	if !ok {
		return Match{}, false, nil
	}
	m := Match{ID: id}
	fields := readFields(boxTok)
	for k, dest := range map[string]*int{"x1": &m.X1, "y1": &m.Y1, "x2": &m.X2, "y2": &m.Y2} {
		v, err := strconv.Atoi(fields[k])
		if err != nil {
			return Match{}, false, nil
		}
		*dest = v
		_ = k
	}
	if m.X2 < m.X1 || m.Y2 < m.Y1 {
		return Match{}, false, nil
	}
	mf := readFields(maskTok)
	if mf["x"] != "0" || mf["y"] != "0" {
		return Match{}, false, nil
	}
	h, w, payload, ok := cutData(mf["data"])
	if !ok {
		return Match{}, false, nil
	}
	m.Width, m.Height = w, h
	raster, err := decodeMask(payload, w, h)
	if err != nil {
		return Match{}, false, fmt.Errorf("ground: object %d: %w", id, err)
	}
	m.Raster = raster
	m.ClickX, m.ClickY = regionCentre(raster, w, h)
	return m, true, nil
}

// cutToken takes one <|name;fields|> token off the front of s.
func cutToken(s, name string) (body, rest string, ok bool) {
	if !strings.HasPrefix(s, "<|"+name) {
		return "", "", false
	}
	i := strings.Index(s, "|>")
	if i < 0 {
		return "", "", false
	}
	if c := s[len("<|"+name)]; c != 0 && c != ';' {
		return "", "", false // a different token name sharing a prefix
	}
	return s[len("<|"+name):i], s[i+2:], true
}

// readFields splits a ;-separated k=v list. Only the keys Ground uses
// matter; unknown ones (confidence) are ignored, like the wire allows.
func readFields(body string) map[string]string {
	m := map[string]string{}
	for _, part := range strings.Split(body, ";") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if found {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// cutData splits the mask's data value H,W,payload. The payload's own
// first character is the encoding marker; payload may contain ~ and !.
func cutData(data string) (h, w int, payload string, ok bool) {
	parts := strings.SplitN(data, ",", 3)
	if len(parts) != 3 {
		return 0, 0, "", false
	}
	hh, err1 := strconv.Atoi(parts[0])
	ww, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, "", false
	}
	return hh, ww, parts[2], true
}

// truncate keeps error strings bounded on hostile payloads.
func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
