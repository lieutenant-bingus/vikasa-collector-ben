package synth

import (
	"sort"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewDetectorDiffer turns consecutive raw counter samples into per-interval
// reports.
//
// Unlike the other differs this one is stateful: Diff is not handed the
// previous snapshot's SampledAt, so it remembers the last sample time per
// device to bound the report interval.
func NewDetectorDiffer() Differ {
	return &detectorDiffer{last: make(map[string]time.Time)}
}

type detectorDiffer struct {
	mu   sync.Mutex
	last map[string]time.Time // deviceID -> previous SampledAt
}

func (d *detectorDiffer) Kind() model.Kind { return model.KindDetectorSamples }

func (d *detectorDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	// Record this poll's time and recover the previous one. Engine serializes
	// Apply today, but a differ must not depend on its caller's locking.
	d.mu.Lock()
	start, hadPrev := d.last[base.DeviceID]
	d.last[base.DeviceID] = base.OccurredAt
	d.mu.Unlock()

	// No previous sample: no interval to attribute counts to. Gen-1 reported
	// the raw cumulative counter here, spiking on every restart.
	if !hadPrev || prev == nil {
		return nil
	}

	prevByCh := byChannel(prev.(model.DetectorSamples))
	c := curr.(model.DetectorSamples)

	readings := make([]model.DetectorReading, 0, len(c.Samples))
	for _, s := range c.Samples {
		p, ok := prevByCh[s.Channel]
		if !ok {
			continue // new channel: no basis this poll, appears in the next
		}
		readings = append(readings, model.DetectorReading{
			Channel:         s.Channel,
			VolumeDelta:     delta(p.VolumeCount, s.VolumeCount),
			OccupancyTenths: s.OccupancyTenths, // a fraction, never delta'd
		})
	}
	if len(readings) == 0 {
		return nil
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].Channel < readings[j].Channel })

	return []model.Event{model.DetectorReport{
		Base:             base,
		IntervalStart:    start,
		IntervalDuration: base.OccurredAt.Sub(start),
		Readings:         readings,
	}}
}

// delta handles a counter that went backwards. At vehicle volumes a genuine
// Counter32 wrap is ~136 years out, so a decrease means the controller reset:
// the current value IS the count since that reset.
func delta(prev, curr uint32) uint32 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}

func byChannel(ds model.DetectorSamples) map[uint32]model.DetectorSample {
	out := make(map[uint32]model.DetectorSample, len(ds.Samples))
	for _, s := range ds.Samples {
		out[s.Channel] = s
	}
	return out
}
