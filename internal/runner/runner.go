// Package runner drives one device: jittered poll loop, per-poll timeout,
// panic isolation, reachability health transitions. One sick device can
// never stall the cabinet.
//
// Decided in the 2026-07-12 greenfield collector architecture spec, §7. That
// spec is scheduled for deletion once its content is harvested into the
// explanation tier; until then git history is the only copy, which is why the
// spec is named in full here rather than cited as a bare "spec §7". See
// docs/README.md's known-gaps list.
package runner

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Runner polls one device on its own goroutine.
type Runner struct {
	dev        adapter.StateReader
	deviceID   string
	deviceKind string
	interval   time.Duration
	timeout    time.Duration
	engine     *synth.Engine
	out        func([]model.Event)

	now    func() time.Time
	jitter func(time.Duration) time.Duration

	consecutiveFailures int
}

// New builds a Runner. timeout==0 defaults to interval.
func New(dev adapter.StateReader, deviceID string, interval, timeout time.Duration,
	engine *synth.Engine, out func([]model.Event)) *Runner {
	if timeout <= 0 {
		timeout = interval
	}
	return &Runner{
		dev: dev, deviceID: deviceID,
		// From the adapter's own Descriptor: an adapter cannot forget to report
		// its kind, or report one that disagrees with its registration.
		deviceKind: dev.Descriptor().DeviceKind,
		interval:   interval, timeout: timeout,
		engine: engine, out: out,
		now:    time.Now,
		jitter: func(d time.Duration) time.Duration { return time.Duration(rand.Int63n(int64(d))) },
	}
}

// SetNow overrides the clock (tests).
func (r *Runner) SetNow(now func() time.Time) { r.now = now }

// SetJitter overrides start-jitter (tests).
func (r *Runner) SetJitter(j func(time.Duration) time.Duration) { r.jitter = j }

// Run blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
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

func (r *Runner) pollOnce(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	snap, err := r.readGuarded(pctx)
	switch {
	case err != nil:
		// A poll cut short by our own shutdown is not the device's fault.
		// Reporting it as unreachable would fabricate a device-down event on
		// every clean restart. Absence of evidence is not a state change —
		// the same rule synth applies to failed facets.
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
		snap.DeviceKind = r.deviceKind
		if evs := r.engine.Apply(snap); len(evs) > 0 {
			r.out(evs)
		}
	}
}

// readGuarded turns an adapter panic into a failed poll: adapters are
// third-party code and must never take the loop down. It also turns a
// (nil, nil) return — a broken adapter contract, since a poll with no error
// must produce a snapshot — into a failed poll instead of a nil-deref panic
// in pollOnce/Engine.Apply once DeviceKind is stamped on it.
func (r *Runner) readGuarded(ctx context.Context) (snap *model.Snapshot, err error) {
	defer func() {
		if p := recover(); p != nil {
			snap, err = nil, fmt.Errorf("adapter panic: %v", p)
		}
	}()
	snap, err = r.dev.Read(ctx)
	if snap == nil && err == nil {
		err = fmt.Errorf("adapter returned a nil snapshot and no error")
	}
	return snap, err
}
