package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func gateSnap(at time.Time, gates ...model.GateReading) *model.Snapshot {
	return &model.Snapshot{
		DeviceID: "acs-1", DeviceKind: "acs", SampledAt: at,
		Facets: []model.Facet{model.GateBank{Gates: gates}},
	}
}

var gateClosed = model.GateReading{
	ID: "WG-111", Kind: model.GateKindWarning,
	Mode: model.GateModeAuto, Position: model.GatePositionClosed, Raw: 0x0025,
}

func TestGateFirstPollEmitsNothing(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	if evs := e.Apply(gateSnap(t0, gateClosed)); len(evs) != 0 {
		t.Fatalf("first poll must emit nothing, got %v", kinds(evs))
	}
}

func TestGateNoChangeEmitsNothing(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	e.Apply(gateSnap(t0, gateClosed))
	if evs := e.Apply(gateSnap(t0.Add(time.Second), gateClosed)); len(evs) != 0 {
		t.Fatalf("unchanged bank must emit nothing, got %v", kinds(evs))
	}
}

func TestGateAxesChangeIndependently(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	e.Apply(gateSnap(t0, gateClosed))

	opening := gateClosed
	opening.Position = model.GatePositionOpening
	opening.Raw = 0x0035
	evs := e.Apply(gateSnap(t0.Add(time.Second), opening))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 gate-status-changed", kinds(evs))
	}
	pc, ok := evs[0].(model.GateStatusChanged)
	if !ok || pc.GateID != "WG-111" || pc.PreviousPosition != model.GatePositionClosed || pc.Position != model.GatePositionOpening {
		t.Fatalf("bad position event: %+v", evs[0])
	}
	if pc.Mode != model.GateModeAuto || pc.PreviousMode != model.GateModeAuto {
		t.Fatalf("mode rode along as %+v -> %+v", pc.PreviousMode, pc.Mode)
	}
	if pc.DeviceKind != "acs" {
		t.Fatalf("DeviceKind = %q, want acs", pc.DeviceKind)
	}

	manual := opening
	manual.Mode = model.GateModeManual
	evs = e.Apply(gateSnap(t0.Add(2*time.Second), manual))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 gate-status-changed", kinds(evs))
	}
	mc, ok := evs[0].(model.GateStatusChanged)
	if !ok || mc.PreviousMode != model.GateModeAuto || mc.Mode != model.GateModeManual {
		t.Fatalf("bad mode event: %+v", evs[0])
	}
	if mc.Position != model.GatePositionOpening {
		t.Fatalf("position = %v, want opening", mc.Position)
	}
}

func TestGateCombinedTransitions(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	e.Apply(gateSnap(t0, gateClosed))

	both := gateClosed
	both.Position = model.GatePositionOpened
	both.Mode = model.GateModeOffline
	evs := e.Apply(gateSnap(t0.Add(time.Second), both))
	if got := kinds(evs); len(got) != 1 || got[0] != "gate-status-changed" {
		t.Fatalf("events = %v, want one gate-status-changed", got)
	}
	st := evs[0].(model.GateStatusChanged)
	if st.Position != model.GatePositionOpened || st.Mode != model.GateModeOffline {
		t.Fatalf("status = %+v", st)
	}
}

func TestGateTwoGatesIndependent(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	a := gateClosed
	b := model.GateReading{
		ID: "BG-114", Kind: model.GateKindBarrier,
		Mode: model.GateModeAuto, Position: model.GatePositionClosed, Raw: 0x0021,
	}
	e.Apply(gateSnap(t0, a, b))

	bOpen := b
	bOpen.Position = model.GatePositionOpened
	evs := e.Apply(gateSnap(t0.Add(time.Second), a, bOpen))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want only BG-114", kinds(evs))
	}
	pc := evs[0].(model.GateStatusChanged)
	if pc.GateID != "BG-114" {
		t.Fatalf("GateID = %q", pc.GateID)
	}
}

func TestGateFailedReadEmitsNothing(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	e.Apply(gateSnap(t0, gateClosed))
	snap := &model.Snapshot{
		DeviceID: "acs-1", DeviceKind: "acs", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindGateBank, Err: "srtp timeout"}},
	}
	if evs := e.Apply(snap); len(evs) != 0 {
		t.Fatalf("failed facet must suspend differ, got %v", kinds(evs))
	}
}

func TestGateModePairTransitions(t *testing.T) {
	modes := []model.GateMode{
		model.GateModeOffline,
		model.GateModeAuto,
		model.GateModeManual,
		model.GateModeManualNonInterlocked,
	}
	for _, from := range modes {
		for _, to := range modes {
			if from == to {
				continue
			}
			e := NewEngine(NewGateDiffer())
			start := gateClosed
			start.Mode = from
			e.Apply(gateSnap(t0, start))
			next := start
			next.Mode = to
			evs := e.Apply(gateSnap(t0.Add(time.Second), next))
			if len(evs) != 1 {
				t.Fatalf("%s -> %s: events = %v", from, to, kinds(evs))
			}
			st, ok := evs[0].(model.GateStatusChanged)
			if !ok || st.PreviousMode != from || st.Mode != to {
				t.Fatalf("%s -> %s: %+v", from, to, evs[0])
			}
			if st.Position != model.GatePositionClosed || st.PreviousPosition != model.GatePositionClosed {
				t.Fatalf("%s -> %s moved position: %+v", from, to, st)
			}
		}
	}
}

func TestGateNewIDMidRunEmitsNothing(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	e.Apply(gateSnap(t0, gateClosed))
	newbie := model.GateReading{
		ID: "WG-112", Kind: model.GateKindWarning,
		Mode: model.GateModeAuto, Position: model.GatePositionClosed,
	}
	if evs := e.Apply(gateSnap(t0.Add(time.Second), gateClosed, newbie)); len(evs) != 0 {
		t.Fatalf("new gate id must not fabricate a transition, got %v", kinds(evs))
	}
}
