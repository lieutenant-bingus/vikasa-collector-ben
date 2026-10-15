package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// ADR 0019 clause 5: a stable per-collector offset — the same every boot,
// different across collectors, always inside the interval.
func TestPhaseIsStableAndSpread(t *testing.T) {
	const iv = 300 * time.Second
	a1, a2 := phaseFor("cab-001-collector", iv), phaseFor("cab-001-collector", iv)
	b := phaseFor("cab-002-collector", iv)
	if a1 != a2 {
		t.Fatalf("phase not stable: %v vs %v", a1, a2)
	}
	if a1 == b {
		t.Fatalf("two collectors share phase %v; the fleet would not spread", a1)
	}
	seen := map[time.Duration]bool{}
	for i := range 1000 {
		p := phaseFor("cab-"+string(rune('a'+i%26))+time.Duration(i).String(), iv)
		if p < 0 || p >= iv {
			t.Fatalf("phase %v outside [0, %v)", p, iv)
		}
		seen[p.Truncate(30*time.Second)] = true
	}
	if len(seen) < 10 {
		t.Fatalf("1000 collectors fell into only %d of 10 thirty-second buckets", len(seen))
	}
}

// Beats land on wall-clock slots ≡ phase (mod interval): regular, and the
// same slots across a restart, so a hub's dead-man timer can predict them.
func TestNextBeatIsTheNextAlignedSlot(t *testing.T) {
	const iv = 300 * time.Second
	phase := 42 * time.Second
	epoch := time.Unix(0, 0).UTC()
	for _, tc := range []struct{ now, want time.Time }{
		{epoch, epoch.Add(42 * time.Second)},
		{epoch.Add(42 * time.Second), epoch.Add(342 * time.Second)}, // exactly on a slot: the next one
		{epoch.Add(100 * time.Second), epoch.Add(342 * time.Second)},
		{epoch.Add(3*time.Hour + 41*time.Second), epoch.Add(3*time.Hour + 42*time.Second)},
	} {
		if got := nextBeat(tc.now, iv, phase); !got.Equal(tc.want) {
			t.Errorf("nextBeat(%v) = %v, want %v", tc.now.Sub(epoch), got.Sub(epoch), tc.want.Sub(epoch))
		}
	}
}

// End to end (ADR 0019): started at boot, heartbeats on the instance root
// addressed by site, and stopping on a clean shutdown — through the real Run.
func TestInstanceEventsReachTheBus(t *testing.T) {
	ns := startNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	var mu sync.Mutex
	subjects := map[string]string{} // ce-type -> subject
	if _, err := nc.Subscribe("openits-collector.>", func(m *nats.Msg) {
		mu.Lock()
		subjects[m.Header.Get("ce-type")] = m.Subject
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	seen := func(ceType string) string { mu.Lock(); defer mu.Unlock(); return subjects[ceType] }

	reg := adapter.NewRegistry()
	dev := &recordingASC{}
	reg.Register(dev.Descriptor(), func(string, map[string]any) (adapter.Adapter, error) { return dev, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1", HeartbeatInterval: 100 * time.Millisecond,
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Hour}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}) }()

	eventually(t, "a heartbeat", func() bool { return seen("openits-collector.instance.heartbeat.v1") != "" })
	if got := seen("openits-collector.instance.heartbeat.v1"); got != "openits-collector.us-ga.metro.d01.instance.cab-1.heartbeat" {
		t.Fatalf("heartbeat subject = %q", got)
	}
	if seen("openits-collector.instance.started.v1") == "" {
		t.Fatal("no started event")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stopping on clean shutdown", func() bool { return seen("openits-collector.instance.stopping.v1") != "" })
}

// panickingASC always panics: a collector fault that logs at warn.
type panickingASC struct{}

func (panickingASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}
func (panickingASC) Close() error                                  { return nil }
func (panickingASC) Read(context.Context) (*model.Snapshot, error) { panic("adapter bug") }

// ADR 0019 clause 10, end to end: a warn-level record — here clause 12's
// adapter-fault warning — reaches the bus as instance.log with its
// structured attributes, while still going to the local log.
func TestWarnLogsReachTheBusAsInstanceLog(t *testing.T) {
	ns := startNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	logs := make(chan map[string]any, 16)
	if _, err := nc.Subscribe("openits-collector.>", func(m *nats.Msg) {
		if m.Header.Get("ce-type") == "openits-collector.instance.log.v1" {
			var body map[string]any
			_ = json.Unmarshal(m.Data, &body)
			logs <- body
		}
	}); err != nil {
		t.Fatal(err)
	}

	reg := adapter.NewRegistry()
	reg.Register(panickingASC{}.Descriptor(), func(string, map[string]any) (adapter.Adapter, error) { return panickingASC{}, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 20 * time.Millisecond}},
	}
	var local bytes.Buffer
	var localMu sync.Mutex
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test",
			Logger: slog.New(slog.NewTextHandler(&lockedWriter{w: &local, mu: &localMu}, nil))})
	}()
	defer func() { cancel(); <-done }()

	select {
	case body := <-logs:
		attrs, _ := body["attrs"].(map[string]any)
		if body["level"] != "WARN" || !strings.Contains(body["message"].(string), "adapter fault") ||
			attrs["device"] != "asc-1" || attrs["adapter"] != "fixture-asc" {
			t.Fatalf("instance.log = %v", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no instance.log event")
	}
	localMu.Lock()
	defer localMu.Unlock()
	if !strings.Contains(local.String(), "adapter fault") {
		t.Fatal("the record did not also reach the local log")
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
