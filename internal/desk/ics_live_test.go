package desk

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	ics "github.com/arran4/golang-ical"
)

// Opt-in: DEVCULES_ICS_URL=<secret address> go test -run TestLiveICS -v
// Prints what a real feed contains so parsing can be checked against it.
// Nothing about the feed is stored; the URL comes from the environment only.
func TestLiveICS(t *testing.T) {
	u := os.Getenv("DEVCULES_ICS_URL")
	if u == "" {
		t.Skip("set DEVCULES_ICS_URL to run against a real feed")
	}
	AllowPrivateHosts = false
	hc := NewClient(40 * time.Second)
	resp, err := hc.Get(u)
	if err != nil {
		t.Fatal(scrubURL(err.Error(), u))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	cal, err := ics.ParseCalendar(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	events := cal.Events()
	tzids := map[string]int{}
	loadable := map[string]bool{}
	for _, e := range events {
		if p := e.GetProperty(ics.ComponentPropertyDtStart); p != nil {
			for _, tz := range p.ICalParameters["TZID"] {
				tzids[tz]++
				_, lerr := time.LoadLocation(tz)
				loadable[tz] = lerr == nil
			}
		}
	}
	fmt.Printf("bytes=%d vevents=%d\n", len(raw), len(events))
	for tz, n := range tzids {
		fmt.Printf("TZID %q x%d loadable=%v\n", tz, n, loadable[tz])
	}
	Unresolved = map[string]int{}
	now := time.Now()
	for _, days := range []int{1, 7, 30} {
		ms, err := ParseMeetings(strings.NewReader(string(raw)), now.Add(-30*time.Minute), now.Add(time.Duration(days)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("next %2d days: %d instances\n", days, len(ms))
		if days == 7 {
			for i, m := range ms {
				if i >= 5 {
					break
				}
				fmt.Printf("   %s  %-28s  allday=%v feed=%q\n", m.Start.Format("Mon 02 15:04 MST"), clip(m.Title, 28), m.AllDay, m.Feed)
			}
		}
	}
	fmt.Printf("TZIDs still unresolved after mapping: %v\n", Unresolved)
}
