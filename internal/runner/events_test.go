package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

type scriptedEventAdapter struct {
	mu     sync.Mutex
	script []func() ([]model.Event, error)
	callN  int
}

func (s *scriptedEventAdapter) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "test", DeviceKind: "asc", Caps: adapter.CapEvents}
}
func (s *scriptedEventAdapter) Close() error { return nil }
func (s *scriptedEventAdapter) Fetch(context.Context) ([]model.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(s.callN, len(s.script)-1)
	s.callN++
	return s.script[i]()
}

func TestEventRunnerForwardsFetchedEventsWithDeviceKind(t *testing.T) {
	var mu sync.Mutex
	var got []model.Event
	done := make(chan struct{})
	out := func(evs []model.Event) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, evs...)
		if len(got) >= 1 {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}

	r := NewEventRunner(&scriptedEventAdapter{script: []func() ([]model.Event, error){
		func() ([]model.Event, error) {
			return []model.Event{model.PhaseLogEvent{
				Base:  model.Base{DeviceID: "asc-1", OccurredAt: time.Now().UTC()},
				LogID: 7, PhaseNumber: 2, Code: model.PhaseLogBeginGreen,
			}}, nil
		},
	}}, "asc-1", 5*time.Millisecond, 0, out)
	r.SetJitter(func(time.Duration) time.Duration { return 0 })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go r.Run(ctx)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out waiting for event runner")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	ev, ok := got[0].(model.PhaseLogEvent)
	if !ok {
		t.Fatalf("got %T", got[0])
	}
	if ev.DeviceKind != "asc" {
		t.Fatalf("DeviceKind = %q, want asc", ev.DeviceKind)
	}
}
