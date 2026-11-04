package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

type memCursors struct {
	mu     sync.Mutex
	seeded chan struct{}
	c      map[string][]byte
}

func newMemCursors(seeded bool) *memCursors {
	m := &memCursors{seeded: make(chan struct{}), c: map[string][]byte{}}
	if seeded {
		close(m.seeded)
	}
	return m
}
func (m *memCursors) Seeded() <-chan struct{} { return m.seeded }
func (m *memCursors) Cursor(id string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.c[id]
}
func (m *memCursors) RecordCursor(id string, c []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.c[id] = c
}

type fakeLog struct {
	mu     sync.Mutex
	afters []string
	read   func(after []byte) ([]model.LogEvent, []byte, error)
}

func (f *fakeLog) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fake", DeviceKind: "asc", Caps: adapter.CapState | adapter.CapEvents}
}
func (f *fakeLog) Close() error                 { return nil }
func (f *fakeLog) EventInterval() time.Duration { return 10 * time.Millisecond }
func (f *fakeLog) ReadEvents(_ context.Context, after []byte) ([]model.LogEvent, []byte, error) {
	f.mu.Lock()
	f.afters = append(f.afters, string(after))
	f.mu.Unlock()
	return f.read(after)
}

func newEvents(dev adapter.EventReader, store CursorStore, out func([]model.Event), counters *stats.Counters) *EventRunner {
	return NewEvents(dev, EventConfig{
		DeviceID: "asc-1", Store: store, Out: out, Counters: counters,
		Log: slog.New(slog.DiscardHandler), Jitter: func(time.Duration) time.Duration { return 0 },
	})
}

func hr(code uint16) model.LogEvent {
	return model.HighResEvent{Base: model.Base{DeviceID: "spoofed", DeviceKind: "dms", OccurredAt: time.Unix(1, 0)}, Code: code, Param: 1}
}

// Events are stamped, handed out, and only then is the cursor advanced.
func TestEventRunnerPublishesThenAdvances(t *testing.T) {
	store := newMemCursors(true)
	store.c["asc-1"] = []byte("10")
	var got []model.Event
	dev := &fakeLog{read: func(after []byte) ([]model.LogEvent, []byte, error) {
		if string(store.Cursor("asc-1")) != "10" || len(got) != 0 {
			t.Error("cursor advanced before the events were handed out")
		}
		return []model.LogEvent{hr(1), hr(7)}, []byte("12"), nil
	}}
	r := newEvents(dev, store, func(evs []model.Event) { got = append(got, evs...) }, nil)
	r.readOnce(t.Context())

	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	for _, e := range got {
		if e.EventDeviceID() != "asc-1" || e.EventDeviceKind() != "asc" {
			t.Errorf("event not stamped by the core: %s/%s", e.EventDeviceID(), e.EventDeviceKind())
		}
	}
	if c := string(store.Cursor("asc-1")); c != "12" {
		t.Errorf("cursor = %q, want 12", c)
	}
}

// A failed read keeps the cursor and retries from it; it reports nothing
// about reachability, which is the state poll's.
func TestEventRunnerFailedReadKeepsCursor(t *testing.T) {
	store := newMemCursors(true)
	store.c["asc-1"] = []byte("5")
	fail := true
	dev := &fakeLog{read: func(after []byte) ([]model.LogEvent, []byte, error) {
		if fail {
			return nil, nil, errors.New("connection refused")
		}
		return []model.LogEvent{hr(1)}, []byte("6"), nil
	}}
	var outs int
	r := newEvents(dev, store, func([]model.Event) { outs++ }, nil)
	r.readOnce(t.Context())
	r.readOnce(t.Context())
	fail = false
	r.readOnce(t.Context())

	if outs != 1 {
		t.Errorf("Out called %d times, want once (no health events from the log loop)", outs)
	}
	for i, a := range dev.afters {
		if a != "5" {
			t.Errorf("read %d after %q, want every read from 5", i, a)
		}
	}
	if c := string(store.Cursor("asc-1")); c != "6" {
		t.Errorf("cursor = %q, want 6", c)
	}
}

func TestEventRunnerPanicIsAnAdapterFault(t *testing.T) {
	store := newMemCursors(true)
	counters := new(stats.Counters)
	dev := &fakeLog{read: func([]byte) ([]model.LogEvent, []byte, error) { panic("boom") }}
	r := newEvents(dev, store, func([]model.Event) { t.Error("no events expected") }, counters)
	r.readOnce(t.Context())
	if counters.AdapterPanics.Load() != 1 {
		t.Errorf("AdapterPanics = %d, want 1", counters.AdapterPanics.Load())
	}
	if store.Cursor("asc-1") != nil {
		t.Error("a panic must not move the cursor")
	}
}

func TestEventRunnerRejectsAnOversizedCursor(t *testing.T) {
	store := newMemCursors(true)
	dev := &fakeLog{read: func([]byte) ([]model.LogEvent, []byte, error) {
		return []model.LogEvent{hr(1)}, make([]byte, adapter.MaxCursorBytes+1), nil
	}}
	r := newEvents(dev, store, func([]model.Event) { t.Error("events of a rejected read must not publish") }, nil)
	r.readOnce(t.Context())
	if store.Cursor("asc-1") != nil {
		t.Error("oversized cursor stored")
	}
}

// Nothing is read before stored cursors load: reading first would prime at
// the log's end and skip what was logged while the collector was down.
func TestEventRunnerWaitsForCursors(t *testing.T) {
	store := newMemCursors(false)
	dev := &fakeLog{read: func([]byte) ([]model.LogEvent, []byte, error) { return nil, []byte("1"), nil }}
	r := newEvents(dev, store, func([]model.Event) {}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	dev.mu.Lock()
	n := len(dev.afters)
	dev.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d reads before cursors loaded", n)
	}
	close(store.seeded)
	deadline := time.Now().Add(5 * time.Second)
	for {
		dev.mu.Lock()
		n = len(dev.afters)
		dev.mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if n == 0 {
		t.Fatal("never read after cursors loaded")
	}
}

// A hung read is abandoned at the timeout and never overlapped.
func TestEventRunnerAbandonsAHungRead(t *testing.T) {
	store := newMemCursors(true)
	release := make(chan struct{})
	dev := &fakeLog{read: func([]byte) ([]model.LogEvent, []byte, error) {
		<-release
		return nil, nil, nil
	}}
	r := newEvents(dev, store, func([]model.Event) {}, nil)
	r.readOnce(t.Context())
	r.readOnce(t.Context())
	dev.mu.Lock()
	calls := len(dev.afters)
	dev.mu.Unlock()
	close(release)
	if calls != 1 {
		t.Errorf("ReadEvents called %d times while the first was hung, want 1", calls)
	}
}
