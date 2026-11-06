package maxtime

import (
	"maps"
	"net/http"
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

// callPhaseASC loads the recorded poll, then replaces the batch with
// withPlan. The recording is one real poll on plan 2 and holds that
// batch as the controller sent it; the plan-change and no-plan tests
// need a batch the recording does not contain, so they keep this Set.
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

// TestCallPhaseFromRecordedPoll replays the lab recording with no answer
// substituted. The detector facet carries the plan-2 call phases, and a
// channel that calls none stays 0.
func TestCallPhaseFromRecordedPoll(t *testing.T) {
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
	if len(snap.Errors) != 0 {
		t.Errorf("snapshot errors: %+v", snap.Errors)
	}
	got := phasesOf(t, snap)
	want := make(map[uint32]uint8, len(got))
	for ch := range got {
		want[ch] = 0
	}
	want[1] = 1
	for _, ch := range []uint32{3, 4, 5, 6} {
		want[ch] = 2
	}
	want[21], want[22] = 6, 6
	for _, ch := range []uint32{31, 32, 33, 34} {
		want[ch] = 8
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
