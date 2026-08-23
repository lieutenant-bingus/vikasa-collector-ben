package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var camAt = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func camBase() model.Base {
	return model.Base{DeviceID: "cam-03", DeviceKind: "cctv", OccurredAt: camAt}
}

func cam(mode model.CCTVControlMode, tours ...model.CCTVTour) model.CCTVStatus {
	return model.CCTVStatus{ControlMode: mode, Tours: tours}
}

func TestCCTVDiffer_FirstObservationEmitsNothing(t *testing.T) {
	// Transition events need a known prior state; there is none on first poll.
	evs := NewCCTVDiffer().Diff(nil, cam(model.CCTVControlCentral,
		model.CCTVTour{TourID: 1, State: model.TourRunning}), camBase())
	if len(evs) != 0 {
		t.Fatalf("first observation emitted %d events, want 0", len(evs))
	}
}

func TestCCTVDiffer_NoChangeEmitsNothing(t *testing.T) {
	prev := cam(model.CCTVControlCentral, model.CCTVTour{TourID: 1, State: model.TourRunning})
	curr := cam(model.CCTVControlCentral, model.CCTVTour{TourID: 1, State: model.TourRunning})
	if evs := NewCCTVDiffer().Diff(prev, curr, camBase()); len(evs) != 0 {
		t.Fatalf("unchanged status emitted %d events, want 0", len(evs))
	}
}

func TestCCTVDiffer_ControlModeTransition(t *testing.T) {
	prev := cam(model.CCTVControlCentral)
	evs := NewCCTVDiffer().Diff(prev, cam(model.CCTVControlLocal), camBase())
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	m, ok := evs[0].(model.CCTVControlModeChanged)
	if !ok {
		t.Fatalf("event type = %T", evs[0])
	}
	if m.From != model.CCTVControlCentral || m.To != model.CCTVControlLocal {
		t.Errorf("transition = %v -> %v", m.From, m.To)
	}
	if m.EventDeviceKind() != "cctv" {
		t.Errorf("DeviceKind = %q", m.EventDeviceKind())
	}
}

func TestCCTVDiffer_ToursTransitionIndependently(t *testing.T) {
	prev := cam(model.CCTVControlCentral,
		model.CCTVTour{TourID: 1, State: model.TourRunning},
		model.CCTVTour{TourID: 2, State: model.TourStopped})
	curr := cam(model.CCTVControlCentral,
		model.CCTVTour{TourID: 1, State: model.TourPaused},
		model.CCTVTour{TourID: 2, State: model.TourRunning})

	evs := NewCCTVDiffer().Diff(prev, curr, camBase())
	if len(evs) != 2 {
		t.Fatalf("two tours changing produced %d events, want 2", len(evs))
	}
	seen := map[uint32]model.TourRunState{}
	for _, e := range evs {
		tc := e.(model.CCTVTourStateChanged)
		seen[tc.TourID] = tc.To
	}
	if seen[1] != model.TourPaused || seen[2] != model.TourRunning {
		t.Errorf("tour transitions = %+v", seen)
	}
}

func TestCCTVDiffer_AxesAreIndependent(t *testing.T) {
	prev := cam(model.CCTVControlCentral, model.CCTVTour{TourID: 1, State: model.TourStopped})
	curr := cam(model.CCTVControlLocal, model.CCTVTour{TourID: 1, State: model.TourRunning})
	evs := NewCCTVDiffer().Diff(prev, curr, camBase())
	if len(evs) != 2 {
		t.Fatalf("control mode and tour both moving produced %d events, want 2", len(evs))
	}
}

func TestCCTVDiffer_NewTourEmitsNothing(t *testing.T) {
	// A tour appearing in the table has no prior state to transition from —
	// the same reason first observation is silent.
	prev := cam(model.CCTVControlCentral)
	curr := cam(model.CCTVControlCentral, model.CCTVTour{TourID: 9, State: model.TourRunning})
	if evs := NewCCTVDiffer().Diff(prev, curr, camBase()); len(evs) != 0 {
		t.Fatalf("newly-appearing tour emitted %d events, want 0", len(evs))
	}
}
