package model

import "testing"

func TestHighResClass(t *testing.T) {
	for _, tc := range []struct {
		code, param uint16
		want        HighResClass
		kind        string
	}{
		{1, 2, HighResPhase, "hr-phase"},
		{1, 0, HighResUnmapped, "hr-unmapped"},  // phase 0 is no phase
		{1, 41, HighResUnmapped, "hr-unmapped"}, // nor is 41
		{21, 4, HighResPedestrian, "hr-pedestrian"},
		{90, 3, HighResPedestrian, "hr-pedestrian"},
		{82, 7, HighResDetector, "hr-detector"},
		{82, 0, HighResUnmapped, "hr-unmapped"},
		{61, 1, HighResOverlap, "hr-overlap"},
		{102, 1, HighResPreemption, "hr-preemption"},
		{116, 4, HighResPreemption, "hr-preemption"},
		{150, 5, HighResCoordination, "hr-coordination"},
		{150, 8, HighResUnmapped, "hr-unmapped"},
		{131, 254, HighResCoordination, "hr-coordination"},
		{139, 22, HighResCoordination, "hr-coordination"},
		{50, 0, HighResUnmapped, "hr-unmapped"},
		{13, 2, HighResUnmapped, "hr-unmapped"},   // Indiana 2019 ext-timer gap-out: not in the pinned catalog
		{305, 20, HighResUnmapped, "hr-unmapped"}, // a vendor code above 255
	} {
		e := HighResEvent{Code: tc.code, Param: tc.param}
		if got := e.Class(); got != tc.want {
			t.Errorf("code %d param %d: class %d, want %d", tc.code, tc.param, got, tc.want)
		}
		if got := e.EventKind(); got != tc.kind {
			t.Errorf("code %d param %d: kind %q, want %q", tc.code, tc.param, got, tc.kind)
		}
	}
}

func TestHighResStampedOverwritesTheAdapters(t *testing.T) {
	var e LogEvent = HighResEvent{Base: Base{DeviceID: "other", DeviceKind: "dms"}, Code: 1, Param: 1}
	s := e.Stamped("asc-1", "asc")
	if s.EventDeviceID() != "asc-1" || s.EventDeviceKind() != "asc" {
		t.Errorf("stamped = %s/%s", s.EventDeviceID(), s.EventDeviceKind())
	}
}
