package synth

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// ADR 0020 clause 2: until a device's previous state is seeded, the engine
// diffs nothing for it — not even the fault differ's first-poll raise,
// which is exactly the restart storm ADR 0017 exists to prevent.
func TestHeldEngineEmitsNothingUntilSeeded(t *testing.T) {
	e := NewEngine(NewFaultDiffer()).HoldUntilSeeded()
	if evs := e.Apply(faultSnap(t0, mmu)); len(evs) != 0 {
		t.Fatalf("unseeded device emitted %v", kinds(evs))
	}
	e.Seed("asc-1", []model.Facet{model.FaultSet{Faults: []model.Fault{mmu}}})

	// The standing fault was already known before the restart: no re-raise.
	if evs := e.Apply(faultSnap(t0.Add(time.Second), mmu)); len(evs) != 0 {
		t.Fatalf("seeded standing fault re-raised: %v", kinds(evs))
	}
	// A real transition after seeding is still seen, exactly once.
	evs := e.Apply(faultSnap(t0.Add(2 * time.Second)))
	if got := kinds(evs); len(got) != 1 || got[0] != "fault-cleared" {
		t.Fatalf("clear after seeding = %v, want one fault-cleared", got)
	}
}

// A transition that happened while the collector was down is reported on
// the first diff after seeding, against the state it had before.
func TestTransitionAcrossARestartIsEmittedOnce(t *testing.T) {
	e := NewEngine(NewSignalDiffer()).HoldUntilSeeded()
	e.Seed("asc-1", []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}})
	evs := e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 2 || got[1] != "mode-changed" {
		t.Fatalf("events = %v, want the normal→flash transition", got)
	}
	if evs := e.Apply(snapAt(t0.Add(time.Second), model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 3})); len(kinds(evs)) != 1 {
		t.Fatalf("second poll re-emitted the transition: %v", kinds(evs))
	}
}

// Seeding a device with no stored state is the legitimate first boot
// (ADR 0017): diffing proceeds with the ordinary first-observation rules.
func TestSeedingEmptyIsAFirstBoot(t *testing.T) {
	e := NewEngine(NewFaultDiffer()).HoldUntilSeeded()
	e.Seed("asc-1", nil)
	if evs := e.Apply(faultSnap(t0, mmu)); len(evs) != 1 {
		t.Fatalf("first boot events = %v, want the fault differ's first-poll raise", kinds(evs))
	}
}

// The change hook reports every facet the engine now remembers, except
// volatile kinds: detector samples change every poll, and persisting them
// would be a flash write per poll (ADR 0017 writes only on transitions).
func TestChangeHookReportsDurableKindsOnly(t *testing.T) {
	var got []model.Kind
	e := NewEngine(NewSignalDiffer(), NewDetectorDiffer()).WithChangeHook(func(_ string, k model.Kind, _ model.Facet) {
		got = append(got, k)
	})
	e.Apply(&model.Snapshot{DeviceID: "asc-1", SampledAt: t0, Facets: []model.Facet{
		model.SignalStatus{Mode: model.ModeNormal},
		model.DetectorSamples{Samples: []model.DetectorSample{{Channel: 1, VolumeCount: 5}}},
	}})
	if len(got) != 1 || got[0] != model.KindSignalStatus {
		t.Fatalf("change hook saw %v, want only signal-status", got)
	}
}

// What the durable layer stores must decode back to the identical facet,
// for every kind the engine persists.
func TestDecodeFacetRoundTripsEveryDurableKind(t *testing.T) {
	e := NewEngine(NewSignalDiffer(), NewFaultDiffer(), NewDMSDiffer(), NewDMSEnvironmentDiffer(),
		NewTrafficIntervalDiffer(), NewZoneIncidentDiffer(), NewZoneIntervalDiffer(), NewCCTVDiffer())
	facets := []model.Facet{
		model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 7, PreemptionActive: true, PreemptionSource: "rail"},
		model.FaultSet{Faults: []model.Fault{mmu, door}},
		model.DMSStatus{ControlMode: model.ControlLocal, ActiveSlot: 4, MessageText: "[jl3]SLOW"},
		model.DMSEnvironment{HumidityReported: true},
		intervals(tsAt, 12),
		set(inc("i-1", model.IncidentMajor, 90)),
		zones(pcpAt, 5),
		cam(model.CCTVControlLocal, model.CCTVTour{TourID: 2, State: model.TourRunning}),
	}
	for _, f := range facets {
		raw, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		back, err := e.DecodeFacet(f.FacetKind(), raw)
		if err != nil {
			t.Fatalf("%s: %v", f.FacetKind(), err)
		}
		if !reflect.DeepEqual(back, f) {
			t.Errorf("%s did not round-trip:\n got %#v\nwant %#v", f.FacetKind(), back, f)
		}
	}
	if _, err := e.DecodeFacet("no-such-kind", []byte(`{}`)); err == nil {
		t.Error("DecodeFacet accepted a kind with no differ")
	}
}
