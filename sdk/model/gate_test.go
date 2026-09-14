package model

import "testing"

func TestGateEnumsString(t *testing.T) {
	for k, want := range map[GateKind]string{
		GateKindUnknown: "unknown", GateKindWarning: "warning", GateKindBarrier: "barrier",
		GateKind(99): "unknown",
	} {
		if got := k.String(); got != want {
			t.Errorf("GateKind(%d).String() = %q, want %q", k, got, want)
		}
	}
	for m, want := range map[GateMode]string{
		GateModeUnknown: "unknown", GateModeAuto: "auto",
		GateModeManual: "manual", GateModeOffline: "offline", GateMode(99): "unknown",
	} {
		if got := m.String(); got != want {
			t.Errorf("GateMode(%d).String() = %q, want %q", m, got, want)
		}
	}
	for p, want := range map[GatePosition]string{
		GatePositionUnknown: "unknown", GatePositionClosing: "closing",
		GatePositionClosed: "closed", GatePositionOpening: "opening",
		GatePositionOpened: "opened", GatePosition(99): "unknown",
	} {
		if got := p.String(); got != want {
			t.Errorf("GatePosition(%d).String() = %q, want %q", p, got, want)
		}
	}
}

func TestGateBankFacetKind(t *testing.T) {
	if got := (GateBank{}).FacetKind(); got != KindGateBank {
		t.Fatalf("FacetKind() = %q, want %q", got, KindGateBank)
	}
}
