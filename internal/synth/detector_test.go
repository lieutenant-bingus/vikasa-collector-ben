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
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	evs := e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000, OccupancyTenths: 100}))
	if len(evs) != 0 {
		t.Fatalf("first poll must emit nothing, got %v", kinds(evs))
	}
}

func TestSecondPollReportsDelta(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0))
	if evs := e.Apply(detSnap(t0.Add(time.Second))); len(evs) != 0 {
		t.Fatalf("empty facet must emit nothing, got %v", kinds(evs))
	}
}

// The iron rule, for this facet.
func TestFailedDetectorReadEmitsNothing(t *testing.T) {
	t.Parallel()
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

func periodSnap(at time.Time, seq uint32, end time.Time, samples ...model.DetectorSample) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at, Facets: []model.Facet{model.DetectorSamples{
		Samples: samples, Period: &model.CollectionPeriod{Sequence: seq, End: end, Duration: 15 * time.Minute},
	}}}
}

// The lab sequence (MaxTime 2.15.1, detector 1 pulsed): period totals 9,
// 169 and 180 at 17:15, 17:30 and 17:45. Each is reported once, as is, over
// its own 15 minutes. The cumulative path published 9, 160 and 11.
func TestPeriodTotalsAreReportedOncePerPeriod(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	end := t0.Add(15 * time.Minute)
	if evs := e.Apply(periodSnap(end.Add(5*time.Second), 54, end, model.DetectorSample{Channel: 1, VolumeCount: 9})); len(evs) != 0 {
		t.Fatalf("first period seen after start must not be reported: %v", kinds(evs))
	}
	var got []uint32
	for i, total := range []uint32{169, 180} {
		end := t0.Add(time.Duration(i+2) * 15 * time.Minute)
		for _, poll := range []time.Duration{5 * time.Second, 10 * time.Second} { // two polls see each period
			for _, ev := range e.Apply(periodSnap(end.Add(poll), uint32(55+i), end, model.DetectorSample{Channel: 1, VolumeCount: total, OccupancyTenths: 200})) {
				r := ev.(model.DetectorReport)
				if !r.IntervalStart.Equal(end.Add(-15*time.Minute)) || r.IntervalDuration != 15*time.Minute {
					t.Errorf("interval %v +%v, want the period ending %v", r.IntervalStart, r.IntervalDuration, end)
				}
				if r.Readings[0].OccupancyTenths != 200 {
					t.Errorf("occupancy %d", r.Readings[0].OccupancyTenths)
				}
				got = append(got, r.Readings[0].VolumeDelta)
			}
		}
	}
	if len(got) != 2 || got[0] != 169 || got[1] != 180 {
		t.Fatalf("reported volumes %v, want [169 180]", got)
	}
}

func TestPeriodWithoutAnEndIsAnchoredAtThePoll(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	e.Apply(periodSnap(t0, 1, time.Time{}, model.DetectorSample{Channel: 1}))
	at := t0.Add(15 * time.Minute)
	evs := e.Apply(periodSnap(at, 2, time.Time{}, model.DetectorSample{Channel: 1, VolumeCount: 4}))
	if len(evs) != 1 || !evs[0].(model.DetectorReport).IntervalStart.Equal(t0) {
		t.Fatalf("events %+v", evs)
	}
}

// The period axes move alone: totals that change inside one period (the
// controller rewriting them, or a mid-period read) emit nothing, and a
// change of sequence alone, with identical totals, emits exactly one report.
func TestPeriodAxesChangeIndependently(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	end := t0.Add(15 * time.Minute)
	e.Apply(periodSnap(end, 7, end, model.DetectorSample{Channel: 1, VolumeCount: 3}))
	if evs := e.Apply(periodSnap(end.Add(5*time.Second), 7, end, model.DetectorSample{Channel: 1, VolumeCount: 40, OccupancyTenths: 90})); len(evs) != 0 {
		t.Fatalf("totals changing within a period must emit nothing, got %v", kinds(evs))
	}
	next := end.Add(15 * time.Minute)
	evs := e.Apply(periodSnap(next, 8, next, model.DetectorSample{Channel: 1, VolumeCount: 40, OccupancyTenths: 90}))
	if len(evs) != 1 {
		t.Fatalf("a new sequence must emit exactly one report, got %v", kinds(evs))
	}
}

// Absence of evidence, for the period path. The recovery poll carries a new
// sequence: an engine that kept the pre-failure period reports it; one that
// forgot its state would treat it as a first period and stay silent.
func TestFailedPeriodReadEmitsNothingAndRecoveryReportsTheNewPeriod(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	end := t0.Add(15 * time.Minute)
	e.Apply(periodSnap(end, 7, end, model.DetectorSample{Channel: 1, VolumeCount: 3}))
	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: end.Add(5 * time.Second),
		Errors: []model.FacetError{{Kind: model.KindDetectorSamples, Err: "detectorSampleTime absent"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}
	next := end.Add(15 * time.Minute)
	evs := e.Apply(periodSnap(next.Add(5*time.Second), 8, next, model.DetectorSample{Channel: 1, VolumeCount: 12}))
	if len(evs) != 1 || evs[0].(model.DetectorReport).Readings[0].VolumeDelta != 12 {
		t.Fatalf("recovery must report period 8 against the surviving state, got %+v", evs)
	}
}

// A device that switches from cumulative counters to periods (an adapter
// upgrade across a restart-free reload) has no reported period to compare
// against: the first period is not reported, as after a restart.
func TestCumulativeToPeriodSwitchEmitsNothing(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000}))
	end := t0.Add(15 * time.Minute)
	if evs := e.Apply(periodSnap(end, 7, end, model.DetectorSample{Channel: 1, VolumeCount: 9})); len(evs) != 0 {
		t.Fatalf("first period after cumulative samples must emit nothing, got %v", kinds(evs))
	}
}

func TestPeriodReportCarriesDeviceKind(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewDetectorDiffer())
	end := t0.Add(15 * time.Minute)
	s := periodSnap(end, 7, end, model.DetectorSample{Channel: 1})
	s.DeviceKind = "asc"
	e.Apply(s)
	s = periodSnap(end.Add(15*time.Minute), 8, end.Add(15*time.Minute), model.DetectorSample{Channel: 1, VolumeCount: 2})
	s.DeviceKind = "asc"
	evs := e.Apply(s)
	if len(evs) != 1 || evs[0].EventDeviceKind() != "asc" {
		t.Fatalf("events %+v, want one report stamped asc", evs)
	}
}
