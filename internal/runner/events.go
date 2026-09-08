package runner

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// EventRunner polls an EventReader on its own goroutine and forwards discrete
// events without synth. Checkpointing (skip already-seen log ids) lives in
// the adapter — ADR 0004 deferred that to the first log-shaped reader.
type EventRunner struct {
	dev        adapter.EventReader
	deviceID   string
	deviceKind string
	interval   time.Duration
	timeout    time.Duration
	out        func([]model.Event)

	now    func() time.Time
	jitter func(time.Duration) time.Duration

	consecutiveFailures int
}

// NewEventRunner builds an EventRunner. timeout==0 defaults to interval.
func NewEventRunner(dev adapter.EventReader, deviceID string, interval, timeout time.Duration,
	out func([]model.Event)) *EventRunner {
	if timeout <= 0 {
		timeout = interval
	}
	return &EventRunner{
		dev: dev, deviceID: deviceID,
		deviceKind: dev.Descriptor().DeviceKind,
		interval:   interval, timeout: timeout,
		out: out, now: time.Now,
		jitter: func(d time.Duration) time.Duration { return time.Duration(rand.Int63n(int64(d))) },
	}
}

// SetNow overrides the clock (tests).
func (r *EventRunner) SetNow(now func() time.Time) { r.now = now }

// SetJitter overrides start-jitter (tests).
func (r *EventRunner) SetJitter(j func(time.Duration) time.Duration) { r.jitter = j }

// Run blocks until ctx is cancelled.
func (r *EventRunner) Run(ctx context.Context) {
	select {
	case <-time.After(r.jitter(r.interval)):
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.pollOnce(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (r *EventRunner) pollOnce(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	events, err := r.fetchGuarded(pctx)
	switch {
	case err != nil:
		if ctx.Err() != nil {
			return
		}
		r.consecutiveFailures++
		if r.consecutiveFailures == 1 {
			r.out([]model.Event{model.DeviceStatusChanged{
				Base:      model.Base{DeviceID: r.deviceID, DeviceKind: r.deviceKind, OccurredAt: r.now().UTC()},
				Reachable: false, Reason: err.Error(), ConsecutiveFailures: 1,
			}})
		}
	default:
		if r.consecutiveFailures > 0 {
			r.out([]model.Event{model.DeviceStatusChanged{
				Base:      model.Base{DeviceID: r.deviceID, DeviceKind: r.deviceKind, OccurredAt: r.now().UTC()},
				Reachable: true, ConsecutiveFailures: 0,
			}})
			r.consecutiveFailures = 0
		}
		if len(events) == 0 {
			return
		}
		r.out(stampEvents(events, r.deviceKind))
	}
}

func (r *EventRunner) fetchGuarded(ctx context.Context) (events []model.Event, err error) {
	defer func() {
		if p := recover(); p != nil {
			events, err = nil, fmt.Errorf("adapter panic: %v", p)
		}
	}()
	return r.dev.Fetch(ctx)
}

// stampEvents copies DeviceKind onto every event's Base. Adapters must not set it.
func stampEvents(events []model.Event, deviceKind string) []model.Event {
	out := make([]model.Event, len(events))
	for i, ev := range events {
		out[i] = withDeviceKind(ev, deviceKind)
	}
	return out
}

func withDeviceKind(ev model.Event, deviceKind string) model.Event {
	switch e := ev.(type) {
	case model.PhaseLogEvent:
		e.DeviceKind = deviceKind
		return e
	case model.OverlapLogEvent:
		e.DeviceKind = deviceKind
		return e
	case model.DetectorTransition:
		e.DeviceKind = deviceKind
		return e
	case model.UnmappedControllerLogEvent:
		e.DeviceKind = deviceKind
		return e
	case model.DeviceStatusChanged:
		e.DeviceKind = deviceKind
		return e
	default:
		return ev
	}
}
