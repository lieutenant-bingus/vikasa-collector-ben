package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func detSnap(at time.Time, samples ...model.DetectorSample) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at,
		Facets: []model.Facet{model.DetectorSamples{Samples: samples}}}
}

// No previous sample means no interval to attribute counts to. Gen-1 reported
// the raw cumulative counter here, producing a large bogus spike on every
// collector restart.
func TestFirstPollEmitsNoReport(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	evs := e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000, OccupancyTenths: 100}))
	if len(evs) != 0 {
		t.Fatalf("first poll must emit nothing, got %v", kinds(evs))
	}
}

func TestSecondPollReportsDelta(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000, OccupancyTenths: 100}))

	t1 := t0.Add(10 * time.Second)
	evs := e.Apply(detSnap(t1, model.DetectorSample{Channel: 1, VolumeCount: 5007, OccupancyTenths: 125}))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 report", kinds(evs))
	}
	r := evs[0].(model.DetectorReport)
	if !r.IntervalStart.Equal(t0) || r.IntervalDuration != 10*time.Second {
		t.Fatalf("interval = %v +%v, want %v +10s", r.IntervalStart, r.IntervalDuration, t0)
	}
	if len(r.Readings) != 1 {
		t.Fatalf("readings = %+v", r.Readings)
	}
	got := r.Readings[0]
	// Volume is delta'd; occupancy is NOT — it is a fraction, not a counter.
	if got.VolumeDelta != 7 || got.OccupancyTenths != 125 {
		t.Fatalf("reading = %+v, want delta 7 occ 125", got)
	}
}

// A sub-second poll interval must survive intact: rounding it to whole
// seconds would collide it with zero while VolumeDelta stays non-zero,
// corrupting any rate a consumer computes. Rounding to the wire's whole
// seconds is the emitter's job, done once, later — not the domain's.
func TestSubSecondIntervalIsNotLostToZero(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000, OccupancyTenths: 100}))

	t1 := t0.Add(400 * time.Millisecond)
	evs := e.Apply(detSnap(t1, model.DetectorSample{Channel: 1, VolumeCount: 5007, OccupancyTenths: 125}))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 report", kinds(evs))
	}
	r := evs[0].(model.DetectorReport)
	if r.IntervalDuration != 400*time.Millisecond {
		t.Fatalf("IntervalDuration = %v, want 400ms", r.IntervalDuration)
	}
	if r.Readings[0].VolumeDelta != 7 {
		t.Fatalf("VolumeDelta = %d, want 7 (non-zero, alongside a non-zero interval)", r.Readings[0].VolumeDelta)
	}
}

// At vehicle volumes a genuine Counter32 wrap is ~136 years away, so a
// decrease means the controller reset. Reporting the current value is correct
// for a reset.
func TestCounterDecreaseIsTreatedAsReset(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000}))
	evs := e.Apply(detSnap(t0.Add(time.Second), model.DetectorSample{Channel: 1, VolumeCount: 3}))
	r := evs[0].(model.DetectorReport)
	if r.Readings[0].VolumeDelta != 3 {
		t.Fatalf("after reset VolumeDelta = %d, want 3 (the current value)", r.Readings[0].VolumeDelta)
	}
}

// A channel with no previous sample has no interval basis — same rule as the
// first poll, applied per channel. It appears in the next report.
func TestNewChannelIsOmittedForOnePoll(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 100}))

	evs := e.Apply(detSnap(t0.Add(time.Second),
		model.DetectorSample{Channel: 1, VolumeCount: 105},
		model.DetectorSample{Channel: 2, VolumeCount: 900}))
	r := evs[0].(model.DetectorReport)
	if len(r.Readings) != 1 || r.Readings[0].Channel != 1 {
		t.Fatalf("readings = %+v, want channel 1 only", r.Readings)
	}

	// Next poll, channel 2 has a basis and appears.
	evs = e.Apply(detSnap(t0.Add(2*time.Second),
		model.DetectorSample{Channel: 1, VolumeCount: 108},
		model.DetectorSample{Channel: 2, VolumeCount: 904}))
	r = evs[0].(model.DetectorReport)
	if len(r.Readings) != 2 || r.Readings[1].Channel != 2 || r.Readings[1].VolumeDelta != 4 {
		t.Fatalf("readings = %+v, want ch1 and ch2(delta 4)", r.Readings)
	}
}

func TestReadingsAreSortedByChannel(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	mk := func(at time.Time, base uint32) *model.Snapshot {
		return detSnap(at,
			model.DetectorSample{Channel: 7, VolumeCount: base},
			model.DetectorSample{Channel: 2, VolumeCount: base},
			model.DetectorSample{Channel: 4, VolumeCount: base})
	}
	e.Apply(mk(t0, 10))
	r := e.Apply(mk(t0.Add(time.Second), 20))[0].(model.DetectorReport)
	want := []uint32{2, 4, 7}
	for i, w := range want {
		if r.Readings[i].Channel != w {
			t.Fatalf("channel order = %+v, want sorted %v", r.Readings, want)
		}
	}
}

// An empty facet is normal (a controller with no detectors), and must not
// produce a report full of nothing.
func TestEmptyFacetEmitsNoReport(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0))
	if evs := e.Apply(detSnap(t0.Add(time.Second))); len(evs) != 0 {
		t.Fatalf("empty facet must emit nothing, got %v", kinds(evs))
	}
}

// The iron rule, for this facet.
func TestFailedDetectorReadEmitsNothing(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 100}))
	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindDetectorSamples, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}
	// Recovery deltas against the pre-failure sample, which survived.
	evs := e.Apply(detSnap(t0.Add(2*time.Second), model.DetectorSample{Channel: 1, VolumeCount: 106}))
	r := evs[0].(model.DetectorReport)
	if r.Readings[0].VolumeDelta != 6 {
		t.Fatalf("VolumeDelta = %d, want 6 (delta vs the surviving sample)", r.Readings[0].VolumeDelta)
	}
}
