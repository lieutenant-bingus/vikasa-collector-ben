package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var tsAt = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func tsBase() model.Base {
	return model.Base{DeviceID: "ts-01", DeviceKind: "traffic-sensor", OccurredAt: tsAt}
}

func intervals(start time.Time, vol uint32) model.TrafficIntervals {
	return model.TrafficIntervals{
		IntervalStart:    start,
		IntervalDuration: 60 * time.Second,
		Lanes: []model.LaneMeasurement{{
			LaneID: 1, Volume: vol, OccupancyTenths: 125,
			SpeedAvgHundredthsKPH: 8850, SpeedReported: true,
			Quality: model.QualityValid,
		}},
	}
}

func TestTrafficDiffer_FirstObservationEmits(t *testing.T) {
	// Deliberately UNLIKE the detector differ, which stays silent on first
	// poll because a cumulative counter has no basis yet. These sensors hand
	// over a finished interval they binned themselves, so the first read is a
	// complete, attributable measurement — discarding it would throw away real
	// data for no reason.
	evs := NewTrafficIntervalDiffer().Diff(nil, intervals(tsAt.Add(-time.Minute), 42), tsBase())
	if len(evs) != 1 {
		t.Fatalf("first observation produced %d events, want 1", len(evs))
	}
	r, ok := evs[0].(model.TrafficIntervalReport)
	if !ok {
		t.Fatalf("event type = %T", evs[0])
	}
	if r.Lanes[0].Volume != 42 {
		t.Errorf("volume = %d, want 42", r.Lanes[0].Volume)
	}
	if r.IntervalDuration != time.Minute {
		t.Errorf("interval duration = %v, want 1m (the DEVICE's, not the poll's)", r.IntervalDuration)
	}
	if r.EventDeviceKind() != "traffic-sensor" {
		t.Errorf("DeviceKind = %q", r.EventDeviceKind())
	}
}

func TestTrafficDiffer_SameIntervalReReadEmitsNothing(t *testing.T) {
	// Polling faster than the sensor's binning window re-reads one interval.
	// Re-publishing it would inflate every downstream count; the interval
	// start is what distinguishes a new measurement from the same one again.
	d := NewTrafficIntervalDiffer()
	start := tsAt.Add(-time.Minute)
	prev := intervals(start, 42)
	if evs := d.Diff(prev, intervals(start, 42), tsBase()); len(evs) != 0 {
		t.Fatalf("re-read of the same interval emitted %d events, want 0", len(evs))
	}
}

func TestTrafficDiffer_NewIntervalEmits(t *testing.T) {
	d := NewTrafficIntervalDiffer()
	prev := intervals(tsAt.Add(-2*time.Minute), 42)
	evs := d.Diff(prev, intervals(tsAt.Add(-time.Minute), 17), tsBase())
	if len(evs) != 1 {
		t.Fatalf("new interval produced %d events, want 1", len(evs))
	}
	if got := evs[0].(model.TrafficIntervalReport).Lanes[0].Volume; got != 17 {
		t.Errorf("volume = %d, want 17 (the interval's own count, not a delta)", got)
	}
}

func TestTrafficDiffer_EmptyLanesEmitNothing(t *testing.T) {
	// A sensor reporting no lanes is a configuration, not a measurement. An
	// empty report would read downstream as "zero traffic".
	d := NewTrafficIntervalDiffer()
	curr := model.TrafficIntervals{IntervalStart: tsAt, IntervalDuration: time.Minute}
	if evs := d.Diff(nil, curr, tsBase()); len(evs) != 0 {
		t.Fatalf("empty lane set emitted %d events, want 0", len(evs))
	}
}

func TestTrafficDiffer_LanesAreSorted(t *testing.T) {
	d := NewTrafficIntervalDiffer()
	curr := model.TrafficIntervals{
		IntervalStart: tsAt, IntervalDuration: time.Minute,
		Lanes: []model.LaneMeasurement{{LaneID: 3}, {LaneID: 1}, {LaneID: 2}},
	}
	evs := d.Diff(nil, curr, tsBase())
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	for i, l := range evs[0].(model.TrafficIntervalReport).Lanes {
		if l.LaneID != uint32(i+1) {
			t.Fatalf("lanes not sorted: %+v", evs[0].(model.TrafficIntervalReport).Lanes)
		}
	}
}
