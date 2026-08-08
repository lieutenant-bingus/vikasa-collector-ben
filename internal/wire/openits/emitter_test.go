package openits

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// decodeModeChanged encodes ev and unmarshals the payload back, so assertions
// are made against what actually goes on the wire rather than against an
// intermediate the emitter happens to hold.
func decodeModeChanged(t *testing.T, ev model.Event, collectorID string) *commonv1.ModeChanged {
	t.Helper()
	enc, ok, err := New(collectorID).Encode(ev)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatalf("emitter did not claim %s on a %s device", ev.EventKind(), ev.EventDeviceKind())
	}
	var got commonv1.ModeChanged
	if err := proto.Unmarshal(enc.Data, &got); err != nil {
		t.Fatalf("payload is not a valid common/v1.ModeChanged: %v", err)
	}
	return &got
}

func base(deviceID, deviceKind string) model.Base {
	return model.Base{
		DeviceID:   deviceID,
		DeviceKind: deviceKind,
		OccurredAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
}

func TestEncode_ModeChangedOnASC_ClaimsSignalControlCEType(t *testing.T) {
	em := New("cabinet-poller-1")

	enc, ok, err := em.Encode(model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFlash,
		To:   model.ModeNormal,
	})

	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatal("emitter did not claim ModeChanged on an asc device")
	}
	if got, want := enc.CEType, "openits.signal-control.mode-changed.v1"; got != want {
		t.Errorf("ce-type = %q, want %q", got, want)
	}
}

func TestEncode_ModeChangedOnASC_MapsControllerModeIdentities(t *testing.T) {
	// ModeNormal maps to mode-FREE, not mode-normal: upstream collapsed
	// "normal" into "free" because NTCIP and signal technicians treat
	// uncoordinated-actuated operation as one mode. A `mode-normal` identity
	// does exist, but it belongs to openits-dms-types and means something else.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFlash,
		To:   model.ModeNormal,
	}, "cabinet-poller-1")

	if want := "openits-signal-control-types:mode-flash"; got.GetPrior() != want {
		t.Errorf("prior = %q, want %q", got.GetPrior(), want)
	}
	if want := "openits-signal-control-types:mode-free"; got.GetCurrent() != want {
		t.Errorf("current = %q, want %q", got.GetCurrent(), want)
	}
}

func TestEncode_ModeChangedToStandby_IsNotClaimed(t *testing.T) {
	// ModeStandby has no controller-mode identity upstream (the set is
	// coordinated/free/flash/preempt/priority/manual/off). Encoding it would
	// mean inventing a near-neighbour, so the event is not claimed and the
	// caller's loud-drop path fires. A visible drop beats a wrong mode.
	enc, ok, err := New("cabinet-poller-1").Encode(model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeNormal,
		To:   model.ModeStandby,
	})

	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if ok {
		t.Errorf("emitter claimed a mode with no upstream identity; payload=%v", enc)
	}
}

func TestEncode_ModeChangedFromUnmappable_StillClaimedWithPriorAbsent(t *testing.T) {
	// The reverse: `current` is mandatory but `prior` is optional ("absent when
	// the device just started up"). An unmappable From must not suppress an
	// otherwise-encodable transition.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeStandby,
		To:   model.ModeFlash,
	}, "cabinet-poller-1")

	if got.GetPrior() != "" {
		t.Errorf("prior = %q, want empty for an unmappable From", got.GetPrior())
	}
	if want := "openits-signal-control-types:mode-flash"; got.GetCurrent() != want {
		t.Errorf("current = %q, want %q", got.GetCurrent(), want)
	}
}
