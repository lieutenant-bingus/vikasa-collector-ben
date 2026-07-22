# C5 — Command Dispatch Worker Pool Implementation Plan


**Goal:** Fix C5 — every command runs synchronously on the single NATS delivery callback (synchronous SNMP round-trips + up to ~2.6 s of inline ack-retry sleeps), so one slow/unreachable device stalls commands for every device. Move execution onto a per-device-routed worker pool so the callback only enqueues.

**Architecture:** The NATS subscription callback becomes `dispatch`, which peeks the command's `DeviceId`, hashes it to one of N worker goroutines, and hands off the raw message. Each worker runs `handleMessage` serially over its queue. Routing by `DeviceId` means all commands for one device land on the same worker (serial — preserving the GET-then-SET read-modify-write ordering that C3's socket mutex does not by itself guarantee across two ops), while commands for different devices run on different workers (parallel — no head-of-line blocking). Workers are panic-guarded with `runtimex.Recover`. Commands are rate-limited (global 100/s, per-device 10/s), so the per-worker buffers effectively never fill under real load.

**Tech Stack:** Go 1.26, `github.com/nats-io/nats.go`, `google.golang.org/protobuf/proto`, stdlib `hash/fnv`/`sync`/`testing`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- **Per-device serialization is a correctness requirement, not just perf:** commands for one device MUST NOT execute concurrently (the signal-control handlers do GET-then-SET read-modify-writes on shared registers). Route by `DeviceId` so same-device commands share one worker.
- The callback (`dispatch`) MUST NOT block on execution — that is the whole point. It only unmarshals-to-peek-DeviceId and enqueues.
- Behavior preserved: `handleMessage`'s logic (validation, C2 device-type check, rate limit, execute, ack) is unchanged — it just runs on a worker instead of the callback. Acks and metrics are unchanged.
- `Handler` fields are safe under parallel `handleMessage` (pollerID/region/… read-only; `registry` RWMutex; `rateLimiter` mutexed; `devices` manager concurrency-safe; per-message state is local). The NATS connection is safe for concurrent publish (acks).
- Commit style: Conventional Commits, no co-author attribution. Commit after the task; `gofmt -s -w .` before committing.

---

### Task 1: Per-device worker pool + routing + tests

**Files:**
- Modify: `internal/command/handler.go` (`Handler` struct; `NewHandler`/options default; `Start`; `Stop`; add `dispatch`, `runWorker`, `workerIndex`)
- Test: extend `internal/command/handler_test.go` (created in P4a)

**Interfaces:**
- Internal: `Handler` gains `ctx`, `stopCh chan struct{}`, `workers []chan *nats.Msg`, `workerWG sync.WaitGroup`, `numWorkers int`. Optional `WithWorkers(n int) Option`.

- [ ] **Step 1: Add fields + a default worker count.** In `internal/command/handler.go`, add to the `Handler` struct (near `subscription`/`started`):

```go
	ctx        context.Context
	stopCh     chan struct{}
	workers    []chan *nats.Msg
	workerWG   sync.WaitGroup
	numWorkers int
```

In the `options` struct add `numWorkers int`, default it in `NewHandler`'s `options{...}` literal to `defaultCommandWorkers`, and copy it to the `Handler` in the return. Add the const and an option near the other options:

```go
// defaultCommandWorkers is the per-device-routed worker pool size. Commands are
// rate-limited and low-volume, so a small pool amply decouples slow devices.
const defaultCommandWorkers = 8

// WithWorkers overrides the command worker-pool size (min 1).
func WithWorkers(n int) Option {
	return optionFunc(func(o *options) {
		if n < 1 {
			n = 1
		}
		o.numWorkers = n
	})
}
```
> Verify how `Option` is defined in this package (e.g. an interface with `apply(*options)` or a func type). If it's an interface, add `WithWorkers` in the same style as the existing options (e.g. `WithRegistry`) rather than the `optionFunc` shown here, and set the default in the `options{...}` literal. Use whatever the existing options use.

- [ ] **Step 2: Rework `Start` to spawn workers and dispatch to them.** Replace the subscribe call so the callback is `dispatch`, and start the pool. `Start` currently locks, checks `started`, subscribes with `func(msg){ h.handleMessage(ctx, msg) }`. Change to:

```go
func (h *Handler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return nil
	}

	h.ctx = ctx
	h.stopCh = make(chan struct{})
	h.workers = make([]chan *nats.Msg, h.numWorkers)
	for i := range h.workers {
		h.workers[i] = make(chan *nats.Msg, 256)
		h.workerWG.Add(1)
		go h.runWorker(h.workers[i])
	}

	subject := h.cmdSubject()
	sub, err := h.client.Subscribe(ctx, subject, func(msg *nats.Msg) {
		h.dispatch(msg)
	})
	if err != nil {
		// Unwind the workers we just started so Start is all-or-nothing.
		close(h.stopCh)
		h.workerWG.Wait()
		h.workers = nil
		return fmt.Errorf("subscribe to %s: %w", subject, err)
	}

	h.subscription = sub
	h.started = true
	h.logger.Info("Started command handler",
		"subject", subject, "poller_id", h.pollerID,
		"workers", h.numWorkers, "registered_services", h.registry.Services())
	return nil
}
```

- [ ] **Step 3: Add `dispatch`, `runWorker`, `workerIndex`.** Add (import `hash/fnv` and `github.com/Vikasa2M/openits-collector/internal/runtimex`):

```go
// dispatch is the NATS delivery callback. It peeks the command's DeviceId to
// route to a per-device worker (so same-device commands serialize and
// different devices run in parallel), then hands off the raw message. It never
// blocks on execution — that runs on the worker.
func (h *Handler) dispatch(msg *nats.Msg) {
	var peek pb.DeviceCommand
	_ = proto.Unmarshal(msg.Data, &peek) // a decode error yields DeviceId="";
	// handleMessage re-unmarshals and drops malformed payloads.
	idx := workerIndex(peek.DeviceId, len(h.workers))
	select {
	case h.workers[idx] <- msg:
	case <-h.stopCh:
	}
}

// runWorker processes one worker's queue serially until Stop.
func (h *Handler) runWorker(ch chan *nats.Msg) {
	defer h.workerWG.Done()
	defer runtimex.Recover(h.logger, "command.worker")
	for {
		select {
		case <-h.stopCh:
			return
		case msg := <-ch:
			h.handleMessage(h.ctx, msg)
		}
	}
}

// workerIndex maps a device id to a worker slot via FNV-1a, so all commands for
// one device route to the same worker (serial execution per device).
func workerIndex(deviceID string, n int) int {
	if n <= 1 {
		return 0
	}
	hsh := fnv.New32a()
	_, _ = hsh.Write([]byte(deviceID))
	return int(hsh.Sum32() % uint32(n))
}
```

- [ ] **Step 4: Rework `Stop` to drain the pool.** `Stop` currently unsubscribes under the lock. Change it to also stop the workers (unsubscribe first so no new messages are dispatched, then signal + wait):

```go
func (h *Handler) Stop() error {
	h.mu.Lock()
	if !h.started {
		h.mu.Unlock()
		return nil
	}
	sub := h.subscription
	stopCh := h.stopCh
	h.subscription = nil
	h.started = false
	h.mu.Unlock()

	if sub != nil {
		if err := sub.Unsubscribe(); err != nil {
			h.logger.Error("Failed to unsubscribe", "error", err)
		}
	}
	close(stopCh)      // signal workers (and any dispatch blocked on a full queue)
	h.workerWG.Wait()  // wait for in-flight handleMessage to finish
	h.logger.Info("Stopped command handler")
	return nil
}
```
> The `handleMessage` signature and body are UNCHANGED — it still does unmarshal, validation, the C2 device-type check, rate limit, execute, and ack. Only who calls it (a worker, not the callback) changed.

- [ ] **Step 5: Extend `internal/command/handler_test.go`** with two tests: same-device commands serialize, and the dispatch callback does not block on a slow Execute. Reuse the P4a fakes (`stubNATS`, `oneDeviceManager`, `recordingHandler`), adding a blocking variant.

```go
// A ServiceHandler whose Execute blocks until released; records max concurrency.
type blockingHandler struct {
	service    string
	deviceType pb.DeviceType
	release    chan struct{}
	active     atomic.Int32
	maxActive  atomic.Int32
	started    chan struct{} // signalled once per Execute entry
}

func (h *blockingHandler) Service() string           { return h.service }
func (h *blockingHandler) DeviceType() pb.DeviceType  { return h.deviceType }
func (h *blockingHandler) Execute(context.Context, *pb.DeviceCommand, *device.Device) (pb.CommandResult, string, error) {
	n := h.active.Add(1)
	for {
		m := h.maxActive.Load()
		if n <= m || h.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.release
	h.active.Add(-1)
	return pb.CommandResult_COMMAND_RESULT_SUCCESS, "", nil
}

// Same-device commands must never execute concurrently (read-modify-write safety).
func TestDispatch_SameDeviceSerializes(t *testing.T) {
	bh := &blockingHandler{service: "signal-control", deviceType: pb.DeviceType_DEVICE_TYPE_ASC, release: make(chan struct{}), started: make(chan struct{}, 4)}
	reg := NewRegistry()
	reg.Register(bh)
	dev := &device.Device{Config: config.DeviceConfig{ID: "asc-001", DeviceKind: "asc"}}
	h := NewHandler("poller-1", stubNATS{}, oneDeviceManager{dev: dev}, WithRegistry(reg))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Stop()

	// Fire 3 commands for the SAME device id.
	for i := 0; i < 3; i++ {
		h.dispatch(cmdMsg(t, pb.DeviceType_DEVICE_TYPE_ASC))
	}
	// Let the first one enter Execute, give any (incorrect) parallelism a moment.
	<-bh.started
	time.Sleep(20 * time.Millisecond)
	close(bh.release) // release all
	// Drain: allow the rest to run.
	time.Sleep(50 * time.Millisecond)

	if got := bh.maxActive.Load(); got > 1 {
		t.Fatalf("same-device commands ran concurrently (max %d): read-modify-write unsafe", got)
	}
}

// The dispatch callback must return promptly even while a command's Execute is
// blocked — proving execution is decoupled from the delivery callback.
func TestDispatch_DoesNotBlockOnExecute(t *testing.T) {
	bh := &blockingHandler{service: "signal-control", deviceType: pb.DeviceType_DEVICE_TYPE_ASC, release: make(chan struct{}), started: make(chan struct{}, 4)}
	reg := NewRegistry()
	reg.Register(bh)
	dev := &device.Device{Config: config.DeviceConfig{ID: "asc-001", DeviceKind: "asc"}}
	h := NewHandler("poller-1", stubNATS{}, oneDeviceManager{dev: dev}, WithRegistry(reg))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { close(bh.release); h.Stop() }()

	done := make(chan struct{})
	go func() { h.dispatch(cmdMsg(t, pb.DeviceType_DEVICE_TYPE_ASC)); close(done) }()
	select {
	case <-done: // dispatch returned without waiting for Execute
	case <-time.After(time.Second):
		t.Fatal("dispatch blocked on Execute (head-of-line blocking not fixed)")
	}
	<-bh.started // confirm the command really did reach a worker's Execute
}
```
> Verify `sync`, `sync/atomic`, and `time` are imported in the test file (P4a imported some). The P4a `recordingHandler` and helpers stay. If the `Option` type isn't `WithRegistry`-style, adjust `NewHandler` calls accordingly.

- [ ] **Step 6: Run tests (race) + full suite.**

Run: `gofmt -s -w . && go test ./internal/command/... -race -v && go test ./...`
Expected: the P4a device-type tests + the two new pool tests PASS with no race; full suite green.

- [ ] **Step 7: Commit.**

```bash
git add internal/command/handler.go internal/command/handler_test.go
git commit -m "fix(command): dispatch execution on a per-device worker pool

Commands ran synchronously on the single NATS callback (blocking SNMP +
inline ack retries), so one slow device stalled commands for all devices.
Route by DeviceId to a worker pool: same-device commands stay serial
(read-modify-write safety), different devices run in parallel, and the
callback only enqueues. Workers are panic-guarded."
```

---

## Self-Review

**Spec coverage:** C5 — head-of-line blocking on the command callback. Execution moves to an N-worker pool routed by `DeviceId` (FNV-1a): same-device serial (preserves the signal-control read-modify-write ordering), cross-device parallel, callback non-blocking. `handleMessage` (incl. the P4a C2 check) is unchanged. Workers are `runtimex.Recover`-guarded (also advances the guard-completeness follow-up for one goroutine class). Tests prove same-device serialization and callback decoupling under `-race`. Deferred (separate items): the in-flight/abandoned-op SNMP concerns (C3 follow-ups), worker-queue overflow policy (buffers are generous and commands are rate-limited, so overflow is not reachable under real load; a full queue applies backpressure via the `stopCh`-guarded blocking send).

**Placeholder scan:** None. The two "verify the `Option` style / imports" notes are concrete reconciliation instructions, not placeholders.

**Type consistency:** `workers []chan *nats.Msg`; `dispatch(msg *nats.Msg)` is the subscribe callback; `runWorker(ch chan *nats.Msg)`; `workerIndex(deviceID string, n int) int`. `handleMessage(h.ctx, msg)` matches its existing `(context.Context, *nats.Msg)` signature. `WithWorkers` follows the package's existing `Option` mechanism (verify at implementation). Test fakes reuse P4a's `stubNATS`/`oneDeviceManager`/`recordingHandler` + `cmdMsg` helper.
