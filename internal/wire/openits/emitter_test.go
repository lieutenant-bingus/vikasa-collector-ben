package openits

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

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
