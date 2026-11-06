package maxtime

import (
	"maps"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpapitest"
)

const callPhaseKey2 = "GET /maxtime/api/mibs/auxVehicleDetectorCallPhase-2/"

// withPlan is the healthy answer on detector plan plan, with channels 1, 2,
// 3 and 31 answering: in the lab assignment they call phases 1, none, 2
// and 8.
func withPlan(plan string) map[string]rows {
	o := healthy()
	o[mibVolume] = rows{"1": "5", "2": "0", "3": "7", "31": "2"}
	o[mibOccupancy] = rows{"1": "4", "2": "0", "3": "6", "31": "2"}
	if plan != "" {
		o[mibActiveDetectorPlan] = rows{"1": plan}
	}
	return o
}

func phasesOf(t *testing.T, snap *model.Snapshot) map[uint32]uint8 {
	t.Helper()
	f, ok := snap.Facet(model.KindDetectorSamples)
	if !ok {
		t.Fatalf("no detector facet: %+v", snap.Errors)
	}
	out := map[uint32]uint8{}
	for _, s := range f.(model.DetectorSamples).Samples {
		out[s.Channel] = s.PhaseServed
	}
	return out
}

func callPhaseASC(t *testing.T, plan string) (*asc, *httpapitest.Static, *time.Time) {
	t.Helper()
	s := httpapitest.LoadFixture(t, "testdata/asc-lab-callphase.json")
	s.Set(batchKey, http.StatusOK, answer(t, withPlan(plan)))
	now := time.Date(2026, 11, 5, 16, 0, 0, 0, time.UTC)
	a := newASC(s)
	a.calls = &callPhases{}
	a.now = func() time.Time { return now }
	return a, s, &now
}

func TestCallPhaseFromLabAssignment(t *testing.T) {
	a, s, _ := callPhaseASC(t, "2")
	snap, err := a.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]uint8{1: 1, 2: 0, 3: 2, 31: 8}
	if got := phasesOf(t, snap); !maps.Equal(got, want) {
		t.Errorf("phases = %v, want %v", got, want)
	}
	// Within callPhaseRefresh and on the same plan, the next poll does not
	// re-read the assignment.
	if _, err := a.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range s.Requests() {
		if "GET "+r.Path == callPhaseKey2 {
			n++
		}
	}
	if n != 1 {
		t.Errorf("assignment read %d times in two polls, want 1", n)
	}
}

// The call-phase poll as record-fixture captured it on the lab controller
// (firmware 2.15.1, 2026-11-05, detector plan 2), replayed unmodified: the
// batch answer names the plan and the second exchange is its assignment.
// The same run published these phases on detector-report and
// detector-transition; every other channel calls phase 0 and carries none.
func TestCallPhaseRecordedPollGolden(t *testing.T) {
	s := httpapitest.LoadFixture(t, "testdata/asc-lab-callphase.json")
	if _, ok := s.Responses[batchKey]; !ok {
		t.Fatalf("fixture has no exchange for today's batch request %q", batchKey)
	}
	a := newASC(s)
	a.calls = &callPhases{}
	snap, err := a.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint32]uint8{}
	for ch, ph := range phasesOf(t, snap) {
		if ph != 0 {
			got[ch] = ph
		}
	}
	want := map[uint32]uint8{
		1: 1,
		3: 2, 4: 2, 5: 2, 6: 2,
		21: 6, 22: 6,
		31: 8, 32: 8, 33: 8, 34: 8,
	}
	if !maps.Equal(got, want) {
		t.Errorf("phases = %v, want %v", got, want)
	}
	if n := len(s.Requests()); n != 2 {
		t.Errorf("%d requests, want the poll and plan 2's assignment", n)
	}
}

// A plan switch whose assignment cannot be read leaves every phase unknown:
// the old plan's phases no longer apply.
func TestCallPhasePlanChangeWithoutAnswerIsUnknown(t *testing.T) {
	a, s, _ := callPhaseASC(t, "2")
	if _, err := a.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.Set(batchKey, http.StatusOK, answer(t, withPlan("3"))) // plan 3: not in the fixture, 404
	snap, err := a.Read(t.Context())
	if err != nil {
		t.Fatalf("an unreadable assignment must not fail the poll: %v", err)
	}
	for ch, ph := range phasesOf(t, snap) {
		if ph != 0 {
			t.Errorf("channel %d phase %d under plan 3, want unknown (0)", ch, ph)
		}
	}
	if len(snap.Errors) != 0 {
		t.Errorf("errors = %+v, want none: phase-served is optional", snap.Errors)
	}
}

// On the same plan, a failed refresh keeps the assignment last read.
func TestCallPhaseRefreshFailureKeepsTheSamePlan(t *testing.T) {
	a, s, now := callPhaseASC(t, "2")
	if _, err := a.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	delete(s.Responses, callPhaseKey2)
	*now = now.Add(callPhaseRefresh + time.Minute)
	snap, err := a.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := phasesOf(t, snap)[3]; got != 2 {
		t.Errorf("channel 3 phase = %d after a failed refresh, want 2 as last read", got)
	}
}

// No active plan in the answer: nothing to read the assignment for.
func TestCallPhaseWithoutPlanIsUnknown(t *testing.T) {
	a, s, _ := callPhaseASC(t, "")
	snap, err := a.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for ch, ph := range phasesOf(t, snap) {
		if ph != 0 {
			t.Errorf("channel %d phase %d without a plan, want 0", ch, ph)
		}
	}
	if n := len(s.Requests()); n != 1 {
		t.Errorf("%d requests, want only the poll", n)
	}
}

// A detector row carries the phase only when it was logged under the
// assignment read; phase and pedestrian-detector rows never do.
func TestLogDetectorRowsCarryPhaseServedFromTheirTime(t *testing.T) {
	s := &httpapitest.Static{}
	body := `<EventResponses><EventResponse>` +
		`<Event ID="100" TimeStamp="09-08-2026 09:59:27.8" EventTypeID="82" Parameter="3"/>` +
		`<Event ID="101" TimeStamp="09-08-2026 09:59:28.0" EventTypeID="82" Parameter="3"/>` +
		`<Event ID="102" TimeStamp="09-08-2026 09:59:28.1" EventTypeID="1" Parameter="2"/>` +
		`<Event ID="103" TimeStamp="09-08-2026 09:59:28.2" EventTypeID="89" Parameter="3"/>` +
		`<Event ID="104" TimeStamp="09-08-2026 09:59:28.3" EventTypeID="81" Parameter="9"/>` +
		`</EventResponse></EventResponses>`
	s.Set("GET "+defaultLogPath+"?"+url.Values{"since": {"09-08-2026 09:59:27.0"}}.Encode(), http.StatusOK, body)
	read := time.Date(2026, 9, 8, 13, 59, 28, 0, time.UTC) // 09:59:28.0 EDT
	l := newLog(s, true, read.Add(time.Minute))
	l.calls = &callPhases{plan: 2, phases: map[uint32]uint8{3: 2}, since: read, fetched: read}
	evs, _, err := l.read(t.Context(), cursor(t, 99, read.Add(-time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	want := []uint8{0, 2, 0, 0, 0} // before the read; joined; phase row; ped detector; calls none
	if len(evs) != len(want) {
		t.Fatalf("%d events, want %d", len(evs), len(want))
	}
	for i, ev := range evs {
		if got := ev.(model.HighResEvent).PhaseServed; got != want[i] {
			t.Errorf("row %d PhaseServed = %d, want %d", 100+i, got, want[i])
		}
	}
}
