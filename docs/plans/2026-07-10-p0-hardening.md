# P0 Hardening Implementation Plan


**Goal:** Make the collector crash-resilient — recover panics in long-lived goroutines, remove three double-close/zero-interval panic vectors — with zero change to happy-path behavior.

**Architecture:** Add a tiny `internal/runtimex` package providing `Recover` (a deferred panic→log+metric guard) and `Go` (a guarded goroutine spawn). Apply it to every long-lived worker (scheduler poll loop, heartbeat loop, ATSPM/TrafficVision runners, inventory watcher, metrics loops). Independently fix the heartbeat `Stop` and ratelimit `Close` double-close races and the scheduler zero-interval `NewTicker` panic. This is P0 of the vendor-adapter design (`docs/specs/2026-07-10-vendor-adapter-architecture-design.md`) and lands before any restructuring.

**Tech Stack:** Go 1.26, `log/slog`, `github.com/prometheus/client_golang` (promauto), stdlib `sync`/`testing`.

## Global Constraints

- Module path: `github.com/Vikasa2M/openits-collector`. Go 1.26.
- This repo has **zero tests today**; `go test ./...` currently passes trivially. This plan adds the first tests. `go test ./...` resolves `openits-models` via the `replace => ../openits-models` in `go.mod` — the sibling checkout must be present (it is, for local dev).
- Prometheus metrics use `promauto`, namespace `openits`, pattern as in `internal/metrics/metrics.go`.
- P0 is **behavior-preserving on the happy path** — no event, subject, or timing changes for valid configs. The only new observable behavior is: a panic is logged + counted instead of crashing, and a zero/negative `poll_interval` is clamped instead of panicking.
- Panic-guard site names are a fixed, low-cardinality label set (we choose them); never derive them from device IDs or errors.
- Commit after every task. Run `gofmt -s -w .` before each commit.

---

### Task 1: `internal/runtimex` panic-guard package + metric + `make test`

**Files:**
- Create: `internal/runtimex/safego.go`
- Create: `internal/runtimex/safego_test.go`
- Modify: `internal/metrics/metrics.go` (append a new metric var block)
- Modify: `Makefile` (add a `test` target)

**Interfaces:**
- Produces:
  - `runtimex.Recover(logger *slog.Logger, site string)` — call as `defer runtimex.Recover(logger, "some.site")` at the top of a goroutine or per-iteration closure.
  - `runtimex.Go(logger *slog.Logger, site string, fn func())` — spawns `fn` in a goroutine guarded by `Recover`.
  - `metrics.PanicsRecoveredTotal *prometheus.CounterVec` with one label `site`.

- [ ] **Step 1: Add the metric.** Append to the end of `internal/metrics/metrics.go` (before the final closing of the file; it uses the same `promauto` import already present):

```go
// Runtime resilience metrics
var (
	// PanicsRecoveredTotal counts panics caught by runtimex.Recover, keyed by
	// a fixed, low-cardinality site label (never a device id or error string).
	PanicsRecoveredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "openits",
		Subsystem: "runtime",
		Name:      "panics_recovered_total",
		Help:      "Total panics recovered in long-lived goroutines, by site",
	}, []string{"site"})
)
```

- [ ] **Step 2: Write the failing test** in `internal/runtimex/safego_test.go`:

```go
package runtimex

import (
	"io"
	"log/slog"
	"sync"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecover_SwallowsPanic(t *testing.T) {
	done := func() (recovered bool) {
		defer func() {
			// If Recover did NOT catch, this deferred func sees the panic.
			if r := recover(); r != nil {
				recovered = false
			}
		}()
		defer Recover(discardLogger(), "test.site")
		panic("boom")
	}()
	if !done {
		t.Fatal("panic escaped runtimex.Recover")
	}
}

func TestGo_RecoversAndCompletes(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	Go(discardLogger(), "test.go", func() {
		defer wg.Done()
		panic("boom in goroutine")
	})
	wg.Wait() // returns only if the goroutine's deferred Done ran despite the panic
}
```

- [ ] **Step 3: Run the test to verify it fails to compile (package missing).**

Run: `go test ./internal/runtimex/...`
Expected: FAIL — `no required module provides package .../internal/runtimex` / undefined `Recover`, `Go`.

- [ ] **Step 4: Write the implementation** in `internal/runtimex/safego.go`:

```go
// Package runtimex provides panic-recovery guards for long-lived goroutines.
//
// A panic in a bare goroutine crashes the whole process. Recover converts it
// into a logged error plus a metric increment so one bad input (a malformed
// controller id reaching envelope construction, a decoder edge case) degrades
// a single worker instead of taking down the collector.
package runtimex

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/Vikasa2M/openits-collector/internal/metrics"
)

// Recover is deferred at the top of a goroutine — or a per-iteration closure —
// to catch a panic, log it with a stack, and count it under the given site.
// site must be a fixed, low-cardinality string.
func Recover(logger *slog.Logger, site string) {
	if r := recover(); r != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("panic recovered",
			"site", site,
			"panic", fmt.Sprint(r),
			"stack", string(debug.Stack()))
		metrics.PanicsRecoveredTotal.WithLabelValues(site).Inc()
	}
}

// Go runs fn in a new goroutine guarded by Recover.
func Go(logger *slog.Logger, site string, fn func()) {
	go func() {
		defer Recover(logger, site)
		fn()
	}()
}
```

- [ ] **Step 5: Add the `test` target** to `Makefile`. Change the `.PHONY` line and add the target after `vet`:

```makefile
.PHONY: build poller vet fmt tidy docker test
test:
	$(GOCMD) test ./...
```

- [ ] **Step 6: Run tests to verify they pass.**

Run: `gofmt -s -w . && make test`
Expected: PASS — `ok  github.com/Vikasa2M/openits-collector/internal/runtimex`, all other packages `ok`/`no test files`.

- [ ] **Step 7: Commit.**

```bash
git add internal/runtimex/ internal/metrics/metrics.go Makefile
git commit -m "feat(runtimex): panic-recovery guard + panics_recovered_total metric + make test"
```

---

### Task 2: Fix ratelimit `Close` double-close panic

**Files:**
- Modify: `internal/ratelimit/limiter.go:19-26` (add a `closeOnce`), `:135-137` (`Close`)
- Test: `internal/ratelimit/limiter_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `(*Limiter).Close()` becomes idempotent (safe to call multiple times).

- [ ] **Step 1: Write the failing test** in `internal/ratelimit/limiter_test.go`:

```go
package ratelimit

import "testing"

func TestClose_Idempotent(t *testing.T) {
	l := NewLimiter(DefaultConfig())
	l.Close()
	l.Close() // must not panic on double close of cleanupCh
}

func TestAllow_UnderLimitPasses(t *testing.T) {
	l := NewLimiter(DefaultConfig())
	defer l.Close()
	if !l.Allow("device-a") {
		t.Fatal("first Allow should pass under default limits")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails.**

Run: `go test ./internal/ratelimit/... -run TestClose_Idempotent -v`
Expected: FAIL — panic `close of closed channel`.

- [ ] **Step 3: Add a `sync.Once` guard.** In `internal/ratelimit/limiter.go`, add the field to the `Limiter` struct (after `cleanupCh chan struct{}`):

```go
	cleanupCh chan struct{}
	closeOnce sync.Once
```

Then replace `Close`:

```go
// Close stops the limiter's cleanup goroutine. Safe to call multiple times.
func (l *Limiter) Close() {
	l.closeOnce.Do(func() {
		close(l.cleanupCh)
	})
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `gofmt -s -w . && go test ./internal/ratelimit/... -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/ratelimit/limiter.go internal/ratelimit/limiter_test.go
git commit -m "fix(ratelimit): make Close idempotent (was panicking on double close)"
```

---

### Task 3: Fix heartbeat `Stop` double-close race

**Files:**
- Modify: `internal/heartbeat/heartbeat.go:176-192` (`Stop`)
- Test: `internal/heartbeat/heartbeat_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `(*Publisher).Stop()` is safe under concurrent calls and repeated calls — the `started`-check and `close(stopCh)` happen in one critical section, capturing `stopCh`/`doneCh` locally (mirrors `poller.Scheduler.Stop`).

- [ ] **Step 1: Write the failing test** in `internal/heartbeat/heartbeat_test.go`. `Start` calls `publishHeartbeat` once immediately, which touches the NATS client (`IsConnected`, `PublishMsg`) and the device manager (`HealthSummary`), so the test provides minimal stubs for both. The constructor signature is `NewPublisher(pollerID string, client inats.NATSClient, devices device.DeviceManager, queue QueueCounter, opts ...Option)` — pass the stubs as the third and (nil) fourth args:

```go
package heartbeat

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/openits-collector/internal/config"
	"github.com/Vikasa2M/openits-collector/internal/device"
	inats "github.com/Vikasa2M/openits-collector/internal/nats"
)

type stubNATS struct{}

func (stubNATS) Connect(context.Context) error                                        { return nil }
func (stubNATS) Publish(context.Context, string, []byte, ...inats.PublishOption) error { return nil }
func (stubNATS) PublishMsg(context.Context, *nats.Msg, ...inats.PublishOption) error   { return nil }
func (stubNATS) Subscribe(context.Context, string, nats.MsgHandler) (*nats.Subscription, error) {
	return nil, nil
}
func (stubNATS) JetStream() jetstream.JetStream { return nil }
func (stubNATS) IsConnected() bool              { return true }
func (stubNATS) Close() error                   { return nil }

// deviceSummaryStub implements device.DeviceManager (7 methods); only
// HealthSummary is exercised by publishHeartbeat.
type deviceSummaryStub struct{}

func (deviceSummaryStub) Add(config.DeviceConfig) error   { return nil }
func (deviceSummaryStub) Remove(string) error             { return nil }
func (deviceSummaryStub) Get(string) (*device.Device, error) { return nil, nil }
func (deviceSummaryStub) Devices() iter.Seq2[string, *device.Device] {
	return func(func(string, *device.Device) bool) {}
}
func (deviceSummaryStub) Count() int                                            { return 0 }
func (deviceSummaryStub) HealthSummary() (total, healthy, degraded, offline int) { return 0, 0, 0, 0 }
func (deviceSummaryStub) Close() error                                          { return nil }

func TestStop_ConcurrentIsSafe(t *testing.T) {
	p := NewPublisher(
		"poller-1",
		stubNATS{},
		deviceSummaryStub{},     // devices
		nil,                     // queue (QueueCounter)
		WithInterval(time.Hour), // ticker won't fire during the test
	)

	p.Start(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Stop() }()
	}
	wg.Wait() // must not panic on double close(p.stopCh)
}
```

- [ ] **Step 2: Run the test with the race detector to verify it fails.**

Run: `go test ./internal/heartbeat/... -run TestStop_ConcurrentIsSafe -race -v`
Expected: FAIL — panic `close of closed channel` (and/or a race report on `p.started`).

- [ ] **Step 3: Fix `Stop`.** Replace `internal/heartbeat/heartbeat.go` `Stop` (lines 176-192) with a single-critical-section version that captures the channels locally:

```go
// Stop stops the heartbeat publisher. Safe to call multiple times and
// concurrently.
func (p *Publisher) Stop() {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	p.started = false
	stopCh := p.stopCh
	doneCh := p.doneCh
	p.mu.Unlock()

	close(stopCh)
	<-doneCh

	p.logger.Info("Stopped heartbeat publisher")
}
```

- [ ] **Step 4: Run the test to verify it passes.**

Run: `gofmt -s -w . && go test ./internal/heartbeat/... -race -v`
Expected: PASS, no race.

- [ ] **Step 5: Commit.**

```bash
git add internal/heartbeat/heartbeat.go internal/heartbeat/heartbeat_test.go
git commit -m "fix(heartbeat): make Stop concurrency-safe (single critical section, no double close)"
```

---

### Task 4: Scheduler — clamp zero interval + recover per poll

**Files:**
- Modify: `internal/poller/scheduler.go` — add `defaultWorkerInterval` const; clamp in `startWorkerLocked` (`:262-272`); wrap `pollDevice` calls in `runWorker` (`:274-296`) with `runtimex.Recover`.
- Test: `internal/poller/scheduler_test.go`

**Interfaces:**
- Consumes: `runtimex.Recover(logger, site)` (Task 1).
- Produces: a worker with `interval <= 0` runs at `defaultWorkerInterval` (10s) instead of panicking in `time.NewTicker`; a panic inside `pollDevice` is recovered and the worker's ticker loop keeps running.

- [ ] **Step 1: Write the failing test** in `internal/poller/scheduler_test.go`:

```go
package poller

import (
	"context"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/config"
	"github.com/Vikasa2M/openits-collector/internal/device"
	"github.com/Vikasa2M/openits-collector/internal/events/synth"
	"github.com/Vikasa2M/openits-collector/sdk/driver"
)

// fakeDriver returns a valid empty Reading and counts Collect calls.
type fakeDriver struct{ collects atomic.Int64 }

func (d *fakeDriver) Collect(context.Context) (*driver.Reading, error) {
	d.collects.Add(1)
	return &driver.Reading{ControllerID: "asc-001", SampledAt: time.Now()}, nil
}
func (d *fakeDriver) Close() error { return nil }

// fakeMgr exposes exactly one device.
type fakeMgr struct{ dev *device.Device }

func (m *fakeMgr) Add(config.DeviceConfig) error   { return nil }
func (m *fakeMgr) Remove(string) error             { return nil }
func (m *fakeMgr) Get(string) (*device.Device, error) { return m.dev, nil }
func (m *fakeMgr) Devices() iter.Seq2[string, *device.Device] {
	return func(yield func(string, *device.Device) bool) { yield(m.dev.Config.ID, m.dev) }
}
func (m *fakeMgr) Count() int                                            { return 1 }
func (m *fakeMgr) HealthSummary() (total, healthy, degraded, offline int) { return 1, 1, 0, 0 }
func (m *fakeMgr) Close() error                                          { return nil }

// panicPub panics on every publish.
type panicPub struct{ calls atomic.Int64 }

func (p *panicPub) PublishSynth(ctx context.Context, deviceKind, controllerID string, evs synth.Events, occurredAt time.Time) error {
	p.calls.Add(1)
	panic("publish boom")
}

func TestWorker_ZeroIntervalDoesNotPanic(t *testing.T) {
	drv := &fakeDriver{}
	dev := &device.Device{
		Config: config.DeviceConfig{ID: "asc-001", PollInterval: 0}, // would panic time.NewTicker(0)
		Driver: drv,
	}
	s := NewScheduler(&fakeMgr{dev: dev}, nil)
	s.Start(context.Background())
	defer s.Stop()
	if err := s.AddDevice("asc-001"); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if !s.Running() {
		t.Fatal("scheduler should be running")
	}
}

func TestWorker_SurvivesPublishPanic(t *testing.T) {
	drv := &fakeDriver{}
	dev := &device.Device{
		Config: config.DeviceConfig{ID: "asc-001", PollInterval: 20 * time.Millisecond},
		Driver: drv,
	}
	pub := &panicPub{}
	s := NewScheduler(&fakeMgr{dev: dev}, pub)
	s.Start(context.Background())
	defer s.Stop()
	time.Sleep(120 * time.Millisecond)
	if got := drv.collects.Load(); got < 2 {
		t.Fatalf("worker did not survive publish panic: Collect called %d times (want >= 2)", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/poller/... -v`
Expected: FAIL — `TestWorker_ZeroIntervalDoesNotPanic` panics `non-positive interval for NewTicker`; `TestWorker_SurvivesPublishPanic` crashes the test binary with the unrecovered `publish boom` panic.

- [ ] **Step 3: Add the const and clamp.** In `internal/poller/scheduler.go`, add near the top (after the `tracer` var, ~line 76):

```go
// defaultWorkerInterval is used when a device's PollInterval is missing or
// non-positive (e.g. an inventory source that didn't apply defaults). It
// keeps time.NewTicker from panicking on a zero interval.
const defaultWorkerInterval = 10 * time.Second
```

Then in `startWorkerLocked`, clamp the interval when building the worker:

```go
func (s *Scheduler) startWorkerLocked(ctx context.Context, deviceID string, dev *device.Device) {
	interval := dev.Config.PollInterval
	if interval <= 0 {
		s.logger.Warn("device has non-positive poll interval; using default",
			"device_id", deviceID, "default", defaultWorkerInterval)
		interval = defaultWorkerInterval
	}
	w := &worker{
		deviceID: deviceID,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	s.workers[deviceID] = w

	go s.runWorker(ctx, w, dev)
}
```

- [ ] **Step 4: Wrap poll calls with Recover.** In `internal/poller/scheduler.go`, add the import `"github.com/Vikasa2M/openits-collector/internal/runtimex"`, then add a guarded wrapper and use it in `runWorker`:

```go
// pollDeviceSafe runs one poll under a panic guard so a single bad poll
// (e.g. a malformed controller id reaching envelope construction) degrades
// that poll rather than killing the worker or the process.
func (s *Scheduler) pollDeviceSafe(ctx context.Context, dev *device.Device) {
	defer runtimex.Recover(s.logger, "scheduler.poll")
	s.pollDevice(ctx, dev)
}
```

Then in `runWorker`, replace the two `s.pollDevice(ctx, dev)` calls (the initial one at ~line 281 and the ticker-driven one at ~line 293) with `s.pollDeviceSafe(ctx, dev)`.

- [ ] **Step 5: Run the tests to verify they pass.**

Run: `gofmt -s -w . && go test ./internal/poller/... -race -v`
Expected: PASS — both tests green, no race.

- [ ] **Step 6: Commit.**

```bash
git add internal/poller/scheduler.go internal/poller/scheduler_test.go
git commit -m "fix(scheduler): clamp non-positive poll interval; recover per-poll panics"
```

---

### Task 5: Apply the panic guard to the remaining long-lived goroutines

**Files:**
- Modify: `internal/heartbeat/heartbeat.go:146` (loop goroutine)
- Modify: `cmd/poller/main.go:285` (inventory watch goroutine), `:432` (device-metrics loop), `:748` (ATSPM runner), `:796` (TrafficVision runner)

**Interfaces:**
- Consumes: `runtimex.Recover(logger, site)` and `runtimex.Go(logger, site, fn)` (Task 1).
- Produces: no new API; every long-lived goroutine started outside the scheduler is now panic-guarded with a fixed site label.

> These are mechanical, behavior-preserving edits. The guard behavior itself is covered by Task 1's `runtimex` unit tests; verification here is `go build` + `go vet` + `make test` (the app's goroutines are not unit-testable without standing up NATS).

- [ ] **Step 1: Guard the heartbeat loop.** In `internal/heartbeat/heartbeat.go`, add import `"github.com/Vikasa2M/openits-collector/internal/runtimex"`, then make the goroutine body at line 146 start with the guard:

```go
	go func() {
		defer runtimex.Recover(p.logger, "heartbeat.loop")
		defer close(p.doneCh)
		// ... existing body unchanged ...
	}()
```

- [ ] **Step 2: Guard the inventory watch goroutine.** In `cmd/poller/main.go`, add import `"github.com/Vikasa2M/openits-collector/internal/runtimex"`. At line 285 the `go func()` iterating `invChanges` becomes:

```go
		go func() {
			defer runtimex.Recover(logger, "inventory.watch")
			for ev := range invChanges {
				// ... existing body unchanged ...
			}
		}()
```

- [ ] **Step 3: Guard the device-metrics loop.** In `cmd/poller/main.go` at line 432, the metrics-update `go func()` becomes:

```go
	go func() {
		defer runtimex.Recover(logger, "metrics.device-loop")
		ticker := time.NewTicker(10 * time.Second)
		// ... existing body unchanged ...
	}()
```

- [ ] **Step 4: Guard the ingest runners.** In `cmd/poller/main.go`, replace `go runner.Run(ctx)` at line 748 (inside `startHRIngest`) with:

```go
			runtimex.Go(logger, "atspm.runner", func() { runner.Run(ctx) })
```

and at line 796 (inside `startTrafficVision`) replace `go runner.Run(ctx)` with:

```go
	runtimex.Go(logger, "trafficvision.runner", func() { runner.Run(ctx) })
```

> Note: `startHRIngest`/`startTrafficVision` receive `logger` as a parameter — use that in-scope `logger`.

- [ ] **Step 5: Verify build, vet, and the full test suite.**

Run: `gofmt -s -w . && go build ./... && go vet ./... && make test`
Expected: build clean, vet clean, all tests PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/heartbeat/heartbeat.go cmd/poller/main.go
git commit -m "chore: panic-guard heartbeat, inventory-watch, metrics-loop, and ingest runners"
```

---

## Self-Review

**Spec coverage (P0 items from the design §8):**
- `safeGo` panic recovery on every long-lived goroutine → Task 1 (helper), Task 4 (scheduler), Task 5 (heartbeat/inventory/metrics/runners). ✅
- Guard heartbeat `Stop` double-close → Task 3. ✅
- Guard ratelimit `Close` double-close → Task 2. ✅
- Validate inventory `poll_interval` before `NewTicker` → Task 4 (clamp in `startWorkerLocked`). ✅
- Explicitly out of P0 (belongs at the P1 boundary): the `DetectorReport` channel-sort ce-id determinism fix. Not in this plan by design.

**Placeholder scan:** Task 3's test intentionally shows a corrected-in-place stub — the final `deviceSummaryStub` (7 methods) is fully specified; the earlier `stubDevices` sketch is explicitly instructed to be deleted. No "TODO"/"implement later" remain.

**Type consistency:** `runtimex.Recover(*slog.Logger, string)` and `runtimex.Go(*slog.Logger, string, func())` are used with those exact signatures in Tasks 4 and 5. `metrics.PanicsRecoveredTotal` is a `*CounterVec` with label `site`, used only via `.WithLabelValues(site).Inc()` inside `Recover`. `PublishSynth` fake signature matches `poller.EventPublisher` (`ctx, deviceKind, controllerID string, synth.Events, time.Time) error`). `device.DeviceManager` fakes implement all 7 interface methods.
