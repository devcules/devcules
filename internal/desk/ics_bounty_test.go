package desk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teambition/rrule-go"
)

// b3VEvent wraps VEVENT bodies (CRLF lines) in a calendar.
func b3VEvent(events ...string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n")
	for _, e := range events {
		b.WriteString("BEGIN:VEVENT\r\n" + e + "END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// b3SetNow pins desk.Now for the test.
func b3SetNow(t *testing.T, at *time.Time) {
	t.Helper()
	Now = func() time.Time { return *at }
	t.Cleanup(func() { Now = time.Now })
}

// GORT-01: concurrent parses of a feed with an unresolvable TZID used to
// write a package-level map without a lock ("fatal error: concurrent map
// writes", which kills serve). The resolver now keeps no shared state.
// The resolver loop makes the race hit fast; the ParseMeetings loop
// covers the real entry point.
func TestParseMeetingsConcurrentUnknownTZID(t *testing.T) {
	ics := b3VEvent("UID:u\r\nSUMMARY:s\r\nDTSTART;TZID=Customized Time Zone:20261102T090000\r\n")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				r := &tzResolver{cache: map[string]*time.Location{}, custom: map[string]*time.Location{}}
				if r.location("Customized Time Zone") != time.Local {
					t.Error("unknown TZID did not fall back to local time")
					return
				}
			}
			for i := 0; i < 100; i++ {
				ms, err := ParseMeetings(strings.NewReader(ics), time.Unix(0, 0), time.Unix(4e9, 0))
				if err != nil || len(ms) != 1 {
					t.Errorf("parse = %d, %v", len(ms), err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// GORT-07: three calls inside the TTL fetch the feed once and leave one
// cache entry (the key used to carry time.Now() in seconds).
func TestMeetingsCacheHitsWithinTTL(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 10, 5, 0, 0, time.UTC)
	b3SetNow(t, &now)
	var hits atomic.Int32
	f.net.set("cal.example.com", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, feedICS)
	})
	if err := f.svc.WriteSources(ctx, Sources{ICSFeeds: []ICSFeed{{Label: "Work", URL: "https://cal.example.com/w.ics"}}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.svc.Meetings(ctx, false); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			time.Sleep(1100 * time.Millisecond) // the old key changed every wall-clock second
		}
	}
	f.svc.meetC.mu.Lock()
	n := len(f.svc.meetC.items)
	f.svc.meetC.mu.Unlock()
	if hits.Load() != 1 || n != 1 {
		t.Fatalf("3 calls within the TTL: feed fetched %d times, %d cache entries; want 1 and 1", hits.Load(), n)
	}

	// Later in the same hour, past the TTL: refetched under the same key.
	now = now.Add(40 * time.Minute)
	if _, err := f.svc.Meetings(ctx, false); err != nil {
		t.Fatal(err)
	}
	f.svc.meetC.mu.Lock()
	n = len(f.svc.meetC.items)
	f.svc.meetC.mu.Unlock()
	if hits.Load() != 2 || n != 1 {
		t.Fatalf("after the TTL: fetched %d times, %d entries; want 2 and 1", hits.Load(), n)
	}

	// The Sync limit still holds: a forced refresh right after a fetch is
	// served from the cache once forced recently.
	if _, err := f.svc.Meetings(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Meetings(ctx, true); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 {
		t.Fatalf("two forced refreshes within minRefresh fetched %d times in total, want 3", hits.Load())
	}

	// The stale fallback still holds under the stable key.
	f.net.set("cal.example.com", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusBadGateway) })
	now = now.Add(11 * time.Minute) // 10:56: past TTL and minRefresh, same hour
	raw, err := f.svc.Meetings(ctx, false)
	if err != nil || !strings.Contains(string(raw), `"stale":true`) {
		t.Fatalf("feed down after a good fetch = %s, %v; want the stale value", raw, err)
	}
}

// GORT-07: the "next up" window still covers ongoing meetings and the
// next 7 days, and stays separate from the calendar grid's wide window.
func TestMeetingsWindowBucketed(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 10, 59, 0, 0, time.UTC)
	b3SetNow(t, &now)
	ics := b3VEvent(
		"UID:ongoing\r\nSUMMARY:Ongoing\r\nDTSTART:20261104T090000Z\r\nDTEND:20261104T113000Z\r\n",
		"UID:old\r\nSUMMARY:Old\r\nDTSTART:20261104T060000Z\r\nDTEND:20261104T070000Z\r\n",
		"UID:week\r\nSUMMARY:InSixDays\r\nDTSTART:20261110T100000Z\r\nDTEND:20261110T110000Z\r\n",
	)
	var hits atomic.Int32
	f.net.set("cal.example.com", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, ics)
	})
	if err := f.svc.WriteSources(ctx, Sources{ICSFeeds: []ICSFeed{{Label: "Work", URL: "https://cal.example.com/w.ics"}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := f.svc.Meetings(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "Ongoing") || !strings.Contains(s, "InSixDays") || strings.Contains(s, `"Old"`) {
		t.Fatalf("next-up list = %s", s)
	}
	// A grid window is cached under its own key: different data, no trample.
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	grid, err := f.svc.FeedMeetings(ctx, from, from.AddDate(0, 2, 0), false, 0)
	if err != nil || !strings.Contains(string(grid), `"Old"`) {
		t.Fatalf("grid = %s, %v", grid, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("next-up and grid fetched %d times, want 2 (separate keys)", hits.Load())
	}
	// Crossing the hour moves to a new key.
	now = now.Add(2 * time.Minute)
	if _, err := f.svc.Meetings(ctx, false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 {
		t.Fatalf("new hour fetched %d times in total, want 3", hits.Load())
	}
}

// GORT-07: a cache keyed by window drops keys that went unused, and never
// holds more than maxCacheItems keys.
func TestCacheEvictsIdleAndCaps(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	b3SetNow(t, &now)
	c := NewCache[int](10*time.Minute, 2*time.Minute).evictIdle(time.Hour)
	ctx := context.Background()
	one := func(context.Context) (int, error) { return 1, nil }
	c.Get(ctx, "a", false, one)
	c.Get(ctx, "b", false, one)
	now = now.Add(50 * time.Minute)
	c.Get(ctx, "b", false, one) // b stays in use
	now = now.Add(20 * time.Minute)
	c.Get(ctx, "c", false, one)
	c.mu.Lock()
	_, hasA := c.items["a"]
	_, hasB := c.items["b"]
	n := len(c.items)
	c.mu.Unlock()
	if hasA || !hasB || n != 2 {
		t.Fatalf("after 70 min: a kept=%v b kept=%v entries=%d; want a evicted, b and c kept", hasA, hasB, n)
	}

	// A key in use keeps its stale fallback across the sweep.
	if e := c.Get(ctx, "b", true, func(context.Context) (int, error) { return 0, fmt.Errorf("down") }); e.Value != 1 || !e.Stale {
		t.Fatalf("b after a failed refresh = %+v, want the stale 1", e)
	}

	for i := 0; i < 3*maxCacheItems; i++ {
		now = now.Add(time.Second)
		c.Get(ctx, fmt.Sprintf("k%d", i), false, one)
	}
	c.mu.Lock()
	n = len(c.items)
	_, newest := c.items[fmt.Sprintf("k%d", 3*maxCacheItems-1)]
	c.mu.Unlock()
	if n > maxCacheItems || !newest {
		t.Fatalf("entries = %d (cap %d), newest kept = %v", n, maxCacheItems, newest)
	}

	// A cache without evictIdle (weather, news) keeps an idle key: it is
	// the stale fallback for the next request.
	w := NewCache[int](time.Minute, time.Minute)
	w.Get(ctx, "x", false, one)
	now = now.Add(48 * time.Hour)
	w.Get(ctx, "y", false, one)
	w.mu.Lock()
	_, hasX := w.items["x"]
	w.mu.Unlock()
	if !hasX {
		t.Fatal("a cache without evictIdle dropped an idle key")
	}
}

// GORT-08 (feed side): FREQ=SECONDLY used to expand every second of the
// window into memory. Sub-hourly series show only their first instance.
func TestParseMeetingsSecondlyBounded(t *testing.T) {
	ics := b3VEvent("UID:a@x\r\nSUMMARY:spam\r\nDTSTART:20261101T000000Z\r\nDTEND:20261101T000001Z\r\nRRULE:FREQ=SECONDLY\r\n",
		"UID:b@x\r\nSUMMARY:spam2\r\nDTSTART:20261101T000000Z\r\nRRULE:FREQ=MINUTELY;INTERVAL=1\r\n")
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ms, err := ParseMeetings(strings.NewReader(ics), from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("1-day window -> %d meetings, want 2 (first instance of each)", len(ms))
	}
}

// GORT-08 (feed side): dense coarser rules are capped per series and per
// feed, and an old dense series cannot spin the iterator.
func TestParseMeetingsCapsPerSeriesAndFeed(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	// DAILY with BYHOUR/BYMINUTE/BYSECOND: 4,320 instances a day, all
	// built inside one iterator call. More times a day than
	// maxRuleTimes show only the first instance.
	dense := b3VEvent("UID:d@x\r\nSUMMARY:dense\r\nDTSTART:20261101T000000Z\r\n" +
		"RRULE:FREQ=DAILY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23;" +
		"BYMINUTE=0,2,4,6,8,10,12,14,16,18,20,22,24,26,28,30,32,34,36,38,40,42,44,46,48,50,52,54,56,58;" +
		"BYSECOND=0,10,20,30,40,50\r\n")
	ms, err := ParseMeetings(strings.NewReader(dense), from, from.Add(24*time.Hour))
	if err != nil || len(ms) != 1 {
		t.Fatalf("dense daily rule -> %d, %v; want its first instance only", len(ms), err)
	}

	// 24 times a day over 60 days: capped per series.
	hours := b3VEvent("UID:d2@x\r\nSUMMARY:hours\r\nDTSTART:20261101T000000Z\r\n" +
		"RRULE:FREQ=DAILY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23\r\n")
	ms, err = ParseMeetings(strings.NewReader(hours), from, from.AddDate(0, 0, 60))
	if err != nil || len(ms) != maxSeriesInstances {
		t.Fatalf("24-a-day rule over 60 days -> %d, %v; want the cap %d", len(ms), err, maxSeriesInstances)
	}

	// A plain DAILY over a 20-year window is capped too.
	daily := b3VEvent("UID:e@x\r\nSUMMARY:daily\r\nDTSTART:20261101T090000Z\r\nRRULE:FREQ=DAILY\r\n")
	ms, err = ParseMeetings(strings.NewReader(daily), from, from.AddDate(20, 0, 0))
	if err != nil || len(ms) != maxSeriesInstances {
		t.Fatalf("daily over 20 years -> %d, %v", len(ms), err)
	}

	// An HOURLY series from 1900 asked for 2026 stops walking (bounded
	// steps) instead of iterating a million occurrences.
	old := b3VEvent("UID:h@x\r\nSUMMARY:hourly\r\nDTSTART:19000101T000000Z\r\nRRULE:FREQ=HOURLY\r\n")
	t0 := time.Now()
	if _, err := ParseMeetings(strings.NewReader(old), from, from.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("old hourly series took %s", d)
	}

	// Many series: the feed total is capped.
	var evs []string
	for i := 0; i < 10; i++ {
		evs = append(evs, fmt.Sprintf("UID:s%d@x\r\nSUMMARY:s%d\r\nDTSTART:20261101T%02d0000Z\r\nRRULE:FREQ=HOURLY\r\n", i, i, i))
	}
	ms, err = ParseMeetings(strings.NewReader(b3VEvent(evs...)), from, from.AddDate(1, 0, 0))
	if err != nil || len(ms) != maxFeedMeetings {
		t.Fatalf("10 hourly series over a year -> %d, %v; want the feed cap %d", len(ms), err, maxFeedMeetings)
	}

	// Ordinary recurrence is unaffected: a weekly series over a month.
	weekly := b3VEvent("UID:w@x\r\nSUMMARY:weekly\r\nDTSTART:20261102T090000Z\r\nDTEND:20261102T093000Z\r\nRRULE:FREQ=WEEKLY\r\n")
	ms, err = ParseMeetings(strings.NewReader(weekly), from, from.AddDate(0, 1, 0))
	if err != nil || len(ms) != 5 {
		t.Fatalf("weekly over November -> %d, %v; want 5", len(ms), err)
	}
}

// GORT-08 (feed side): the merged response over several feeds is capped.
func TestFeedMeetingsMergedCap(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	var evs []string
	for i := 0; i < 10; i++ {
		evs = append(evs, fmt.Sprintf("UID:s%d@x\r\nSUMMARY:s%d\r\nDTSTART:20261101T%02d0000Z\r\nRRULE:FREQ=HOURLY\r\n", i, i, i))
	}
	ics := b3VEvent(evs...)
	f.net.set("cal.example.com", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, ics) })
	if err := f.svc.WriteSources(ctx, Sources{ICSFeeds: []ICSFeed{
		{Label: "A", URL: "https://cal.example.com/a.ics"},
		{Label: "B", URL: "https://cal.example.com/b.ics"},
	}}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	raw, err := f.svc.FeedMeetings(ctx, from, from.AddDate(1, 0, 0), false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), `"uid"`); n != maxFeedMeetings {
		t.Fatalf("two capped feeds merged -> %d items, want %d", n, maxFeedMeetings)
	}
}

// GORT-06 part 4: an all-day weekly feed event whose first instance spans
// the 25-hour DST-end day must not bleed into the next day on later weeks.
func TestParseMeetingsAllDayRecurringAcrossDST(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip(err)
	}
	old := time.Local
	time.Local = ams
	defer func() { time.Local = old }()
	ics := b3VEvent("UID:a@x\r\nSUMMARY:Sunday off\r\nDTSTART;VALUE=DATE:20261025\r\nDTEND;VALUE=DATE:20261026\r\nRRULE:FREQ=WEEKLY\r\n")
	// Monday 2 Nov only: nothing.
	mon := time.Date(2026, 11, 2, 0, 0, 0, 0, ams)
	ms, err := ParseMeetings(strings.NewReader(ics), mon, mon.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 {
		t.Fatalf("Sunday all-day event listed on Monday: %+v", ms)
	}
	// Sunday 1 Nov: one instance, local midnight to midnight.
	sun := mon.AddDate(0, 0, -1)
	ms, err = ParseMeetings(strings.NewReader(ics), sun, mon)
	if err != nil || len(ms) != 1 {
		t.Fatalf("Sunday 1 Nov -> %d, %v", len(ms), err)
	}
	if !ms[0].AllDay || !ms[0].Start.Equal(sun) || !ms[0].End.Equal(mon) {
		t.Fatalf("instance = %s -> %s", ms[0].Start, ms[0].End)
	}
	// The DST-end Sunday itself is still a whole (25 h) day.
	ms, err = ParseMeetings(strings.NewReader(ics), time.Date(2026, 10, 25, 0, 0, 0, 0, ams), time.Date(2026, 10, 26, 0, 0, 0, 0, ams))
	if err != nil || len(ms) != 1 || ms[0].End.Sub(ms[0].Start) != 25*time.Hour {
		t.Fatalf("25 Oct -> %+v, %v", ms, err)
	}
	// A two-day all-day series given by DURATION keeps whole days too.
	two := b3VEvent("UID:b@x\r\nSUMMARY:Weekend\r\nDTSTART;VALUE=DATE:20261024\r\nDURATION:P2D\r\nRRULE:FREQ=WEEKLY\r\n")
	ms, err = ParseMeetings(strings.NewReader(two), mon, mon.AddDate(0, 0, 1))
	if err != nil || len(ms) != 0 {
		t.Fatalf("Sat-Sun event on Monday 2 Nov: %+v, %v", ms, err)
	}
}

// GORT-08 (feed side), check round 1 item 1: the feed cap keeps the
// EARLIEST instances, not the first ones in file order. Dense series come
// first in the file (enough to trip the 2x-cap trim mid-parse), the
// meeting that is really next comes last, and a weekly series after it
// starts before the cutoff too.
func TestParseMeetingsCapKeepsEarliest(t *testing.T) {
	from := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	seriesStart := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	var evs []string
	var want []time.Time
	for i := 0; i < 70; i++ {
		evs = append(evs, fmt.Sprintf("UID:h%d@x\r\nSUMMARY:h%d\r\nDTSTART:20261105T000000Z\r\nRRULE:FREQ=HOURLY\r\n", i, i))
		for st := seriesStart; st.Before(to); st = st.Add(time.Hour) {
			want = append(want, st)
		}
	}
	evs = append(evs,
		"UID:next@x\r\nSUMMARY:really next\r\nDTSTART:20261104T100000Z\r\nDTEND:20261104T103000Z\r\n",
		"UID:wk@x\r\nSUMMARY:weekly\r\nDTSTART:20261104T110000Z\r\nDTEND:20261104T113000Z\r\nRRULE:FREQ=WEEKLY\r\n")
	want = append(want, time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC), time.Date(2026, 11, 11, 11, 0, 0, 0, time.UTC))
	if len(want) < 2*maxFeedMeetings {
		t.Fatalf("fixture too small to trip the mid-parse trim: %d", len(want))
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Before(want[j]) })

	// No deadline: what is kept must depend on the cap alone, not on
	// how busy the box is.
	ms, st, err := parseMeetings(context.Background(), strings.NewReader(b3VEvent(evs...)), from, to, b3NoDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if st.skipped != 0 {
		t.Fatalf("stats %+v: nothing should be skipped", st)
	}
	if len(ms) != maxFeedMeetings {
		t.Fatalf("got %d meetings, want the cap %d", len(ms), maxFeedMeetings)
	}
	if ms[0].UID != "next@x" || ms[1].UID != "wk@x" {
		t.Fatalf("first two = %s@%s, %s@%s; want the 10:00 meeting then the 11:00 weekly",
			ms[0].UID, ms[0].Start, ms[1].UID, ms[1].Start)
	}
	for i, m := range ms {
		if !m.Start.Equal(want[i]) {
			t.Fatalf("instance %d starts %s, want %s (not the earliest %d)", i, m.Start, want[i], maxFeedMeetings)
		}
	}
	// Everything dropped starts at or after the last kept instance.
	last := ms[len(ms)-1].Start
	for _, st := range want[maxFeedMeetings:] {
		if st.Before(last) {
			t.Fatalf("dropped instance %s starts before the last kept %s", st, last)
		}
	}
}

// b3NoDeadline keeps the default step budget but no practical deadline, so
// a test's counts depend on the budget alone, never on how busy the box is.
var b3NoDeadline = expandLimits{feedSteps: defaultExpandLimits.feedSteps, feedTime: time.Hour}

// b3Ordinary are the ordinary events the expansion tests put around
// hostile series: a single meeting and a weekly series first, a single
// meeting last. b3OrdinaryWant is what they give in the test window.
var (
	b3OrdinaryHead = []string{
		"UID:one@x\r\nSUMMARY:ordinary\r\nDTSTART:20261104T100000Z\r\nDTEND:20261104T103000Z\r\n",
		"UID:wk@x\r\nSUMMARY:weekly\r\nDTSTART:20261028T120000Z\r\nDTEND:20261028T123000Z\r\nRRULE:FREQ=WEEKLY\r\n",
	}
	b3OrdinaryTail = "UID:late@x\r\nSUMMARY:late single\r\nDTSTART:20261105T100000Z\r\nDTEND:20261105T103000Z\r\n"
	b3OrdinaryWant = map[string]int{"one@x": 1, "wk@x": 2, "late@x": 1}
)

// b3Feed puts series between the ordinary head and tail events.
func b3Feed(series ...string) string {
	evs := append(append([]string{}, b3OrdinaryHead...), series...)
	return b3VEvent(append(evs, b3OrdinaryTail)...)
}

// b3OldHourly is n HOURLY series from 1900: each walks its 200k-step cap
// and emits nothing in 2026 (about 65 ms each on an idle box).
func b3OldHourly(n int) []string {
	var evs []string
	for i := 0; i < n; i++ {
		evs = append(evs, fmt.Sprintf("UID:old%d@x\r\nSUMMARY:old\r\nDTSTART:19000101T000000Z\r\nRRULE:FREQ=HOURLY\r\n", i))
	}
	return evs
}

// GORT-08 (feed side), check round 1 item 2: the expansion work is
// bounded per feed, not only per series. Hundreds of HOURLY series from
// 1900 each walk their 200k-step cap and emit nothing, so an 8 MB feed of
// them was hours of CPU. The feed-wide step budget stops expanding after
// feedSteps; ordinary events earlier in the file are still returned, and
// single events later in the file are still read. The budget is injected
// small (the default is 5M) and the deadline large, so the exact counts
// depend on the step budget only.
func TestParseMeetingsFeedStepBudget(t *testing.T) {
	from := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	const old = 30
	lim := expandLimits{feedSteps: 1_000_000, feedTime: time.Hour}
	ms, st, err := parseMeetings(context.Background(), strings.NewReader(b3Feed(b3OldHourly(old)...)), from, to, lim)
	if err != nil {
		t.Fatal(err)
	}
	b3WantUIDs(t, ms, b3OrdinaryWant)
	walked := lim.feedSteps / maxSeriesSteps // old series that fit the budget
	// The weekly series walks 3 steps before the old ones use the rest.
	if st.steps != lim.feedSteps+3 && st.steps != lim.feedSteps {
		t.Fatalf("stats %+v: want %d steps walked", st, lim.feedSteps)
	}
	if st.skipped != old-walked || st.empty != 0 || st.collapsed != 0 || st.failed != 0 {
		t.Fatalf("stats %+v: want %d series skipped and nothing else", st, old-walked)
	}
	if defaultExpandLimits.feedSteps < 5_000_000 {
		t.Fatalf("default feed step budget %d, want at least 5M (large feeds of old weekly series)", defaultExpandLimits.feedSteps)
	}
}

// Check round 2 note: the default budget no longer cuts ordinary large
// feeds. 1,500 open-ended weekly series from 2010 each walk ~880 steps
// to reach 2026; with a 1M budget 364 of them were dropped.
func TestParseMeetingsLargeLegitFeed(t *testing.T) {
	from := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	to := from.Add(14 * 24 * time.Hour)
	var evs []string
	for i := 0; i < 1500; i++ {
		evs = append(evs, fmt.Sprintf("UID:w%d@x\r\nSUMMARY:w\r\nDTSTART:20100104T%02d%02d00Z\r\nDTEND:20100104T%02d%02d00Z\r\nRRULE:FREQ=WEEKLY\r\n",
			i, 8+i%10, i%60, 9+i%10, i%60))
	}
	ms, st, err := parseMeetings(context.Background(), strings.NewReader(b3VEvent(evs...)), from, to, b3NoDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3000 || st.skipped != 0 {
		t.Fatalf("1,500 weekly series over two weeks -> %d instances, stats %+v; want 3000 and none skipped", len(ms), st)
	}
}

// GORT-08 (feed side), check round 2 item 1: rule shapes on which one
// rrule-go iterator call panics, never returns, allocates without bound,
// or searches up to year 9999 for minutes. Each is either refused by
// walkableRule (shown as its first instance) or proven empty by
// ruleIsEmpty (shown as nothing) before any walk. The reason column pins
// which.
func TestWalkableRuleShapes(t *testing.T) {
	cases := []struct {
		rule   string
		reason string // walkableRule's verdict ("" = walkable)
		empty  bool   // ruleIsEmpty's verdict, for walkable rules
	}{
		// Panics inside rrule-go v1.8.2 (index out of range).
		{"FREQ=YEARLY;BYEASTER=1000", "byeaster", false},
		{"FREQ=YEARLY;BYEASTER=-200", "byeaster", false},
		{"FREQ=MONTHLY;BYDAY=-53MO", "byday-nth", false},
		{"FREQ=MONTHLY;BYDAY=53MO", "byday-nth", false},
		{"FREQ=YEARLY;BYMONTH=1;BYDAY=-53MO", "byday-nth", false},
		{"FREQ=MONTHLY;INTERVAL=100000000000", "interval", false},
		// Never returns: from 09:00 the hour only takes odd values.
		{"FREQ=HOURLY;INTERVAL=2;BYHOUR=2", "byhour-unreachable", false},
		{"FREQ=HOURLY;INTERVAL=6;BYHOUR=1,2,4,5,7", "byhour-unreachable", false},
		// Minutes to hours inside one call (check-2 table).
		{"FREQ=HOURLY;BYSETPOS=2", "hourly-bysetpos", false},
		{"FREQ=HOURLY;BYMINUTE=0;BYSETPOS=3", "hourly-bysetpos", false},
		{"FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30;BYMINUTE=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45,46,47,48,49,50,51,52,53,54,55,56,57,58,59", "list", false},
		{"FREQ=HOURLY;BYMINUTE=0,15,30,45;BYSECOND=0,1,2,3,4;BYSETPOS=366", "hourly-bysetpos", false},
		{"FREQ=HOURLY;BYMINUTE=0,30", "sub-hourly", false},
		{"FREQ=HOURLY;INTERVAL=25;BYMONTH=2;BYMONTHDAY=30", "hourly-interval", false},
		{"FREQ=MINUTELY", "sub-hourly", false},
		// Unbounded allocation: 8,640 times a day, 366 days, in one call.
		{"FREQ=YEARLY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23;BYMINUTE=0,10,20,30,40,50;BYSECOND=0,1", "times", false},
		{"FREQ=DAILY;BYMONTHDAY=1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1", "list", false},
		// Can never match: searched to 9999 before; now proven empty.
		{"FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30", "", true},
		{"FREQ=HOURLY;BYMONTH=4;BYMONTHDAY=31", "", true},
		{"FREQ=DAILY;BYMONTH=2;BYMONTHDAY=30", "", true},
		{"FREQ=WEEKLY;BYMONTH=4;BYMONTHDAY=31", "", true},
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", "", true},
		{"FREQ=YEARLY;BYWEEKNO=53;BYMONTH=6", "", true},
		{"FREQ=YEARLY;BYYEARDAY=366;BYMONTH=1", "", true},
		{"FREQ=DAILY;BYSETPOS=2", "", true},
		{"FREQ=WEEKLY;BYSETPOS=8", "", true},
		{"FREQ=MONTHLY;BYSETPOS=32", "", true},
		{"FREQ=MONTHLY;INTERVAL=12;BYMONTH=3", "", true}, // DTSTART is in November: every 12th month is November
		{"FREQ=DAILY;INTERVAL=7;BYDAY=MO", "", true},     // DTSTART is a Wednesday
		// Ordinary and rare-but-real rules stay walkable and non-empty.
		{"FREQ=WEEKLY", "", false},
		{"FREQ=WEEKLY;BYDAY=MO,WE,FR;INTERVAL=2", "", false},
		{"FREQ=MONTHLY;BYDAY=-1FR", "", false},
		{"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", "", false},
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", "", false},
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO", "", false}, // a Monday 29 Feb: every 28 years or so
		{"FREQ=DAILY;INTERVAL=1000;BYMONTH=2;BYMONTHDAY=29", "", false},
		{"FREQ=DAILY;BYHOUR=9,14", "", false},
		{"FREQ=HOURLY", "", false},
		{"FREQ=HOURLY;INTERVAL=8;BYHOUR=1,9,17", "", false},
	}
	start := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC) // a Wednesday
	for _, c := range cases {
		rule, err := rrule.StrToRRule(c.rule)
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		if got := walkableRule(rule.OrigOptions, start); got != c.reason {
			t.Errorf("%s: walkableRule = %q, want %q", c.rule, got, c.reason)
			continue
		}
		if c.reason != "" {
			continue
		}
		for _, st := range []time.Time{start, time.Date(1, 1, 3, 9, 0, 0, 0, time.UTC), time.Date(1900, 1, 3, 9, 0, 0, 0, time.UTC)} {
			if st.Weekday() != time.Wednesday {
				t.Fatalf("fixture start %s is not a Wednesday", st)
			}
			if st != start && strings.Contains(c.rule, "INTERVAL=12;BYMONTH=3") {
				st = st.AddDate(0, 10, 0) // keep it a November start
				st = st.AddDate(0, 0, int(time.Wednesday-st.Weekday()+7)%7)
			}
			empty, err := ruleIsEmpty(rule.OrigOptions, st)
			if err != nil || empty != c.empty {
				t.Errorf("%s from %s: ruleIsEmpty = %v, %v; want %v", c.rule, st.Format("2006-01-02"), empty, err, c.empty)
			}
		}
	}
}

// b3HostileSeries is every hostile shape of TestWalkableRuleShapes as a
// VEVENT (with an unreachable BYHOUR for these starts), from 2026 and from year 1 (the iterator's search to 9999 used to
// cost more from older starts).
func b3HostileSeries() (evs []string, collapsed, empty int) {
	rules := []string{
		"FREQ=YEARLY;BYEASTER=1000", "FREQ=MONTHLY;BYDAY=-53MO", "FREQ=HOURLY;INTERVAL=2;BYHOUR=1",
		"FREQ=HOURLY;BYSETPOS=2", "FREQ=HOURLY;BYMINUTE=0;BYSETPOS=3",
		"FREQ=HOURLY;BYMINUTE=0,15,30,45;BYSECOND=0,1,2,3,4;BYSETPOS=366",
		"FREQ=HOURLY;INTERVAL=25;BYMONTH=2;BYMONTHDAY=30",
	}
	impossible := []string{
		"FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30", "FREQ=HOURLY;BYMONTH=4;BYMONTHDAY=31",
		"FREQ=DAILY;BYMONTH=2;BYMONTHDAY=30", "FREQ=WEEKLY;BYMONTH=4;BYMONTHDAY=31",
		"FREQ=YEARLY;BYWEEKNO=53;BYMONTH=6", "FREQ=DAILY;BYSETPOS=2", "FREQ=MONTHLY;BYSETPOS=32",
	}
	i := 0
	// Even hours: BYHOUR=1 is unreachable. The year-1 start is one second
	// past the zero instant, which is collapsed outright (see
	// TestParseMeetingsZeroDTStart), so these still reach the classifier.
	for _, dt := range []string{"20261104T100000Z", "00010101T000002Z"} {
		for _, r := range rules {
			evs = append(evs, fmt.Sprintf("UID:bad%d@x\r\nSUMMARY:bad\r\nDTSTART:%s\r\nRRULE:%s\r\n", i, dt, r))
			i++
		}
		for _, r := range impossible {
			evs = append(evs, fmt.Sprintf("UID:imp%d@x\r\nSUMMARY:never\r\nDTSTART:%s\r\nRRULE:%s\r\n", i, dt, r))
			i++
		}
	}
	return evs, 2 * len(rules), 2 * len(impossible)
}

// GORT-08 (feed side), check round 2 item 1: a feed of every hostile shape
// parses on the calling goroutine, leaves no goroutine behind, and still
// returns its ordinary events. The counts are exact (no wall clock is
// involved); the time bound is loose on purpose (about 0.5 s idle) and
// only catches a shape that spins again.
func TestParseMeetingsHostileRules(t *testing.T) {
	from := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	evs, collapsed, empty := b3HostileSeries()
	feed := b3Feed(evs...)
	before := runtime.NumGoroutine()
	t0 := time.Now()
	ms, st, err := parseMeetings(context.Background(), strings.NewReader(feed), from, to, b3NoDeadline)
	took := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d -> %d: the parse left work running", before, after)
	}
	// The collapsed ones from 2026 show their first instance (DTSTART is
	// inside the window); the year-1 ones and the empty ones show nothing.
	want := map[string]int{}
	for k, v := range b3OrdinaryWant {
		want[k] = v
	}
	for i := 0; i < collapsed/2; i++ {
		want[fmt.Sprintf("bad%d@x", i)] = 1
	}
	b3WantUIDs(t, ms, want)
	if st.collapsed != collapsed || st.empty != empty || st.skipped != 0 || st.failed != 0 {
		t.Fatalf("stats %+v: want %d collapsed, %d empty, nothing skipped or failed", st, collapsed, empty)
	}
	if took > 20*time.Second {
		t.Fatalf("hostile feed took %s", took)
	}
}

// Check round 2 item 1: concurrent hostile parses leave no goroutine
// behind once they return (the checker saw 16 walkers still spinning 30 s
// after 8 parses returned). The parse starts none, so the count returns
// to the baseline as soon as the parsing goroutines themselves exit.
func TestParseMeetingsConcurrentHostileNoLeak(t *testing.T) {
	from := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	evs, _, _ := b3HostileSeries()
	feed := b3Feed(evs...)
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ParseMeetings(strings.NewReader(feed), from, from.Add(24*time.Hour)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// wg.Done runs just before each goroutine exits; wait for the exits.
	for end := time.Now().Add(2 * time.Second); runtime.NumGoroutine() > before && time.Now().Before(end); {
		runtime.Gosched()
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d -> %d after 8 hostile parses returned", before, after)
	}
}

// The feed deadline backs up the step budget on a loaded box. With the
// deadline already spent no recurring series is expanded, and single
// events are still read: no wall clock decides the counts.
func TestParseMeetingsFeedDeadline(t *testing.T) {
	from := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	feed := b3Feed(b3OldHourly(3)...)
	ms, st, err := parseMeetings(context.Background(), strings.NewReader(feed), from, to, expandLimits{feedSteps: 1 << 40, feedTime: 0})
	if err != nil {
		t.Fatal(err)
	}
	b3WantUIDs(t, ms, map[string]int{"one@x": 1, "late@x": 1})
	if st.skipped != 4 || st.steps != 0 {
		t.Fatalf("stats %+v: want the weekly and 3 old series skipped, nothing walked", st)
	}

	// A deadline that runs out mid-walk stops the walk between iterator
	// calls. 300 old series are 60M steps (about 20 s idle) without it.
	lim := expandLimits{feedSteps: 1 << 40, feedTime: 50 * time.Millisecond}
	t0 := time.Now()
	ms, st, err = parseMeetings(context.Background(), strings.NewReader(b3Feed(b3OldHourly(300)...)), from, to, lim)
	took := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	b3WantUIDs(t, ms, b3OrdinaryWant)
	if st.skipped == 0 || st.steps >= 300*maxSeriesSteps {
		t.Fatalf("stats %+v: the deadline did not stop the expansion", st)
	}
	if took > 10*time.Second {
		t.Fatalf("50 ms deadline took %s", took)
	}
}

// expandSeries turns a panic inside the walk into an error for that one
// series (rrule-go has panics on hostile rules; walkableRule refuses the
// known ones, this catches the rest).
func TestExpandSeriesRecoversPanic(t *testing.T) {
	start := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	rule, err := rrule.StrToRRule("FREQ=DAILY")
	if err != nil {
		t.Fatal(err)
	}
	set := rrule.Set{}
	set.DTStart(start)
	set.RRule(rule)
	w := &seriesWalk{
		set: &set, endOf: func(time.Time) time.Time { panic("boom") },
		from: start, after: start, until: start.AddDate(0, 0, 3),
		maxSteps: 10, stop: func() bool { return false },
	}
	if _, _, err := expandSeries(w, rule.OrigOptions, start); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expandSeries err = %v, want the recovered panic", err)
	}
}

// b3WantUIDs checks the instance count per UID, and nothing else.
func b3WantUIDs(t *testing.T, ms []Meeting, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, m := range ms {
		got[m.UID]++
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// The expansion also honours the caller's context: a cancelled context
// returns at once, and one that expires mid-walk stops it between
// iterator calls.
func TestParseMeetingsContextLimit(t *testing.T) {
	from := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	feed := b3Feed(b3OldHourly(300)...) // 60M steps without a stop

	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseMeetingsContext(done, strings.NewReader(feed), from, to); err != context.Canceled {
		t.Fatalf("cancelled context -> %v, want context.Canceled", err)
	}

	ctx, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	t0 := time.Now()
	_, st, err := parseMeetings(ctx, strings.NewReader(feed), from, to, expandLimits{feedSteps: 1 << 40, feedTime: time.Hour})
	if err != context.DeadlineExceeded {
		t.Fatalf("expired context -> %v, want context.DeadlineExceeded", err)
	}
	if st.steps >= 300*maxSeriesSteps {
		t.Fatalf("stats %+v: the context did not stop the walk", st)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Fatalf("expired context took %s", d)
	}
}

// b3ParseGuarded runs parseMeetings on its own goroutine and fails the test
// with a message if it has not returned within d, so a rule that spins
// inside one rrule-go iterator call fails the test instead of hanging the
// package. (The spinning goroutine cannot be stopped; it dies with the
// test binary.)
func b3ParseGuarded(t *testing.T, d time.Duration, feed string, from, to time.Time) ([]Meeting, expandStats) {
	t.Helper()
	type result struct {
		ms  []Meeting
		st  expandStats
		err error
	}
	ch := make(chan result, 1)
	go func() {
		ms, st, err := parseMeetings(context.Background(), strings.NewReader(feed), from, to, b3NoDeadline)
		ch <- result{ms, st, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.ms, r.st
	case <-time.After(d):
		t.Fatalf("parse did not return within %s: a rule spins inside the iterator", d)
		return nil, expandStats{}
	}
}

// Check round 3 item 1(a): rrule-go accepts DTSTART= inside the RRULE
// value, and Set.RRule let it override the VEVENT's start, so the walk
// began at a time the classifier never saw. From 01:00, BYHOUR=1 with
// INTERVAL=2 is reachable (daily at 01:00); from the embedded 10:00 the
// hour stays even and the iterator never returned. The VEVENT's DTSTART
// now always wins: the series is walked from 01:00, and an embedded
// DTSTART no longer moves an ordinary weekly series either.
func TestParseMeetingsRuleDTStartIgnored(t *testing.T) {
	from := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	feed := b3VEvent(
		"UID:hr@x\r\nSUMMARY:h\r\nDTSTART:20261104T010000Z\r\nDTEND:20261104T013000Z\r\nRRULE:FREQ=HOURLY;INTERVAL=2;BYHOUR=1;DTSTART=20261104T100000Z\r\n",
		"UID:wk@x\r\nSUMMARY:w\r\nDTSTART:20261104T010000Z\r\nDTEND:20261104T013000Z\r\nRRULE:FREQ=WEEKLY;BYDAY=WE;DTSTART=20261104T100000Z\r\n",
	)
	ms, st := b3ParseGuarded(t, 10*time.Second, feed, from, to)
	b3WantUIDs(t, ms, map[string]int{"hr@x": 8, "wk@x": 2})
	for _, m := range ms {
		if m.Start.Hour() != 1 || m.Start.Minute() != 0 {
			t.Fatalf("%s instance at %s: want 01:00, the VEVENT's DTSTART", m.UID, m.Start)
		}
	}
	if st.collapsed != 0 || st.empty != 0 || st.failed != 0 || st.skipped != 0 {
		t.Fatalf("stats %+v: want both series walked", st)
	}
}

// Check round 3 item 1(b): rrule-go treats a zero DTSTART (0001-01-01
// 00:00:00 UTC) as unset and walks from time.Now() instead, while the
// classifier checked hour 0. HOURLY;INTERVAL=24 keeps the hour fixed, so
// BYHOUR=0 spins at every hour of the day but 00 UTC, and BYHOUR=12 at
// every hour but 12: one of the two hangs whatever the clock says. Such
// starts, and starts outside years 1 to 9998 (in their own zone or in
// UTC), are now shown as their first instance only.
func TestParseMeetingsZeroDTStart(t *testing.T) {
	from := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	to := from.Add(8 * 24 * time.Hour)
	feed := b3Feed(
		"UID:z0@x\r\nSUMMARY:z\r\nDTSTART:00010101T000000Z\r\nRRULE:FREQ=HOURLY;INTERVAL=24;BYHOUR=0\r\n",
		"UID:z12@x\r\nSUMMARY:z\r\nDTSTART:00010101T000000Z\r\nRRULE:FREQ=HOURLY;INTERVAL=24;BYHOUR=12\r\n",
		"UID:zfuzz@x\r\nSUMMARY:z\r\nDTSTART:00010101T000000Z\r\nRRULE:FREQ=HOURLY;INTERVAL=4;BYDAY=MO,TU,SA;BYHOUR=16,19,5\r\n",
		// Before the zero instant in UTC (Amsterdam LMT is UTC+00:19:32).
		"UID:ams@x\r\nSUMMARY:z\r\nDTSTART;TZID=Europe/Amsterdam:00010101T000000\r\nRRULE:FREQ=DAILY\r\n",
		// Past rrule-go's year search.
		"UID:end@x\r\nSUMMARY:z\r\nDTSTART:99991231T230000Z\r\nRRULE:FREQ=HOURLY\r\n",
		"UID:endd@x\r\nSUMMARY:z\r\nDTSTART;VALUE=DATE:99990101\r\nRRULE:FREQ=YEARLY\r\n",
	)
	ms, st := b3ParseGuarded(t, 10*time.Second, feed, from, to)
	b3WantUIDs(t, ms, b3OrdinaryWant) // each first instance lies outside the window
	if st.collapsed != 6 || st.empty != 0 || st.failed != 0 || st.skipped != 0 {
		t.Fatalf("stats %+v: want the 6 out-of-range starts collapsed", st)
	}

	// The edges themselves still walk: one second past the zero instant,
	// and a start in 9998.
	for _, c := range []struct {
		start time.Time
		why   string
	}{
		{time.Time{}, "dtstart-zero"},
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second), "dtstart-range"},
		{time.Date(1, 1, 1, 0, 0, 1, 0, time.UTC), ""},
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("E", 3600)), "dtstart-range"},
		{time.Date(9998, 12, 31, 22, 0, 0, 0, time.UTC), ""},
		{time.Date(9998, 12, 31, 23, 30, 0, 0, time.FixedZone("W", -3600)), "dtstart-range"},
		{time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), "dtstart-range"},
	} {
		if got := seriesStartReason(c.start); got != c.why {
			t.Errorf("seriesStartReason(%s) = %q, want %q", c.start, got, c.why)
		}
	}
}

// Check round 3 fix, third part: the rule is built with the VEVENT's start
// and the set is checked to walk from exactly that start before walking.
func TestSeriesRuleUsesClassifiedStart(t *testing.T) {
	start := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	rule, err := seriesRule("FREQ=HOURLY;INTERVAL=2;BYHOUR=1;DTSTART=20261104T100000Z", start)
	if err != nil {
		t.Fatal(err)
	}
	if !rule.OrigOptions.Dtstart.Equal(start) || !rule.Options.Dtstart.Equal(start) {
		t.Fatalf("rule starts %s / %s, want %s", rule.OrigOptions.Dtstart, rule.Options.Dtstart, start)
	}
	set := rrule.Set{}
	set.DTStart(start)
	set.RRule(rule)
	if !walksFrom(&set, start) {
		t.Fatal("walksFrom = false for the rule built from the classified start")
	}
	// A rule bound to another start (what Set.RRule did with an embedded
	// DTSTART) is caught.
	other, err := rrule.StrToRRule("FREQ=HOURLY;INTERVAL=2;BYHOUR=1;DTSTART=20261104T100000Z")
	if err != nil {
		t.Fatal(err)
	}
	set2 := rrule.Set{}
	set2.DTStart(start)
	set2.RRule(other)
	if walksFrom(&set2, start) {
		t.Fatal("walksFrom = true for a rule carrying its own DTSTART")
	}
	if walksFrom(&rrule.Set{}, start) {
		t.Fatal("walksFrom = true for a set without a rule")
	}
}
