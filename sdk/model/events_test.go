package model

import (
	"testing"
	"time"
)

func TestEventKindsAndAccessors(t *testing.T) {
	at := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "asc-1", OccurredAt: at}

	cases := []struct {
		ev   Event
		kind string
	}{
		{OperationalStatusReport{Base: b, Mode: ModeNormal, ActivePlanID: 3}, "operational-status-report"},
		{ModeChanged{Base: b, From: ModeNormal, To: ModeFlash}, "mode-changed"},
		{PlanChanged{Base: b, FromPlanID: 3, ToPlanID: 7}, "plan-changed"},
		{PreemptionActivated{Base: b, Source: "railroad"}, "preemption-activated"},
		{PreemptionCleared{Base: b}, "preemption-cleared"},
		{DeviceStatusChanged{Base: b, Reachable: false, Reason: "timeout", ConsecutiveFailures: 1}, "device-status-changed"},
		{CollectorStarted{Base: Base{OccurredAt: at}, Version: "dev"}, "collector-started"},
	}
	for _, c := range cases {
		if got := c.ev.EventKind(); got != c.kind {
			t.Errorf("EventKind() = %q, want %q", got, c.kind)
		}
		if got := c.ev.EventOccurredAt(); !got.Equal(at) {
			t.Errorf("%s: OccurredAt = %v, want %v", c.kind, got, at)
		}
	}
	if (SetPlan{PlanID: 4}).CommandKind() != "set-plan" {
		t.Error("SetPlan.CommandKind() != set-plan")
	}
}

// Event kinds match the catalog's ce-type event tokens (fault-raised,
// fault-cleared, detector-report) so Plan 2's mapping stays mechanical.
func TestFacetEventKindsAndAccessors(t *testing.T) {
	at := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "asc-1", OccurredAt: at}

	cases := []struct {
		ev   Event
		kind string
	}{
		{FaultRaised{Base: b, FaultID: "mmu-fault", Severity: SeverityCritical,
			Category: CategoryConflict, Description: "MMU fault detected"}, "fault-raised"},
		{FaultCleared{Base: b, FaultID: "mmu-fault"}, "fault-cleared"},
		{DetectorReport{Base: b, IntervalStart: at.Add(-time.Second), IntervalDuration: time.Second,
			Readings: []DetectorReading{{Channel: 1, VolumeDelta: 3, OccupancyTenths: 125}}}, "detector-report"},
	}
	for _, c := range cases {
		if got := c.ev.EventKind(); got != c.kind {
			t.Errorf("EventKind() = %q, want %q", got, c.kind)
		}
		if got := c.ev.EventOccurredAt(); !got.Equal(at) {
			t.Errorf("%s: OccurredAt = %v, want %v", c.kind, got, at)
		}
		if got := c.ev.EventDeviceID(); got != "asc-1" {
			t.Errorf("%s: DeviceID = %q", c.kind, got)
		}
	}
}

// Events must be self-describing about their device kind: the catalog reuses
// one fault proto across every service, so the kind is what tells a wire
// emitter whether this is dms.fault-raised or signal-control.fault-raised.
func TestEventsCarryDeviceKind(t *testing.T) {
	at := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "dms-1", DeviceKind: "dms", OccurredAt: at}

	var ev Event = FaultRaised{Base: b, FaultID: "dms-fault-pixel", Severity: SeverityMinor}
	if got := ev.EventDeviceKind(); got != "dms" {
		t.Fatalf("EventDeviceKind() = %q, want %q", got, "dms")
	}
	if got := ev.EventDeviceID(); got != "dms-1" {
		t.Fatalf("EventDeviceID() = %q", got)
	}
}

func TestDMSEventKindsAndAccessors(t *testing.T) {
	at := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "dms-1", DeviceKind: "dms", OccurredAt: at}

	cases := []struct {
		ev   Event
		kind string
	}{
		{DMSControlModeChanged{Base: b, From: ControlLocal, To: ControlCentral}, "control-mode-changed"},
		{DMSDisplayStateChanged{Base: b, From: DisplayBlank, To: DisplayNormal}, "display-state-changed"},
		{DMSMessageActivationFailed{Base: b, MemoryType: MemoryChangeable, Slot: 4,
			Error: SyntaxErrorFontNotFound, ErrorPosition: 12}, "message-activation-failed"},
	}
	for _, c := range cases {
		if got := c.ev.EventKind(); got != c.kind {
			t.Errorf("EventKind() = %q, want %q", got, c.kind)
		}
		if got := c.ev.EventOccurredAt(); !got.Equal(at) {
			t.Errorf("%s: OccurredAt = %v", c.kind, got)
		}
		if got := c.ev.EventDeviceKind(); got != "dms" {
			t.Errorf("%s: DeviceKind = %q", c.kind, got)
		}
	}
}
