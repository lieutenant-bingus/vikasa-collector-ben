package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewTrafficIntervalDiffer emits a report each time the sensor presents a new
// measurement interval.
//
// It is stateless, unlike the detector differ. That difference is the whole
// point of the facet: an NTCIP controller exposes cumulative counters, so the
// collector must remember the last sample to compute a delta and an interval.
// A roadside traffic sensor bins internally and hands over a finished
// interval, so there is nothing to remember and nothing to difference — the
// numbers are already the answer.
func NewTrafficIntervalDiffer() Differ { return trafficIntervalDiffer{} }

type trafficIntervalDiffer struct{}

func (trafficIntervalDiffer) Kind() model.Kind { return model.KindTrafficIntervals }

func (trafficIntervalDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.TrafficIntervals)

	// No lanes is a configuration, not a measurement. Emitting an empty report
	// would read downstream as "zero traffic on this sensor" — an assertion
	// the device never made.
	if len(c.Lanes) == 0 {
		return nil
	}

	// Polling faster than the sensor's binning window re-reads one interval.
	// Republishing it inflates every count derived from the stream, so the
	// device's own interval start is what separates a new measurement from the
	// same one seen twice.
	//
	// First observation (prev == nil) DOES emit, unlike the counter-based
	// differs: the sensor did the binning, so the very first read is already a
	// complete and attributable interval.
	if prev != nil {
		if p, ok := prev.(model.TrafficIntervals); ok && p.IntervalStart.Equal(c.IntervalStart) {
			return nil
		}
	}

	lanes := append([]model.LaneMeasurement(nil), c.Lanes...)
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].LaneID < lanes[j].LaneID })

	return []model.Event{model.TrafficIntervalReport{
		Base:             base,
		IntervalStart:    c.IntervalStart,
		IntervalDuration: c.IntervalDuration,
		Lanes:            lanes,
	}}
}
