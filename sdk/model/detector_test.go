package model

import "testing"

func TestDetectorSamplesIsAFacet(t *testing.T) {
	var f Facet = DetectorSamples{Samples: []DetectorSample{
		{Channel: 1, VolumeCount: 1234, OccupancyTenths: 125},
	}}
	if f.FacetKind() != KindDetectorSamples {
		t.Fatalf("FacetKind() = %q, want %q", f.FacetKind(), KindDetectorSamples)
	}
	s := f.(DetectorSamples).Samples[0]
	// VolumeCount is the RAW cumulative counter as the controller reported it.
	// The differ turns consecutive values into an interval delta; keeping the
	// raw value here leaves the adapter memoryless.
	if s.VolumeCount != 1234 || s.OccupancyTenths != 125 {
		t.Fatalf("sample = %+v", s)
	}
}
