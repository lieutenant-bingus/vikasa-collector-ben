package model

import (
	"testing"
	"time"
)

func TestSnapshotFacetLookup(t *testing.T) {
	s := &Snapshot{
		DeviceID:  "asc-1",
		SampledAt: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
		Facets:    []Facet{SignalStatus{Mode: ModeNormal, ActivePlanID: 3}},
		Errors:    []FacetError{{Kind: Kind("detector-samples"), Err: "timeout"}},
	}

	f, ok := s.Facet(KindSignalStatus)
	if !ok {
		t.Fatal("expected signal-status facet present")
	}
	if got := f.(SignalStatus).ActivePlanID; got != 3 {
		t.Fatalf("ActivePlanID = %d, want 3", got)
	}
	if _, ok := s.Facet(Kind("dms-status")); ok {
		t.Fatal("unexpected facet found")
	}
	if !s.FacetFailed(Kind("detector-samples")) {
		t.Fatal("expected detector-samples marked failed")
	}
	if s.FacetFailed(KindSignalStatus) {
		t.Fatal("signal-status must not be marked failed")
	}
}

func TestControllerModeString(t *testing.T) {
	cases := map[ControllerMode]string{
		ModeUnknown: "unknown", ModeNormal: "normal", ModeFlash: "flash",
		ModeStandby: "standby", ModeOff: "off", ControllerMode(99): "unknown",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", mode, got, want)
		}
	}
}
