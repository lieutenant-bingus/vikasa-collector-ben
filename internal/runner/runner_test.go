package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// scriptedAdapter returns queued outcomes then repeats the last one.
type scriptedAdapter struct {
	mu     sync.Mutex
	script []func() (*model.Snapshot, error)
	callN  int
}

func (s *scriptedAdapter) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "test", DeviceKind: "asc", Caps: adapter.CapState}
}
func (s *scriptedAdapter) Close() error { return nil }
func (s *scriptedAdapter) Read(context.Context) (*model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(s.callN, len(s.script)-1)
	s.callN++
	return s.script[i]()
}

func okSnap() (*model.Snapshot, error) {
	return &model.Snapshot{
		DeviceID: "asc-1", SampledAt: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}},
	}, nil
}
func failRead() (*model.Snapshot, error)  { return nil, errors.New("timeout") }
func panicRead() (*model.Snapshot, error) { panic("adapter bug") }
func nilSnap() (*model.Snapshot, error)   { return nil, nil }

func collect(t *testing.T, script []func() (*model.Snapshot, error), needAtLeast int) []model.Event {
	t.Helper()
	var mu sync.Mutex
	var events []model.Event
	done := make(chan struct{})
	out := func(evs []model.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evs...)
		if len(events) >= needAtLeast {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}
	r := New(&scriptedAdapter{script: script}, "asc-1", 5*time.Millisecond, 0,
		synth.NewEngine(synth.NewSignalDiffer()), out)
	r.SetJitter(func(time.Duration) time.Duration { return 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go r.Run(ctx)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("timed out; got %d events", len(events))
	}
	cancel()
	mu.Lock()
	defer mu.Unlock()
	return events
}

func TestHealthySequenceEmitsReports(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){okSnap}, 2)
	for _, ev := range events[:2] {
		if ev.EventKind() != "operational-status-report" {
			t.Fatalf("unexpected event %q", ev.EventKind())
		}
	}
}

func TestFailureThenRecoveryEmitsHealthTransitions(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){failRead, failRead, okSnap}, 3)
	// Expect: unreachable (poll 1), NO event for poll 2 (still down),
	// then reachable + status report (poll 3).
	var health []model.DeviceStatusChanged
	for _, ev := range events {
		if h, ok := ev.(model.DeviceStatusChanged); ok {
			health = append(health, h)
		}
	}
	if len(health) != 2 {
		t.Fatalf("health transitions = %d, want 2 (down, up): %+v", len(health), health)
	}
	if health[0].Reachable || health[0].ConsecutiveFailures != 1 {
		t.Fatalf("first transition should be unreachable/1: %+v", health[0])
	}
	if !health[1].Reachable {
		t.Fatalf("second transition should be reachable: %+v", health[1])
	}
}

func TestPanickingAdapterIsAFailedPollNotACrash(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){panicRead, okSnap}, 2)
	if len(events) < 2 {
		t.Fatal("runner must survive adapter panic and keep polling")
	}
	if h, ok := events[0].(model.DeviceStatusChanged); !ok || h.Reachable {
		t.Fatalf("first event should be unreachable health transition, got %+v", events[0])
	}
}

// A broken adapter that returns (nil, nil) — no error, but no snapshot
// either — must not nil-deref the runner (pollOnce stamps DeviceKind on the
// snapshot and hands it to Engine.Apply). It must be treated as a failed
// poll, exactly like a returned error, and the runner must keep polling.
func TestNilSnapshotIsAFailedPollNotACrash(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){nilSnap, okSnap}, 2)
	if len(events) < 2 {
		t.Fatal("runner must survive a nil snapshot with no error and keep polling")
	}
	if h, ok := events[0].(model.DeviceStatusChanged); !ok || h.Reachable {
		t.Fatalf("first event should be unreachable health transition, got %+v", events[0])
	}
}

// The runner stamps the device kind from the adapter's own Descriptor, so an
// adapter can neither forget it nor misreport it. This covers BOTH paths: the
// snapshot (which reaches synth) and health events (which do not come from a
// snapshot at all).
func TestRunnerStampsDeviceKindFromDescriptor(t *testing.T) {
	var mu sync.Mutex
	var events []model.Event
	done := make(chan struct{})
	var once sync.Once
	out := func(evs []model.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evs...)
		var health, domain bool
		for _, ev := range events {
			if _, ok := ev.(model.DeviceStatusChanged); ok {
				health = true
			} else {
				domain = true
			}
		}
		// Wait for what this test actually asserts — one of each path — not a
		// count. The first two events are both health (down, then recovery);
		// the domain event only arrives on a later r.out call.
		if health && domain {
			once.Do(func() { close(done) })
		}
	}

	// Fails once (-> health event), then succeeds (-> domain event via synth).
	script := []func() (*model.Snapshot, error){failRead, okSnap}
	r := New(&scriptedAdapter{script: script}, "asc-1", 5*time.Millisecond, 0,
		synth.NewEngine(synth.NewSignalDiffer()), out)
	r.SetJitter(func(time.Duration) time.Duration { return 0 })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go r.Run(ctx)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	var sawHealth, sawDomain bool
	for _, ev := range events {
		if got := ev.EventDeviceKind(); got != "asc" {
			t.Errorf("%s: DeviceKind = %q, want %q (scriptedAdapter's Descriptor says asc)", ev.EventKind(), got, "asc")
		}
		if _, ok := ev.(model.DeviceStatusChanged); ok {
			sawHealth = true
		} else {
			sawDomain = true
		}
	}
	if !sawHealth || !sawDomain {
		t.Fatalf("test must cover both paths: health=%v domain=%v", sawHealth, sawDomain)
	}
}
