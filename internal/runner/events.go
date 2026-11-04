package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// CursorStore keeps each device's event-log cursor across restarts.
// durable.State is one.
type CursorStore interface {
	// Seeded is closed once stored cursors have loaded.
	Seeded() <-chan struct{}
	Cursor(deviceID string) []byte
	RecordCursor(deviceID string, cursor []byte)
}

// EventRunner reads one device's event log on its own goroutine (ADR 0025).
//
// It shares the poll loop's isolation — a per-read timeout, an abandoned
// read is never overlapped, a panic is a counted adapter fault — but none of
// its health reporting: reachability is the state poll's to report, and a
// device whose log endpoint fails while its state poll answers is not down.
// A failed read is logged and retried from the same cursor, so nothing the
// device logged is skipped.
type EventRunner struct {
	dev        adapter.EventReader
	deviceID   string
	deviceKind string
	interval   time.Duration
	timeout    time.Duration
	store      CursorStore
	out        func([]model.Event)
	jitter     func(time.Duration) time.Duration

	failing bool
	pending chan eventResult

	counters *stats.Counters
	log      *slog.Logger
}

type eventResult struct {
	events []model.LogEvent
	next   []byte
	err    error
}

// EventConfig is what an EventRunner needs beyond the adapter itself.
type EventConfig struct {
	DeviceID string
	// Timeout bounds one read; zero means the adapter's EventInterval.
	Timeout time.Duration
	Store   CursorStore
	// Out receives each read's events, oldest first. It must not block.
	Out      func([]model.Event)
	Counters *stats.Counters
	Log      *slog.Logger
	Jitter   func(time.Duration) time.Duration
}

// NewEvents builds an EventRunner for one device. A non-positive
// EventInterval panics: the adapter's factory validates its configuration.
func NewEvents(dev adapter.EventReader, c EventConfig) *EventRunner {
	interval := dev.EventInterval()
	if interval <= 0 {
		panic(fmt.Sprintf("runner.NewEvents: non-positive event interval %v for device %s", interval, c.DeviceID))
	}
	if c.Timeout <= 0 {
		c.Timeout = interval
	}
	if c.Counters == nil {
		c.Counters = new(stats.Counters)
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	if c.Jitter == nil {
		c.Jitter = rand.N[time.Duration]
	}
	return &EventRunner{
		dev: dev, deviceID: c.DeviceID, deviceKind: dev.Descriptor().DeviceKind,
		interval: interval, timeout: c.Timeout, store: c.Store, out: c.Out, jitter: c.Jitter,
		counters: c.Counters, log: c.Log,
	}
}

// Run blocks until ctx is cancelled. Nothing is read until stored cursors
// have loaded: reading before then would prime a fresh cursor at the log's
// end and skip everything logged while the collector was down.
func (r *EventRunner) Run(ctx context.Context) {
	select {
	case <-r.store.Seeded():
	case <-ctx.Done():
		return
	}
	select {
	case <-time.After(r.jitter(r.interval)):
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.readOnce(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (r *EventRunner) readOnce(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	after := r.store.Cursor(r.deviceID)
	res := r.read(rctx, after)
	var fault adapterFault
	if res.err == nil && len(res.next) > adapter.MaxCursorBytes {
		res.err = adapterFault{fmt.Sprintf("cursor of %d bytes exceeds %d", len(res.next), adapter.MaxCursorBytes)}
	}
	switch {
	case errors.As(res.err, &fault):
		r.counters.AdapterPanics.Add(1)
		r.log.Warn("event log adapter fault; the cursor is unchanged",
			"device", r.deviceID, "adapter", r.dev.Descriptor().Key(), "fault", fault.reason)
		return
	case res.err != nil:
		if ctx.Err() != nil {
			return // our own shutdown
		}
		if !r.failing {
			r.failing = true
			r.log.Warn("event log read failed; retrying from the same cursor", "device", r.deviceID, "err", res.err)
		}
		return
	}
	if r.failing {
		r.failing = false
		r.log.Info("event log read recovered", "device", r.deviceID)
	}

	if len(res.events) > 0 {
		evs := make([]model.Event, 0, len(res.events))
		for _, e := range res.events {
			if e == nil {
				continue
			}
			evs = append(evs, e.Stamped(r.deviceID, r.deviceKind))
		}
		r.out(evs)
	}
	// Recorded after Out: a crash between the two replays this read's events
	// on the next boot — the same rows, so the same ce-id, deduplicated by
	// the bus — rather than skipping them.
	if res.next != nil {
		r.store.RecordCursor(r.deviceID, res.next)
	}
}

// read bounds ReadEvents by rctx, exactly as Runner.read bounds Read.
func (r *EventRunner) read(rctx context.Context, after []byte) eventResult {
	if r.pending != nil {
		select {
		case <-r.pending:
			r.pending = nil
		default:
			return eventResult{err: errors.New("previous event read is still running")}
		}
	}
	ch := make(chan eventResult, 1)
	go func() { ch <- r.readGuarded(rctx, after) }()
	select {
	case res := <-ch:
		return res
	case <-rctx.Done():
		r.pending = ch
		return eventResult{err: fmt.Errorf("event read did not return within %v: %w", r.timeout, rctx.Err())}
	}
}

func (r *EventRunner) readGuarded(ctx context.Context, after []byte) (res eventResult) {
	defer func() {
		if p := recover(); p != nil {
			res = eventResult{err: adapterFault{fmt.Sprintf("adapter panic: %v", p)}}
		}
	}()
	events, next, err := r.dev.ReadEvents(ctx, after)
	return eventResult{events: events, next: next, err: err}
}
