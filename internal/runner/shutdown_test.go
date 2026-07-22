package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// slowASC blocks in Read until the context is cancelled — i.e. it is mid-poll
// exactly when shutdown arrives.
type slowASC struct{ started chan struct{} }

func (s *slowASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "test", DeviceKind: "asc", Caps: adapter.CapState}
}
func (s *slowASC) Close() error { return nil }
func (s *slowASC) Read(ctx context.Context) (*model.Snapshot, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done() // still talking to the device when the collector is told to stop
	return nil, ctx.Err()
}

// Shutting the collector down must never be reported as the device going
// unreachable. A cancelled poll is our own doing, not the device's fault —
// emitting device-down here would page someone for a clean restart.
func TestShutdownDoesNotSlanderTheDevice(t *testing.T) {
	var mu sync.Mutex
	var events []model.Event
	out := func(evs []model.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evs...)
	}

	dev := &slowASC{started: make(chan struct{}, 1)}
	r := New(dev, "asc-1", 5*time.Millisecond, time.Second,
		synth.NewEngine(synth.NewSignalDiffer()), out)
	r.SetJitter(func(time.Duration) time.Duration { return 0 })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()

	<-dev.started // wait until we are genuinely inside Read
	cancel()      // shutdown arrives mid-poll

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not exit after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, ev := range events {
		if h, ok := ev.(model.DeviceStatusChanged); ok && !h.Reachable {
			t.Fatalf("shutdown emitted a false device-unreachable event: %+v", h)
		}
	}
}
