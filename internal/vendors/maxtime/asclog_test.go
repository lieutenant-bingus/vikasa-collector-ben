package maxtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpapitest"
)

const logFixture = "testdata/asclog-sample.json"

var newYork = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return l
}()

func newLog(c httpapi.Client, since bool, now time.Time) *ascLog {
	return &ascLog{
		api: &mibAPI{client: c, prefix: "/maxtime", batch: true},
		cfg: logConfig{path: defaultLogPath, interval: 15 * time.Second, loc: newYork, since: since},
		now: func() time.Time { return now },
	}
}

func cursor(t *testing.T, id uint64, at time.Time) []byte {
	t.Helper()
	b, err := json.Marshal(logCursor{ID: id, At: at})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeCursor(t *testing.T, b []byte) logCursor {
	t.Helper()
	var c logCursor
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("cursor %q: %v", b, err)
	}
	return c
}

// First contact never replays the ring: no events, and a cursor at its end.
func TestLogFirstReadPrimesAtTheEnd(t *testing.T) {
	l := newLog(httpapitest.LoadFixture(t, logFixture), true, fixedNow())
	evs, next, err := l.read(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("first read published %d rows of history", len(evs))
	}
	c := decodeCursor(t, next)
	want := time.Date(2026, 9, 8, 15, 22, 40, 500_000_000, time.UTC) // 11:22:40.5 EDT
	if c.ID != 3722593 || !c.At.Equal(want) {
		t.Errorf("cursor = %+v, want 3722593 at %v", c, want)
	}
}

// After a cursor, every later row is published once, oldest first, with the
// controller's own timestamp converted from its zone.
func TestLogReadsRowsAfterTheCursor(t *testing.T) {
	l := newLog(httpapitest.LoadFixture(t, logFixture), false, fixedNow())
	evs, next, err := l.read(t.Context(), cursor(t, 3712652, time.Date(2026, 9, 8, 14, 0, 3, 300_000_000, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	// Rows 3712653, 3712654, 3712655, 3712663, then the five at 11:22:40.5.
	if len(evs) != 9 {
		t.Fatalf("got %d events, want 9", len(evs))
	}
	first := evs[0].(model.HighResEvent)
	if first.Code != 305 || first.Param != 20 || !first.OccurredAt.Equal(time.Date(2026, 9, 8, 14, 0, 3, 300_000_000, time.UTC)) {
		t.Errorf("first = %+v", first)
	}
	if first.Class() != model.HighResUnmapped {
		t.Errorf("MaxTime code 305 must reach the bus raw, as unmapped; class = %d", first.Class())
	}
	ovl := evs[3].(model.HighResEvent)
	if ovl.Code != 61 || ovl.Param != 1 || ovl.Class() != model.HighResOverlap {
		t.Errorf("row 3712663 = %+v, want overlap 1 begin green", ovl)
	}
	if c := decodeCursor(t, next); c.ID != 3722593 {
		t.Errorf("cursor id = %d, want 3722593", c.ID)
	}
	// Reading again from the new cursor publishes nothing.
	again, next2, err := l.read(t.Context(), next)
	if err != nil || len(again) != 0 || string(next2) != string(next) {
		t.Errorf("re-read = %d events, cursor %s, err %v; want none, unchanged", len(again), next2, err)
	}
}

// With since on, the request carries the cursor's wall time in the
// controller's zone and format; the ID filter still decides what is new.
func TestLogAsksSinceTheCursor(t *testing.T) {
	s := &httpapitest.Static{}
	body := `<EventResponses><EventResponse>` +
		`<Event ID="100" TimeStamp="09-08-2026 09:59:27.8" EventTypeID="1" Parameter="2"/>` +
		`<Event ID="101" TimeStamp="09-08-2026 09:59:28.0" EventTypeID="7" Parameter="2"/>` +
		`</EventResponse></EventResponses>`
	s.Set("GET "+defaultLogPath+"?"+url.Values{"since": {"09-08-2026 09:59:27.8"}}.Encode(), http.StatusOK, body)
	l := newLog(s, true, time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC))
	evs, _, err := l.read(t.Context(), cursor(t, 100, time.Date(2026, 9, 8, 13, 59, 27, 800_000_000, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].(model.HighResEvent).Code != 7 {
		t.Errorf("events = %+v, want only row 101", evs)
	}
}

// Autumn DST: the controller relogs 01:00-01:59. A cursor from the first
// pass asks from an hour earlier, so the second pass is not filtered away.
func TestLogSinceCoversTheRepeatedHour(t *testing.T) {
	at := time.Date(2026, 11, 1, 5, 59, 0, 0, time.UTC) // 01:59 EDT
	now := time.Date(2026, 11, 1, 6, 5, 0, 0, time.UTC) // 01:05 EST
	if got := newLog(nil, true, now).sinceFor(at); got != "11-01-2026 00:59:00.0" {
		t.Errorf("since = %q, want an hour before the cursor's wall time", got)
	}
	earlier := at.Add(-2 * time.Hour) // 23:59 EDT, before the change
	if got := newLog(nil, true, earlier.Add(time.Minute)).sinceFor(earlier); got != "10-31-2026 23:59:00.0" {
		t.Errorf("since without a zone change = %q", got)
	}
}

// A controller that rebooted renumbers from low IDs; rows after the
// cursor's instant are the new ones.
func TestLogResyncsAfterRenumbering(t *testing.T) {
	l := newLog(httpapitest.LoadFixture(t, logFixture), false, fixedNow())
	evs, next, err := l.read(t.Context(), cursor(t, 9_000_000, time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 11 { // 3712647 (10:00:00.3 EDT) onward
		t.Fatalf("got %d events after renumbering, want 11", len(evs))
	}
	if c := decodeCursor(t, next); c.ID != 3722593 {
		t.Errorf("cursor id = %d, want the renumbered log's end", c.ID)
	}
}

func TestLogBoundsOneRead(t *testing.T) {
	var b strings.Builder
	b.WriteString("<EventResponses><EventResponse>")
	for i := 1; i <= maxEventsPerRead+500; i++ {
		fmt.Fprintf(&b, `<Event ID="%d" TimeStamp="09-08-2026 09:59:27.8" EventTypeID="82" Parameter="1"/>`, i)
	}
	b.WriteString("</EventResponse></EventResponses>")
	s := &httpapitest.Static{}
	s.Set("GET "+defaultLogPath, http.StatusOK, b.String())
	l := newLog(s, false, fixedNow())
	evs, next, err := l.read(t.Context(), cursor(t, 0+1, time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != maxEventsPerRead {
		t.Fatalf("got %d, want %d", len(evs), maxEventsPerRead)
	}
	if c := decodeCursor(t, next); c.ID != maxEventsPerRead+1 {
		t.Errorf("cursor id = %d, want the last row handed out", c.ID)
	}
}

// A row that does not parse is skipped, so it cannot pin the cursor.
func TestLogSkipsAMalformedRow(t *testing.T) {
	s := &httpapitest.Static{}
	s.Set("GET "+defaultLogPath, http.StatusOK, `<EventResponses><EventResponse>`+
		`<Event ID="11" TimeStamp="garbage" EventTypeID="1" Parameter="2"/>`+
		`<Event ID="12" TimeStamp="09-08-2026 09:59:28.0" EventTypeID="99999" Parameter="2"/>`+
		`<Event ID="13" TimeStamp="09-08-2026 09:59:28.1" EventTypeID="8" Parameter="2"/>`+
		`</EventResponse></EventResponses>`)
	evs, next, err := newLog(s, false, fixedNow()).read(t.Context(), cursor(t, 10, time.Time{}))
	if err != nil || len(evs) != 1 || decodeCursor(t, next).ID != 13 {
		t.Errorf("events %+v cursor %s err %v; want row 13 only", evs, next, err)
	}
}

func TestLogUnreadableCursorPrimes(t *testing.T) {
	l := newLog(httpapitest.LoadFixture(t, logFixture), true, fixedNow())
	evs, next, err := l.read(t.Context(), []byte("not json"))
	if err != nil || len(evs) != 0 || decodeCursor(t, next).ID != 3722593 {
		t.Errorf("events %d cursor %s err %v; want a fresh prime", len(evs), next, err)
	}
}

func TestLogFailuresAreErrors(t *testing.T) {
	s := &httpapitest.Static{}
	if _, _, err := newLog(s, false, fixedNow()).read(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing endpoint: err = %v", err)
	}
	s.Set("GET "+defaultLogPath, http.StatusOK, "<EventResponses><Event")
	if _, _, err := newLog(s, false, fixedNow()).read(t.Context(), nil); err == nil {
		t.Error("truncated XML: want an error")
	}
}

// The log authenticates exactly as the state poll does.
func TestLogUsesTheConfiguredAuth(t *testing.T) {
	s := httpapitest.LoadFixture(t, logFixture)
	l := newLog(s, false, fixedNow())
	l.api.auth, l.api.username, l.api.password = authBasic, "u", "p"
	if _, _, err := l.read(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if h := s.Requests()[0].Header.Get("Authorization"); !strings.HasPrefix(h, "Basic ") {
		t.Errorf("Authorization = %q", h)
	}
}

func TestParseLogBlock(t *testing.T) {
	cfg, err := parseHTTPBlock(block(map[string]any{"base_url": "http://h", "asclog": map[string]any{
		"timezone": "America/Chicago", "interval": "30s", "timeout": "90s", "path": "/maxtime/v1/asclog/xml/full", "since": false,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if l := cfg.log; l == nil || l.loc.String() != "America/Chicago" || l.interval != 30*time.Second || l.timeout != 90*time.Second ||
		l.path != "/maxtime/v1/asclog/xml/full" || l.since {
		t.Errorf("log = %+v", cfg.log)
	}
	cfg, err = parseHTTPBlock(block(map[string]any{"base_url": "http://h", "asclog": map[string]any{"enabled": false}}))
	if err != nil || cfg.log != nil {
		t.Errorf("disabled: %+v, %v", cfg.log, err)
	}
	for name, tc := range map[string]struct {
		log  any
		want string
	}{
		"not a map":    {true, "must be a mapping"},
		"no timezone":  {map[string]any{}, "timezone required"},
		"bad timezone": {map[string]any{"timezone": "Mars/Base"}, "Mars/Base"},
		"short":        {map[string]any{"timezone": "UTC", "interval": "100ms"}, "at least 1s"},
		"bad timeout":  {map[string]any{"timezone": "UTC", "timeout": "0s"}, "positive duration"},
		"query path":   {map[string]any{"timezone": "UTC", "path": "/v1/asclog?x"}, "no query"},
		"unknown":      {map[string]any{"timezone": "UTC", "url": "http://h"}, "url"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseHTTPBlock(block(map[string]any{"base_url": "http://h", "asclog": tc.log}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// With the log configured the built adapter is an EventReader and says so;
// without it, it is state-only, so no event loop starts.
func TestRegisteredWithLog(t *testing.T) {
	r := adapter.NewRegistry()
	RegisterTo(r)
	a, err := r.Build("maxtime", "asc", "asc-1", block(map[string]any{
		"base_url": "http://127.0.0.1:1", "asclog": map[string]any{"timezone": "UTC"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	er, ok := a.(adapter.EventReader)
	if !ok || !a.Descriptor().Caps.Has(adapter.CapEvents) || er.EventInterval() != 15*time.Second {
		t.Errorf("built %T caps %v", a, a.Descriptor().Caps)
	}
	b, err := r.Build("maxtime", "asc", "asc-2", block(map[string]any{"base_url": "http://127.0.0.1:1"}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if b.Descriptor().Caps.Has(adapter.CapEvents) {
		t.Error("an adapter with no log configured must not declare CapEvents")
	}
}

// A log that is empty on first contact primes at ID 0; the rows it gains
// afterwards are published, not taken for another first contact.
func TestLogEmptyPrimeThenRows(t *testing.T) {
	s := &httpapitest.Static{}
	s.Set("GET "+defaultLogPath, http.StatusOK, `<EventResponses><EventResponse></EventResponse></EventResponses>`)
	l := newLog(s, false, fixedNow())
	evs, next, err := l.read(t.Context(), nil)
	if err != nil || len(evs) != 0 {
		t.Fatalf("empty prime: %d events, err %v", len(evs), err)
	}
	if c := decodeCursor(t, next); c.ID != 0 || c.At.IsZero() {
		t.Fatalf("empty prime cursor = %+v, want ID 0 at now", c)
	}
	s.Set("GET "+defaultLogPath, http.StatusOK, `<EventResponses><EventResponse>`+
		`<Event ID="1" TimeStamp="11-04-2026 09:00:00.0" EventTypeID="1" Parameter="2"/>`+
		`<Event ID="2" TimeStamp="11-04-2026 09:00:00.1" EventTypeID="7" Parameter="2"/>`+
		`</EventResponse></EventResponses>`)
	evs, next, err = l.read(t.Context(), next)
	if err != nil || len(evs) != 2 || decodeCursor(t, next).ID != 2 {
		t.Errorf("after empty prime: %d events, cursor %s, err %v; want both rows", len(evs), next, err)
	}
}

const labLogFixture = "testdata/asclog-lab-2.15.1.json"

// The lab controller (firmware 2.15.1, recorded with record-fixture): 6165
// rows, IDs from 1, timestamps in America/New_York (EST on the day).
func TestLabLogPrimesAtTheEnd(t *testing.T) {
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)
	evs, next, err := newLog(httpapitest.LoadFixture(t, labLogFixture), false, now).read(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("first read published %d rows of history", len(evs))
	}
	want := time.Date(2026, 11, 4, 19, 55, 15, 400_000_000, time.UTC) // 14:55:15.4 EST
	if c := decodeCursor(t, next); c.ID != 6165 || !c.At.Equal(want) {
		t.Errorf("cursor = %+v, want 6165 at %v", c, want)
	}
}

func TestLabLogClassesAfterACursor(t *testing.T) {
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)
	l := newLog(httpapitest.LoadFixture(t, labLogFixture), false, now)
	evs, _, err := l.read(t.Context(), cursor(t, 6146, time.Date(2026, 11, 4, 19, 53, 56, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 19 { // 6147..6165
		t.Fatalf("got %d events, want 19", len(evs))
	}
	for i, want := range map[int]struct {
		code, param uint16
		class       model.HighResClass
	}{
		0: {13, 2, model.HighResUnmapped},      // not in the pinned release
		4: {150, 5, model.HighResCoordination}, // cycle state 5
		5: {316, 110, model.HighResUnmapped},   // MaxTime vendor range
		8: {150, 7, model.HighResCoordination},
	} {
		e := evs[i].(model.HighResEvent)
		if e.Code != want.code || e.Param != want.param || e.Class() != want.class {
			t.Errorf("event %d = code %d param %d class %d, want %+v", i, e.Code, e.Param, e.Class(), want)
		}
	}
}
