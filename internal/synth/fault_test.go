package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func faultSnap(at time.Time, faults ...model.Fault) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at,
		Facets: []model.Facet{model.FaultSet{Faults: faults}}}
}

var (
	mmu      = model.Fault{ID: "mmu-fault", Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "MMU fault detected"}
	door     = model.Fault{ID: "cabinet-door", Severity: model.SeverityMinor, Category: model.CategoryCabinet, Description: "Cabinet door open"}
	lampOut  = model.Fault{ID: "lamp-out", Severity: model.SeverityMajor, Category: model.CategoryLamp, Description: "Signal head lamp failure"}
	powerOut = model.Fault{ID: "power-loss", Severity: model.SeverityMajor, Category: model.CategoryPower, Description: "Power loss / generator running"}
)

func TestFirstPollRaisesEverythingCurrentlyRaised(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	evs := e.Apply(faultSnap(t0, mmu, door))
	if len(evs) != 2 {
		t.Fatalf("first poll events = %v, want 2 raises", kinds(evs))
	}
	for _, ev := range evs {
		if ev.EventKind() != "fault-raised" {
			t.Errorf("unexpected %q", ev.EventKind())
		}
	}
}

func TestRaiseAndClear(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu))

	// door appears, mmu persists -> one raise, no clear.
	evs := e.Apply(faultSnap(t0.Add(time.Second), mmu, door))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 raise", kinds(evs))
	}
	r, ok := evs[0].(model.FaultRaised)
	if !ok || r.FaultID != "cabinet-door" || r.Severity != model.SeverityMinor {
		t.Fatalf("bad raise: %+v", evs[0])
	}

	// mmu disappears -> one clear.
	evs = e.Apply(faultSnap(t0.Add(2*time.Second), door))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 clear", kinds(evs))
	}
	c, ok := evs[0].(model.FaultCleared)
	if !ok || c.FaultID != "mmu-fault" {
		t.Fatalf("bad clear: %+v", evs[0])
	}
}

func TestUnchangedFaultSetEmitsNothing(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu, door))
	if evs := e.Apply(faultSnap(t0.Add(time.Second), mmu, door)); len(evs) != 0 {
		t.Fatalf("unchanged set must emit nothing, got %v", kinds(evs))
	}
}

// A fault whose attributes change while its ID stays raised is not a raise.
// Nothing consumes an "amended fault" event; inventing one is YAGNI.
func TestAttributeChangeIsNotARaise(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu))
	worse := mmu
	worse.Description = "MMU fault detected (escalated)"
	if evs := e.Apply(faultSnap(t0.Add(time.Second), worse)); len(evs) != 0 {
		t.Fatalf("attribute change must emit nothing, got %v", kinds(evs))
	}
}

// Determinism: gen-1 iterated a Go map here, so its raise/clear order was
// nondeterministic. This is a regression guard, not a style preference.
func TestEventOrderIsSortedByFaultID(t *testing.T) {
	for i := 0; i < 20; i++ { // map iteration order varies per run
		e := NewEngine(NewFaultDiffer())
		evs := e.Apply(faultSnap(t0, lampOut, mmu, door, powerOut))
		var ids []string
		for _, ev := range evs {
			ids = append(ids, ev.(model.FaultRaised).FaultID)
		}
		want := []string{"cabinet-door", "lamp-out", "mmu-fault", "power-loss"}
		if len(ids) != len(want) {
			t.Fatalf("ids = %v", ids)
		}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("raise order = %v, want sorted %v", ids, want)
			}
		}
	}
}

func TestClearOrderIsSortedByFaultID(t *testing.T) {
	for i := 0; i < 20; i++ {
		e := NewEngine(NewFaultDiffer())
		e.Apply(faultSnap(t0, lampOut, mmu, door, powerOut))
		evs := e.Apply(faultSnap(t0.Add(time.Second)))
		var ids []string
		for _, ev := range evs {
			ids = append(ids, ev.(model.FaultCleared).FaultID)
		}
		want := []string{"cabinet-door", "lamp-out", "mmu-fault", "power-loss"}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("clear order = %v, want sorted %v", ids, want)
			}
		}
	}
}

// THE IRON RULE. Gen-1's failure here was severe: on any SNMP error it
// returned a reading containing only a synthetic snmp-unreachable fault, and
// because diffFaults was a pure set-difference that CLEARED EVERY REAL FAULT,
// then re-raised them on recovery. A failed read must never clear a fault.
func TestFailedFaultReadNeverClears(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu, door))

	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindFaultSet, Err: "alarm OID unanswered"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}

	// Recovery with the same faults still raised: no re-raise, because prev
	// survived the failed poll.
	if evs := e.Apply(faultSnap(t0.Add(2*time.Second), mmu, door)); len(evs) != 0 {
		t.Fatalf("post-recovery events = %v, want none", kinds(evs))
	}
}
