package model

// KindDetectorSamples is per-channel vehicle detector data at one poll.
const KindDetectorSamples Kind = "detector-samples"

// DetectorSample is one detector channel as read.
//
// VolumeCount is the RAW cumulative counter the controller reported, not an
// interval count: the differ turns consecutive values into a delta, which
// leaves the adapter memoryless.
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
type DetectorSamples struct{ Samples []DetectorSample }

func (DetectorSamples) FacetKind() Kind { return KindDetectorSamples }
