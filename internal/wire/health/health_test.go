package health

import (
	"runtime"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
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

var instance = Instance{CollectorID: "cab-042-collector", Version: "v0.2.0", ConfigRevision: "24790e5d193f", BootID: "cd5a78e48f9b1b4a"}

// ADR 0019 clause 1: the collector's own events live on the instance root,
// and every one states the collector's identity (clause 7) — collector_id
// in the body, while ce-source carries the site.
func TestStartedGolden(t *testing.T) {
	enc, ok, err := NewHealthEmitter().Encode(Started{Base: model.Base{OccurredAt: at}, Instance: instance})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.instance.started.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	want := `{"occurred_at":"2026-07-12T10:00:00Z","collector_id":"cab-042-collector","version":"v0.2.0","config_revision":"24790e5d193f","boot_id":"cd5a78e48f9b1b4a"}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

func TestStoppingGolden(t *testing.T) {
	enc, ok, err := NewHealthEmitter().Encode(Stopping{Base: model.Base{OccurredAt: at}, Instance: instance, Uptime: 90 * time.Minute})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.instance.stopping.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	want := `{"occurred_at":"2026-07-12T10:00:00Z","collector_id":"cab-042-collector","version":"v0.2.0","config_revision":"24790e5d193f","boot_id":"cd5a78e48f9b1b4a","uptime_s":5400}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

// ADR 0019 clause 9: identity, resource, errors and throughput — flat, and
// every counter cumulative since boot (clause 4), so the newest beat alone
// carries full totals under last-value retention.
func TestHeartbeatGolden(t *testing.T) {
	enc, ok, err := NewHealthEmitter().Encode(Heartbeat{
		Base: model.Base{OccurredAt: at}, Instance: instance, Uptime: 3600 * time.Second,
		Resource: Resource{CPUSecondsTotal: 12.5, MemoryBytes: 20 << 20, HeapObjectsBytes: 4 << 20, Goroutines: 42},
		Counters: stats.Snapshot{EventsPublished: 812, DroppedQueueFull: 1, AdapterPanics: 2},
	})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.instance.heartbeat.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	want := `{"occurred_at":"2026-07-12T10:00:00Z","collector_id":"cab-042-collector","version":"v0.2.0","config_revision":"24790e5d193f","boot_id":"cd5a78e48f9b1b4a","uptime_s":3600,` +
		`"cpu_seconds_total":12.5,"memory_bytes":20971520,"heap_objects_bytes":4194304,"goroutines":42,` +
		`"events_published":812,"dropped_queue_full":1,"dropped_unpublishable":0,"dropped_at_shutdown":0,"dropped_no_emitter":0,"dropped_no_source":0,"encode_errors":0,"adapter_panics":2,"comm_lost_without_contact":0,"facets_rejected":0,"logs_suppressed":0}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

// One runtime/metrics read, no syscalls (clause 9): the values are live.
// The runtime refreshes its CPU estimate at each GC cycle, so the test forces
// one; a long-running collector collects continually.
func TestReadResourceIsLive(t *testing.T) {
	runtime.GC()
	r := ReadResource()
	if r.Goroutines == 0 || r.MemoryBytes == 0 || r.HeapObjectsBytes == 0 || r.CPUSecondsTotal <= 0 {
		t.Fatalf("ReadResource() = %+v, want every field populated", r)
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

// ADR 0019 clause 10: a shipped log record keeps its structured attributes.
func TestLogGolden(t *testing.T) {
	enc, ok, err := NewHealthEmitter().Encode(Log{
		Base: model.Base{OccurredAt: at}, Instance: instance,
		Level: "WARN", Message: "adapter fault", Attrs: map[string]string{"device": "asc-1", "adapter": "ntcip-asc"},
	})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.instance.log.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	want := `{"occurred_at":"2026-07-12T10:00:00Z","collector_id":"cab-042-collector","version":"v0.2.0","config_revision":"24790e5d193f","boot_id":"cd5a78e48f9b1b4a","level":"WARN","message":"adapter fault","attrs":{"adapter":"ntcip-asc","device":"asc-1"}}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}
