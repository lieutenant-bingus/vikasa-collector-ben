// Package runner drives one device: jittered poll loop, per-poll timeout,
// panic isolation, reachability health transitions. One sick device can
// never stall the cabinet.
//
// See docs/explanation/architecture.md's "The poll loop: jitter, per-poll
// timeout, and panic isolation" section for the mechanics and why each one
// exists.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
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

	// pending receives the result of a Read that outlived its poll. While it
	// is non-nil that Read is still running, and no new one is started:
	// adapters may assume they are never read concurrently.
	pending chan readResult

	counters *stats.Counters
	devices  *stats.Devices
	log      *slog.Logger
}

// adapterFault is a failed poll caused by the adapter's code, not the
// device: a panic, or a nil snapshot with no error. The device was never
// contacted (ADR 0019 clause 12).
type adapterFault struct{ reason string }

func (f adapterFault) Error() string { return f.reason }

type readResult struct {
	snap *model.Snapshot
	err  error
}

// Config is what a Runner needs beyond the adapter itself.
type Config struct {
	DeviceID string
	// Interval between polls. Non-positive panics, as time.NewTicker does:
	// config.Load rejects one at boot, so it is a programming error here.
	Interval time.Duration
	// Timeout bounds one poll; zero means Interval.
	Timeout time.Duration
	Engine  *synth.Engine
	// Out receives the events each poll produces. It must not block.
	Out func([]model.Event)
	// Counters receives the runner's counts (adapter panics); nil keeps
	// them private.
	Counters *stats.Counters
	// Devices receives every poll outcome, for /status and readiness; nil
	// keeps them private.
	Devices *stats.Devices
	// Log receives adapter faults; nil means slog.Default().
	Log *slog.Logger
}

// New builds a Runner for one device.
func New(dev adapter.StateReader, c Config) *Runner {
	if c.Interval <= 0 {
		panic(fmt.Sprintf("runner.New: non-positive poll interval %v for device %s", c.Interval, c.DeviceID))
	}
	if c.Timeout <= 0 {
		c.Timeout = c.Interval
	}
	if c.Counters == nil {
		c.Counters = new(stats.Counters)
	}
	if c.Devices == nil {
		c.Devices = stats.NewDevices([]string{c.DeviceID})
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	return &Runner{
		dev: dev, deviceID: c.DeviceID,
		// From the adapter's own Descriptor: an adapter cannot forget to report
		// its kind, or report one that disagrees with its registration.
		deviceKind: dev.Descriptor().DeviceKind,
		interval:   c.Interval, timeout: c.Timeout,
		engine: c.Engine, out: c.Out, counters: c.Counters, devices: c.Devices, log: c.Log,
		now:    time.Now,
		jitter: rand.N[time.Duration],
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

	snap, err := r.read(pctx)
	var fault adapterFault
	switch {
	case errors.As(err, &fault):
		// A collector fault, not a device fault: counted and logged, and the
		// device's reachability is left exactly as it was. Reporting it
		// unreachable would publish a false catalog fact — a bad adapter
		// release would look like a wave of dead devices.
		r.counters.AdapterPanics.Add(1)
		r.log.Warn("adapter fault; the device was not contacted, so its reachability is unchanged",
			"device", r.deviceID, "adapter", r.dev.Descriptor().Key(), "fault", fault.reason)
		return
	case err != nil:
		// A poll cut short by our own shutdown is not the device's fault.
		// Reporting it as unreachable would fabricate a device-down event on
		// every clean restart. Absence of evidence is not a state change —
		// the same rule synth applies to failed facets.
		if ctx.Err() != nil {
			return
		}
		failedAt := r.now().UTC()
		r.devices.PollFailed(r.deviceID, failedAt, err)
		r.consecutiveFailures++
		if r.consecutiveFailures == 1 {
			last := r.devices.LastSuccess(r.deviceID)
			ev := model.DeviceStatusChanged{
				Base:      model.Base{DeviceID: r.deviceID, DeviceKind: r.deviceKind, OccurredAt: failedAt},
				Reachable: false, Reason: err.Error(), ConsecutiveFailures: 1,
				LastContact: last,
			}
			// comm-lost requires last-contact, the last valid response. A
			// device this process has never heard from has none, so the
			// catalog event would be invalid. Leave LastContact zero: the
			// openits emitter declines it and the health emitter publishes
			// openits-collector.health.device-status-changed.
			if last.IsZero() {
				r.counters.CommLostWithoutContact.Add(1)
				r.log.Warn("no last-contact; publishing collector health device-status-changed instead of comm-lost",
					"device", r.deviceID, "device_kind", r.deviceKind)
			}
			r.out([]model.Event{ev})
		}
	default:
		r.devices.PollOK(r.deviceID, r.now().UTC())
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

// read bounds an adapter Read by pctx even when the adapter ignores it. A
// Read still running when pctx ends is abandoned rather than waited on —
// so a hung adapter is a failed poll, and shutdown never waits on one —
// and the next poll fails fast until it finally returns. Its eventual
// result is discarded: by then it is stale, and absence of evidence is
// not a state change.
func (r *Runner) read(pctx context.Context) (*model.Snapshot, error) {
	if r.pending != nil {
		select {
		case <-r.pending:
			r.pending = nil
		default:
			return nil, errors.New("previous read is still running")
		}
	}
	ch := make(chan readResult, 1) // buffered: an abandoned Read never blocks on send
	go func() {
		snap, err := r.readGuarded(pctx)
		ch <- readResult{snap, err}
	}()
	select {
	case res := <-ch:
		return res.snap, res.err
	case <-pctx.Done():
		r.pending = ch
		return nil, fmt.Errorf("read did not return within %v: %w", r.timeout, pctx.Err())
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
			snap, err = nil, adapterFault{fmt.Sprintf("adapter panic: %v", p)}
		}
	}()
	snap, err = r.dev.Read(ctx)
	if snap == nil && err == nil {
		err = adapterFault{"adapter returned a nil snapshot and no error"}
	}
	return snap, err
}
