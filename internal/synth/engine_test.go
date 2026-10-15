package synth

import (
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// An adapter that returns a facet AND records a read failure for the same
// kind has said "I could not read this". The failure wins: diffing the
// facet would turn a value the adapter itself distrusts into a transition.
func TestApplyIgnoresAFacetItsSnapshotReportsFailed(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))

	contradictory := snapAt(t0.Add(time.Second), model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 3})
	contradictory.Errors = []model.FacetError{{Kind: model.KindSignalStatus, Err: "partial read"}}
	if evs := e.Apply(contradictory); len(evs) != 0 {
		t.Fatalf("a facet reported as failed was diffed: %v", kinds(evs))
	}

	// prev survived: the next good poll diffs against normal, not flash.
	evs := e.Apply(snapAt(t0.Add(2*time.Second), model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 2 || got[1] != "mode-changed" {
		t.Fatalf("recovery events = %v, want the normal→flash transition", got)
	}
}

// foreignSignal claims the signal-status kind without being model.SignalStatus,
// the shape a third-party adapter could plausibly ship.
type foreignSignal struct{}

func (foreignSignal) FacetKind() model.Kind { return model.KindSignalStatus }

// Differs assert their facet's concrete type. A facet of the right Kind but
// the wrong type used to panic the poll loop; it is now rejected, reported,
// and treated as no evidence at all.
func TestApplyRejectsAWrongTypedFacet(t *testing.T) {
	cases := []struct {
		name  string
		facet model.Facet
		want  string
	}{
		{"pointer to the right type", &model.SignalStatus{Mode: model.ModeFlash}, "*model.SignalStatus"},
		{"foreign type, same kind", foreignSignal{}, "synth.foreignSignal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rejected []string
			e := NewEngine(NewSignalDiffer()).WithRejectHook(func(deviceID string, k model.Kind, got string) {
				rejected = append(rejected, deviceID+"|"+string(k)+"|"+got)
			})
			e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal}))

			bad := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second), Facets: []model.Facet{tc.facet}}
			if evs := e.Apply(bad); len(evs) != 0 {
				t.Fatalf("wrong-typed facet produced %v", kinds(evs))
			}
			if len(rejected) != 1 || !strings.Contains(rejected[0], "asc-1|") || !strings.HasSuffix(rejected[0], tc.want) {
				t.Fatalf("reject hook got %v, want one report naming asc-1 and %s", rejected, tc.want)
			}

			// prev was not replaced by the bad facet.
			evs := e.Apply(snapAt(t0.Add(2*time.Second), model.SignalStatus{Mode: model.ModeFlash}))
			if got := kinds(evs); len(got) != 2 || got[1] != "mode-changed" {
				t.Fatalf("next good poll = %v, want normal→flash against the pre-rejection state", got)
			}
		})
	}
}

// Registry panics on a duplicate registration; the engine silently kept
// the last differ. Two differs for one kind is a wiring bug either way.
func TestNewEnginePanicsOnDuplicateKind(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, string(model.KindSignalStatus)) {
			t.Fatalf("panic = %q, want one naming the duplicated kind", msg)
		}
	}()
	NewEngine(NewSignalDiffer(), NewSignalDiffer())
}
