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
		t.Fatalf("events = %v, want 1 gate-position-changed", kinds(evs))
	}
	pc, ok := evs[0].(model.GatePositionChanged)
	if !ok || pc.GateID != "WG-111" || pc.From != model.GatePositionClosed || pc.To != model.GatePositionOpening {
		t.Fatalf("bad position event: %+v", evs[0])
	}
	if pc.DeviceKind != "acs" {
		t.Fatalf("DeviceKind = %q, want acs", pc.DeviceKind)
	}

	manual := opening
	manual.Mode = model.GateModeManual
	evs = e.Apply(gateSnap(t0.Add(2*time.Second), manual))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 gate-mode-changed", kinds(evs))
	}
	mc, ok := evs[0].(model.GateModeChanged)
	if !ok || mc.From != model.GateModeAuto || mc.To != model.GateModeManual {
		t.Fatalf("bad mode event: %+v", evs[0])
	}
}

func TestGateCombinedTransitions(t *testing.T) {
	e := NewEngine(NewGateDiffer())
	e.Apply(gateSnap(t0, gateClosed))

	both := gateClosed
	both.Position = model.GatePositionOpened
	both.Mode = model.GateModeOffline
	evs := e.Apply(gateSnap(t0.Add(time.Second), both))
	if got := kinds(evs); len(got) != 2 {
		t.Fatalf("events = %v, want position then mode", got)
	}
	if _, ok := evs[0].(model.GatePositionChanged); !ok {
		t.Fatalf("first event = %T, want GatePositionChanged", evs[0])
	}
	if _, ok := evs[1].(model.GateModeChanged); !ok {
		t.Fatalf("second event = %T, want GateModeChanged", evs[1])
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
	pc := evs[0].(model.GatePositionChanged)
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
