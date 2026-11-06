package model

import "time"

// KindDetectorSamples is per-channel vehicle detector data at one poll.
const KindDetectorSamples Kind = "detector-samples"

// DetectorSample is one detector channel as read.
//
// VolumeCount is what the controller reported. With DetectorSamples.Period
// nil it is a RAW cumulative counter: the differ turns consecutive values
// into a delta, which leaves the adapter memoryless. With Period set it is
// the total for that completed collection period (NTCIP 1202
// detectorVolume), reported once per period as is (ADR 0026).
//
// OccupancyTenths is 0..1000. NTCIP reports occupancy in half-percent
// (0..200) and the catalog wants percent (0..100); tenths represents
// half-percent exactly (0.5% = 5), so the domain stays lossless and the
// emitter rounds to the wire's coarser precision.
type DetectorSample struct {
	Channel         uint32 // NTCIP channel number == table row, 1-based
	VolumeCount     uint32
	OccupancyTenths uint16
}

// DetectorSamples holds one sample per answering channel, sorted by Channel.
// A controller with no detectors yields an EMPTY facet, not an error — that
// is a normal deployment, and a FacetError would be a permanent false alarm.
type DetectorSamples struct {
	Samples []DetectorSample
	// Period is the completed collection period the samples total, for a
	// controller that reports per-period totals; nil for a cumulative
	// counter. See ADR 0026.
	Period *CollectionPeriod
}

// CollectionPeriod identifies one completed volume/occupancy collection
// period (NTCIP 1202 volumeOccupancySequence, detectorSampleTime,
// detectorSampleDuration).
type CollectionPeriod struct {
	// Sequence changes when a new period completes; equal Sequences on
	// consecutive polls are the same period, reported once.
	Sequence uint32
	// End is when the period closed; zero means unknown, and the report is
	// anchored at the poll that first saw it.
	End time.Time
	// Duration is the period's length. The controller's configured period
	// is not evidence of it: MaxTime 2.15.1 advertised 60 s and rolled at
	// 900 s in the lab.
	Duration time.Duration
}

func (DetectorSamples) FacetKind() Kind { return KindDetectorSamples }
