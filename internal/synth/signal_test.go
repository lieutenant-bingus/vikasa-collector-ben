package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var t0 = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func snapAt(at time.Time, st model.SignalStatus) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at, Facets: []model.Facet{st}}
}

func kinds(evs []model.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventKind()
	}
	return out
}

func TestFirstPollEmitsOnlyStatusReport(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	evs := e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 1 || got[0] != "operational-status-report" {
		t.Fatalf("first poll events = %v, want [operational-status-report]", got)
	}
	rep := evs[0].(model.OperationalStatusReport)
	if rep.ActivePlanID != 3 || rep.Mode != model.ModeNormal || !rep.OccurredAt.Equal(t0) {
		t.Fatalf("bad report: %+v", rep)
	}
}

func TestTransitionsEmitChangeEvents(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))

	t1 := t0.Add(time.Second)
	evs := e.Apply(snapAt(t1, model.SignalStatus{
		Mode: model.ModeFlash, ActivePlanID: 7,
		PreemptionActive: true, PreemptionSource: "preempt-2",
	}))
	got := map[string]bool{}
	for _, k := range kinds(evs) {
		got[k] = true
	}
	for _, want := range []string{"operational-status-report", "mode-changed", "plan-changed", "preemption-activated"} {
		if !got[want] {
			t.Fatalf("missing %q in %v", want, kinds(evs))
		}
	}

	t2 := t1.Add(time.Second)
	evs = e.Apply(snapAt(t2, model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 7}))
	found := false
	for _, ev := range evs {
		if ev.EventKind() == "preemption-cleared" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected preemption-cleared in %v", kinds(evs))
	}
}

func TestFailedFacetSuspendsDiffing(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))

	// Poll 2: facet failed. No events at all for this facet.
	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindSignalStatus, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed facet must emit nothing, got %v", kinds(evs))
	}

	// Poll 3: recovered with same state. Must NOT re-emit transitions
	// against a zero value — prev survived the failed poll.
	evs := e.Apply(snapAt(t0.Add(2*time.Second), model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 1 || got[0] != "operational-status-report" {
		t.Fatalf("post-recovery events = %v, want [operational-status-report]", got)
	}
}

// The Engine must propagate the snapshot's device kind onto every event it
// synthesizes; a differ never sets it.
func TestEngineCopiesDeviceKindOntoEvents(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	snap := &model.Snapshot{
		DeviceID: "asc-1", DeviceKind: "asc", SampledAt: t0,
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}},
	}
	evs := e.Apply(snap)
	if len(evs) == 0 {
		t.Fatal("expected at least one event")
	}
	for _, ev := range evs {
		if got := ev.EventDeviceKind(); got != "asc" {
			t.Errorf("%s: EventDeviceKind() = %q, want %q", ev.EventKind(), got, "asc")
		}
	}
}
