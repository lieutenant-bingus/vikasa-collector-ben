package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// hangingAdapter's Read blocks until release is closed and ignores its
// context: the third-party adapter the runner must survive.
type hangingAdapter struct {
	release  chan struct{}
	started  chan struct{} // receives once per Read call
	mu       sync.Mutex
	inFlight int
	maxSeen  int
	calls    int
}

func newHanging() *hangingAdapter {
	return &hangingAdapter{release: make(chan struct{}), started: make(chan struct{}, 64)}
}

func (h *hangingAdapter) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "test", DeviceKind: "asc", Caps: adapter.CapState}
}
func (h *hangingAdapter) Close() error { return nil }
func (h *hangingAdapter) Read(context.Context) (*model.Snapshot, error) {
	h.mu.Lock()
	h.calls++
	h.inFlight++
	h.maxSeen = max(h.maxSeen, h.inFlight)
	h.mu.Unlock()
	h.started <- struct{}{}
	<-h.release
	h.mu.Lock()
	h.inFlight--
	h.mu.Unlock()
	return nil, context.Canceled
}

// Shutdown must not wait on an adapter that ignores its context: one sick
// device can never stall the cabinet, and that includes stopping it.
func TestRunReturnsOnShutdownWhileAReadHangs(t *testing.T) {
	h := newHanging()
	t.Cleanup(func() { close(h.release) })
	r := New(h, Config{DeviceID: "asc-1", Interval: time.Hour, Timeout: time.Hour, Engine: synth.NewEngine(synth.NewSignalDiffer()), Out: func([]model.Event) {}})
	r.SetJitter(func(time.Duration) time.Duration { return 0 })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	<-h.started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel while the adapter's Read was hung")
	}
}

// A read that outlives its poll timeout is a failed poll, so a hung adapter
// surfaces as unreachable instead of silently freezing its device's loop.
// The runner must never start a second Read on the same adapter while the
// first is still running.
func TestHungReadIsAFailedPollAndNeverOverlaps(t *testing.T) {
	h := newHanging()
	t.Cleanup(func() { close(h.release) })

	var mu sync.Mutex
	var events []model.Event
	gotDown := make(chan struct{})
	out := func(evs []model.Event) {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range evs {
			if ds, ok := ev.(model.DeviceStatusChanged); ok && !ds.Reachable && len(events) == 0 {
				close(gotDown)
			}
		}
		events = append(events, evs...)
	}
	r := New(h, Config{DeviceID: "asc-1", Interval: 10 * time.Millisecond, Timeout: 10 * time.Millisecond, Engine: synth.NewEngine(synth.NewSignalDiffer()), Out: out})
	r.SetJitter(func(time.Duration) time.Duration { return 0 })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	select {
	case <-gotDown:
	case <-time.After(2 * time.Second):
		t.Fatal("a read hung past its poll timeout was never reported as a failed poll")
	}
	time.Sleep(100 * time.Millisecond) // ten more poll intervals for an overlap to show
	cancel()
	<-done

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.maxSeen != 1 || h.calls != 1 {
		t.Fatalf("adapter saw %d Read calls, up to %d at once; want exactly 1 while it is hung", h.calls, h.maxSeen)
	}
}

// An adapter panic is a collector fault (ADR 0019 clause 12), so it is
// counted where the runner recovers it, not just turned into a failed poll.
func TestAdapterPanicIsCounted(t *testing.T) {
	var c stats.Counters
	r := New(&scriptedAdapter{script: []func() (*model.Snapshot, error){panicRead, okSnap}}, Config{
		DeviceID: "asc-1", Interval: 5 * time.Millisecond,
		Engine: synth.NewEngine(synth.NewSignalDiffer()), Out: func([]model.Event) {}, Counters: &c,
	})
	r.SetJitter(func(time.Duration) time.Duration { return 0 })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for c.AdapterPanics.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if got := c.AdapterPanics.Load(); got != 1 {
		t.Fatalf("AdapterPanics = %d, want 1", got)
	}
}

// Every poll outcome reaches the device tracker /status and readiness read:
// a failure, then a success that latches readiness.
func TestPollOutcomesReachTheDeviceTracker(t *testing.T) {
	devices := stats.NewDevices([]string{"asc-1"})
	r := New(&scriptedAdapter{script: []func() (*model.Snapshot, error){failRead, okSnap}}, Config{
		DeviceID: "asc-1", Interval: 5 * time.Millisecond,
		Engine: synth.NewEngine(synth.NewSignalDiffer()), Out: func([]model.Event) {}, Devices: devices,
	})
	r.SetJitter(func(time.Duration) time.Duration { return 0 })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for !devices.EverPolled() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	s := devices.Snapshot()[0]
	if !devices.EverPolled() || s.LastError != "timeout" || s.LastSuccess.IsZero() {
		t.Fatalf("tracker = %+v (ever polled %v); want the failure and then the success", s, devices.EverPolled())
	}
}
