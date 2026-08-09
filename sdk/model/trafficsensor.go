package model

import "time"

// KindTrafficIntervals is a completed measurement interval from a roadside
// traffic sensor (radar, magnetometer, loop-emulation station).
const KindTrafficIntervals Kind = "traffic-intervals"

// LaneClassVolume is one classification bin's count for a lane.
//
// ClassID is the bin index within whatever scheme the sensor is configured
// for — length-based, FHWA Scheme F 13-category, axle-based. The domain does
// NOT interpret it: the same id means different vehicles under different
// schemes, and a sensor that cannot tell you its scheme has not told you what
// bin 3 is either.
type LaneClassVolume struct {
	ClassID uint32
	Volume  uint32
}

// LaneMeasurement is one lane over one interval, exactly as the sensor
// reported it.
//
// Unlike DetectorSample this carries no raw cumulative counter: these sensors
// bin internally and report a finished interval, so there is nothing for the
// collector to difference. Volume is the count within the interval.
//
// OccupancyTenths is 0..1000 — tenths of a percent, the same convention
// DetectorSample uses, so the domain stays lossless against a wire that wants
// percent.
//
// SpeedAvgHundredthsKPH is hundredths of a km/h. SpeedReported exists because
// zero is a LEGITIMATE reading: a lane of stopped traffic reports 0 km/h, and
// collapsing that with "the sensor did not report speed" would turn gridlock
// into missing data at exactly the moment someone cares.
type LaneMeasurement struct {
	LaneID                uint32
	Volume                uint32
	OccupancyTenths       uint16
	SpeedAvgHundredthsKPH uint32
	SpeedReported         bool
	// ClassVolumes is sorted by ClassID and EMPTY when the sensor is not
	// classifying — a normal configuration, not a read failure.
	ClassVolumes []LaneClassVolume
	Quality      DataQuality
}

// TrafficIntervals is the sensor's most recently completed interval.
//
// IntervalStart and IntervalDuration come from the DEVICE, not from poll
// timing: the sensor decides its own binning window, so a collector polling
// faster or slower than that window must not invent one. The differ uses
// IntervalStart to tell a fresh interval from a re-read of the same one.
//
// A sensor reporting no lanes yields an EMPTY facet, not an error.
type TrafficIntervals struct {
	IntervalStart    time.Time
	IntervalDuration time.Duration
	Lanes            []LaneMeasurement
}

func (TrafficIntervals) FacetKind() Kind { return KindTrafficIntervals }
