package health

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var at = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func TestDeviceStatusChangedGolden(t *testing.T) {
	e := NewHealthEmitter()
	enc, ok, err := e.Encode(model.DeviceStatusChanged{
		Base:      model.Base{DeviceID: "asc-1", OccurredAt: at},
		Reachable: false, Reason: "read timeout", ConsecutiveFailures: 3,
	})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.health.device-status-changed.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	if enc.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", enc.ContentType)
	}
	want := `{"device_id":"asc-1","occurred_at":"2026-07-12T10:00:00Z","reachable":false,"reason":"read timeout","consecutive_failures":3}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

func TestCollectorStartedGolden(t *testing.T) {
	enc, ok, err := NewHealthEmitter().Encode(model.CollectorStarted{
		Base: model.Base{OccurredAt: at}, Version: "dev",
	})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.health.collector-started.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	want := `{"occurred_at":"2026-07-12T10:00:00Z","version":"dev"}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

func TestUnmappedEventIsNotOK(t *testing.T) {
	_, ok, err := NewHealthEmitter().Encode(model.PlanChanged{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatal("health emitter must not claim domain events")
	}
}
