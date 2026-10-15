package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Absence of evidence is never a state change (ADR 0013), for the four
// differs that had no failed-read test. Each one drives a real Engine
// through good poll → failed poll → recovery, and the recovery is chosen so
// that an engine which forgot the pre-failure state would emit something
// different: a transition with the wrong From, a re-detected incident, or a
// duplicate interval report. "The failed poll emitted nothing" alone cannot
// tell a correct engine from one that wiped its memory.

func snapOf(dev, kind string, at time.Time, f model.Facet) *model.Snapshot {
	return &model.Snapshot{DeviceID: dev, DeviceKind: kind, SampledAt: at, Facets: []model.Facet{f}}
}

func failedOf(dev, kind string, at time.Time, k model.Kind) *model.Snapshot {
	return &model.Snapshot{DeviceID: dev, DeviceKind: kind, SampledAt: at,
		Errors: []model.FacetError{{Kind: k, Err: "timeout"}}}
}

func TestCCTVFailedReadEmitsNothingAndRecoveryDiffsAgainstPreFailure(t *testing.T) {
	e := NewEngine(NewCCTVDiffer())
	e.Apply(snapOf("cam-03", "cctv", camAt, cam(model.CCTVControlCentral)))

	if evs := e.Apply(failedOf("cam-03", "cctv", camAt.Add(time.Second), model.KindCCTVStatus)); len(evs) != 0 {
		t.Fatalf("failed read emitted %v, want nothing", kinds(evs))
	}

	evs := e.Apply(snapOf("cam-03", "cctv", camAt.Add(2*time.Second), cam(model.CCTVControlLocal)))
	if len(evs) != 1 {
		t.Fatalf("recovery emitted %v, want exactly one control-mode change", kinds(evs))
	}
	got, ok := evs[0].(model.CCTVControlModeChanged)
	if !ok || got.From != model.CCTVControlCentral || got.To != model.CCTVControlLocal {
		t.Fatalf("recovery event = %+v, want central→local against the pre-failure state", evs[0])
	}
}

// The dangerous reading of a failed poll here is "no incidents", which would
// clear every open incident and then re-detect it on recovery.
func TestZoneIncidentFailedReadNeitherClearsNorRedetects(t *testing.T) {
	e := NewEngine(NewZoneIncidentDiffer())
	open := set(inc("i-1", model.IncidentMajor, 90))
	e.Apply(snapOf("lidar-01", "perception", pcpAt, open))

	if evs := e.Apply(failedOf("lidar-01", "perception", pcpAt.Add(time.Second), model.KindZoneIncidents)); len(evs) != 0 {
		t.Fatalf("failed read emitted %v; it must not clear open incidents", kinds(evs))
	}
	if evs := e.Apply(snapOf("lidar-01", "perception", pcpAt.Add(2*time.Second), open)); len(evs) != 0 {
		t.Fatalf("recovery emitted %v; a still-open incident must not be re-detected", kinds(evs))
	}
}

func TestTrafficIntervalFailedReadDoesNotDuplicateTheReport(t *testing.T) {
	e := NewEngine(NewTrafficIntervalDiffer())
	if evs := e.Apply(snapOf("ts-01", "traffic-sensor", tsAt, intervals(tsAt, 12))); len(evs) != 1 {
		t.Fatalf("first interval emitted %v, want one report", kinds(evs))
	}

	if evs := e.Apply(failedOf("ts-01", "traffic-sensor", tsAt.Add(time.Second), model.KindTrafficIntervals)); len(evs) != 0 {
		t.Fatalf("failed read emitted %v, want nothing", kinds(evs))
	}
	// Same interval still current on recovery: already reported.
	if evs := e.Apply(snapOf("ts-01", "traffic-sensor", tsAt.Add(2*time.Second), intervals(tsAt, 12))); len(evs) != 0 {
		t.Fatalf("recovery re-reported an interval already published: %v", kinds(evs))
	}
}

func TestZoneIntervalFailedReadDoesNotDuplicateTheReport(t *testing.T) {
	e := NewEngine(NewZoneIntervalDiffer())
	if evs := e.Apply(snapOf("lidar-01", "perception", pcpAt, zones(pcpAt, 5))); len(evs) != 2 {
		t.Fatalf("first interval emitted %v, want its two reports", kinds(evs))
	}

	if evs := e.Apply(failedOf("lidar-01", "perception", pcpAt.Add(time.Second), model.KindZoneIntervals)); len(evs) != 0 {
		t.Fatalf("failed read emitted %v, want nothing", kinds(evs))
	}
	if evs := e.Apply(snapOf("lidar-01", "perception", pcpAt.Add(2*time.Second), zones(pcpAt, 5))); len(evs) != 0 {
		t.Fatalf("recovery re-reported an interval already published: %v", kinds(evs))
	}
}
