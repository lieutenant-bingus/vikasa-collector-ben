package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// recordingASC records whether it was ever read or closed, so a test can
// see what Run left behind after it returned.
type recordingASC struct {
	mu     sync.Mutex
	reads  int
	closed bool
}

func (r *recordingASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}

func (r *recordingASC) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *recordingASC) Read(context.Context) (*model.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: time.Now().UTC()}, nil
}

func startNATS(t *testing.T) *server.Server {
	t.Helper()
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	return ns
}

// A device whose adapter cannot be built is a boot failure, and a boot
// failure must leave nothing running: before this test, devices configured
// ahead of the broken one were already polling when Run returned, nothing
// stopped or closed them, and the collector had already announced
// a started event for a boot that then failed.
func TestRunBootFailureLeavesNothingRunning(t *testing.T) {
	ns := startNATS(t)

	good := &recordingASC{}
	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return good, nil })
	reg.Register(adapter.Descriptor{Vendor: "broken", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return nil, errors.New("dial refused") })

	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{
			{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Millisecond},
			{ID: "asc-2", Vendor: "broken", DeviceKind: "asc", PollInterval: time.Millisecond},
		},
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	published := make(chan string, 16)
	if _, err := nc.Subscribe(">", func(m *nats.Msg) {
		if ceType := m.Header.Get("ce-type"); ceType != "" {
			published <- ceType
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}); err == nil {
		t.Fatal("Run must fail when a device's adapter cannot be built")
	}

	good.mu.Lock()
	closed := good.closed
	good.mu.Unlock()
	if !closed {
		t.Error("adapter built before the failing one was never closed")
	}

	// A runner left behind would keep reading after Run returned. Give one
	// the chance to show itself: many poll intervals, not a race window.
	time.Sleep(50 * time.Millisecond)
	good.mu.Lock()
	reads := good.reads
	good.mu.Unlock()
	if reads != 0 {
		t.Errorf("adapter was read %d times; no device may poll on a failed boot", reads)
	}

	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	select {
	case ceType := <-published:
		t.Errorf("published %s during a boot that failed", ceType)
	default:
	}
}

// stuckCloser polls fine but never returns from Close.
type stuckCloser struct{ recordingASC }

func (s *stuckCloser) Close() error { select {} }

// Shutdown is bounded even when an adapter's Close never returns: the
// process must exit for its supervisor, not wait on a third-party bug.
func TestRunShutdownIsBoundedWhenAnAdapterCloseHangs(t *testing.T) {
	ns := startNATS(t)
	old := closeTimeout
	closeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { closeTimeout = old })

	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &stuckCloser{}, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Hour}},
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}) }()
	time.Sleep(200 * time.Millisecond) // let it boot; nothing here signals readiness yet
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return: shutdown waited on an adapter Close that never returns")
	}
}

// ADR 0020 clause 1, end to end: the collector boots with no broker, keeps
// running, and publishes what it queued once the broker appears. Before,
// Run dialled NATS during boot and returned an error, so a broker that lost
// the start-up race read as a failed boot.
func TestRunBootsWithoutABrokerAndPublishesWhenOneArrives(t *testing.T) {
	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &recordingASC{}, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Hour}},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, cfg, reg, Options{NATSURL: fmt.Sprintf("nats://127.0.0.1:%d", port), Version: "test"})
	}()

	select {
	case err := <-runErr:
		t.Fatalf("Run returned %v with no broker; boot must not depend on it", err)
	case <-time.After(500 * time.Millisecond):
	}

	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)

	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := js.Stream(t.Context(), "OPENITS-COLLECTOR-US-GA-METRO-D01")
		if err == nil {
			if info, err := st.Info(t.Context()); err == nil && info.State.Msgs >= 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("instance.started, queued before the broker existed, never reached the stream")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run = %v after a clean shutdown", err)
	}
}

// faultyASC reports one standing fault on every poll and counts its reads.
type faultyASC struct {
	mu    sync.Mutex
	reads int
}

func (f *faultyASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}
func (f *faultyASC) Close() error { return nil }
func (f *faultyASC) Read(context.Context) (*model.Snapshot, error) {
	f.mu.Lock()
	f.reads++
	f.mu.Unlock()
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: time.Now().UTC(), Facets: []model.Facet{
		model.FaultSet{Faults: []model.Fault{{ID: "cabinet-door", Severity: model.SeverityMinor,
			Category: model.CategoryCabinet, Description: "Cabinet door open"}}},
	}}, nil
}
func (f *faultyASC) readCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.reads }

// ADR 0017, end to end: restarting the collector while a fault stands does
// not raise it again. Before, every restart — every rollout — re-raised
// every standing fault, with a new ce-id no consumer could dedupe.
func TestRestartDoesNotReRaiseStandingFaults(t *testing.T) {
	ns := startNATS(t)
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 20 * time.Millisecond}},
	}
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	var mu sync.Mutex
	raised := 0
	if _, err := nc.Subscribe(">", func(m *nats.Msg) {
		if m.Header.Get("ce-type") == "openits.signal-control.fault-raised.v1" {
			mu.Lock()
			raised++
			mu.Unlock()
		}
	}); err != nil {
		t.Fatal(err)
	}
	countRaised := func() int { mu.Lock(); defer mu.Unlock(); return raised }

	lifetime := func(until func(dev *faultyASC) bool) {
		dev := &faultyASC{}
		reg := adapter.NewRegistry()
		reg.Register(dev.Descriptor(), func(string, map[string]any) (adapter.Adapter, error) { return dev, nil })
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}) }()
		deadline := time.Now().Add(10 * time.Second)
		for !until(dev) {
			if time.Now().After(deadline) {
				t.Fatal("lifetime condition never met")
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	lifetime(func(*faultyASC) bool { return countRaised() == 1 })
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	lifetime(func(dev *faultyASC) bool { return dev.readCount() >= 10 }) // well past seeding
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := countRaised(); got != 1 {
		t.Fatalf("fault-raised published %d times across a restart, want 1", got)
	}
}
