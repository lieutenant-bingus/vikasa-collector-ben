package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestPhaseDiffer_EmitsOnStateChange(t *testing.T) {
	base := model.Base{DeviceID: "asc-1", DeviceKind: "asc", OccurredAt: time.Now()}
	d := NewPhaseDiffer()
	prev := model.PhaseStatuses{Phases: []model.PhaseStatus{
		{PhaseNumber: 1, State: model.SignalStateStop},
	}}
	curr := model.PhaseStatuses{Phases: []model.PhaseStatus{
		{PhaseNumber: 1, State: model.SignalStateProtectedMovement},
	}}
	events := d.Diff(prev, curr, base)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	ev := events[0].(model.PhaseStateChanged)
	if ev.PhaseNumber != 1 || ev.From != model.SignalStateStop || ev.To != model.SignalStateProtectedMovement {
		t.Fatalf("event = %+v", ev)
	}
}

func TestCoordinationDiffer_EmitsOnOffsetChange(t *testing.T) {
	base := model.Base{DeviceID: "asc-1", DeviceKind: "asc", OccurredAt: time.Now()}
	d := NewCoordinationDiffer()
	prev := model.CoordinationStatus{ActualOffset: 0}
	curr := model.CoordinationStatus{ActualOffset: 15}
	events := d.Diff(prev, curr, base)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	ev := events[0].(model.CoordinationChanged)
	if ev.Axis != model.CoordAxisActualOffset || ev.PreviousValue != 0 || ev.NewValue != 15 {
		t.Fatalf("event = %+v", ev)
	}
}

func TestDetectorChannelDiffer_EmitsCallOn(t *testing.T) {
	base := model.Base{DeviceID: "asc-1", DeviceKind: "asc", OccurredAt: time.Now()}
	d := NewDetectorChannelDiffer()
	prev := model.DetectorChannelStatuses{Channels: []model.DetectorChannelStatus{
		{Channel: 1},
	}}
	curr := model.DetectorChannelStatuses{Channels: []model.DetectorChannelStatus{
		{Channel: 1, CallActive: true},
	}}
	events := d.Diff(prev, curr, base)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	ev := events[0].(model.DetectorTransition)
	if ev.Channel != 1 || ev.Kind != model.DetectorTransitionCallOn {
		t.Fatalf("event = %+v", ev)
	}
}
