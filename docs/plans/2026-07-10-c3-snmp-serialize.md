# C3 — Serialize SNMP Socket I/O Implementation Plan


**Goal:** Fix C3 — one `internal/snmp.Client` (shared by the poll path via `Get` and the command path via `Set`) lets up to `maxInFlight` (5) operations run concurrently on a single `*gosnmp.GoSNMP` connection, which is not safe for concurrent use (one UDP socket + shared request-id state → data race / response mismatch). Serialize the actual socket I/O per client.

**Architecture:** Add a per-`Client` `ioMu sync.Mutex` held around the actual gosnmp socket call inside both goroutine bodies that touch `conn`: the one in `runWithContext` (covers `Get`/`GetBulk`/`Set`/`getV1`) and the inline one in `Walk`. This serializes all socket operations on a client so a poll `Get` and a command `Set` can never touch the same connection simultaneously. Devices remain fully parallel — each device has its own `Client`, connection, and `ioMu`. The context-cancellation, in-flight-limit, and circuit-breaker layers are unchanged.

**Tech Stack:** Go 1.26, `github.com/gosnmp/gosnmp`, stdlib `sync`/`testing`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- Behavior-preserving for a single caller: serialization only changes what happens under CONCURRENT access to one client (which was racy before). Per-device throughput is unchanged (one socket physically serves one op at a time regardless). Cross-device parallelism is unchanged (separate clients/mutexes).
- The `ioMu` must be held ONLY around the socket call, never together with `c.mu` (which guards the `conn` pointer and is released before the I/O), so no lock-ordering deadlock is possible.
- Use `defer` for the unlock so a panic in gosnmp can't leave the mutex held.
- Commit style: Conventional Commits, no co-author attribution. Commit after the task; `gofmt -s -w .` before committing.

---

### Task 1: Add `ioMu` and serialize the socket I/O + concurrency test

**Files:**
- Modify: `internal/snmp/client.go` (`Client` struct; the goroutine in `runWithContext`; the goroutine in `Walk`)
- Test: `internal/snmp/client_test.go` (create)

**Interfaces:** No public API change. Internal: `Client` gains an `ioMu sync.Mutex` field.

- [ ] **Step 1: Add the `ioMu` field.** In `internal/snmp/client.go`, add a mutex to the `Client` struct near the existing `sync` fields (e.g. right after the `mu sync.Mutex`/connection-guarding fields, before or after `skipMu`). Add:

```go
	// ioMu serializes the actual gosnmp socket operations. gosnmp is not safe
	// for concurrent use on one connection (single UDP socket + shared
	// request-id state), and one Client is shared by the poll path (Get) and
	// the command path (Set), so every socket call is guarded by this mutex.
	ioMu sync.Mutex
```

- [ ] **Step 2: Serialize the `runWithContext` I/O goroutine.** Replace the goroutine body in `runWithContext` (currently `go func() { defer c.inFlight.Add(-1); packet, err := fn(); resultCh <- snmpResult{packet: packet, err: err} }()`) with:

```go
	go func() {
		defer c.inFlight.Add(-1)
		c.ioMu.Lock()
		defer c.ioMu.Unlock()
		packet, err := fn()
		resultCh <- snmpResult{packet: packet, err: err}
	}()
```

- [ ] **Step 3: Serialize the `Walk` I/O goroutine.** Replace `Walk`'s inline goroutine (currently `go func() { defer c.inFlight.Add(-1); errCh <- conn.Walk(oid, handler) }()`) with:

```go
	go func() {
		defer c.inFlight.Add(-1)
		c.ioMu.Lock()
		defer c.ioMu.Unlock()
		errCh <- conn.Walk(oid, handler)
	}()
```

- [ ] **Step 4: Write the serialization test.** Create `internal/snmp/client_test.go`. It drives `runWithContext` directly (no real socket needed — it just runs the supplied `fn`) with an `fn` that detects overlapping execution, and asserts the max observed concurrency is 1:

```go
package snmp

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// runWithContext runs the supplied fn in a goroutine; if the socket I/O is
// serialized by ioMu, no two fn invocations on one client ever overlap.
func TestRunWithContext_SerializesSocketIO(t *testing.T) {
	c := NewClient(ClientConfig{DeviceID: "dev-001"}) // maxInFlight defaults to 5

	var active, maxActive atomic.Int32
	fn := func() (*gosnmp.SnmpPacket, error) {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond) // hold the "socket" so overlap is observable
		active.Add(-1)
		return &gosnmp.SnmpPacket{}, nil
	}

	// 4 concurrent callers, below the maxInFlight=5 cap so all four actually run
	// their fn (none rejected by the in-flight limiter).
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.runWithContext(context.Background(), "test", fn)
		}()
	}
	wg.Wait()

	if got := maxActive.Load(); got > 1 {
		t.Fatalf("socket I/O not serialized: %d fn invocations ran concurrently (want max 1)", got)
	}
}
```

- [ ] **Step 5: Run the test to confirm it fails without the fix, passes with it.**

Run: `go test ./internal/snmp/... -run TestRunWithContext_SerializesSocketIO -race -v`
Expected: with Steps 2-3 applied, PASS (max concurrency 1). To see RED, temporarily revert Step 2 (remove the `ioMu` lock in `runWithContext`) and re-run — it reports `maxActive` of 2-4; then re-apply Step 2. (If writing strictly test-first, run before Steps 2-3 to observe the failure.)

- [ ] **Step 6: Run the full package tests + suite.**

Run: `gofmt -s -w . && go test ./internal/snmp/... -race -v && go test ./...`
Expected: green, no race.

- [ ] **Step 7: Commit.**

```bash
git add internal/snmp/client.go internal/snmp/client_test.go
git commit -m "fix(snmp): serialize socket I/O to prevent concurrent gosnmp connection use

One Client (shared by the poll Get path and the command Set path) let up to
maxInFlight operations run concurrently on a single gosnmp connection, which
is not concurrency-safe (one UDP socket + shared request-id state). Guard the
actual socket call in runWithContext and Walk with a per-client ioMu so
operations on one connection serialize; devices stay parallel (separate
clients)."
```

---

## Self-Review

**Spec coverage:** C3 from the audit — concurrent use of one gosnmp connection. `ioMu` guards the socket call in both goroutine bodies that touch `conn` (`runWithContext` covering Get/GetBulk/Set/getV1; `Walk`), serializing per-client I/O. The test drives `runWithContext` concurrently and asserts non-overlapping execution, which fails on the pre-fix code and passes after. Deferred (separate audit items, not C3): the in-flight TOCTOU (`Load` then `Add`), the abandoned-op-holds-slot behavior, and refactoring `Walk` to reuse `runWithContext`.

**Placeholder scan:** None. RED is demonstrated by a described temporary revert, not a placeholder.

**Type consistency:** `ioMu sync.Mutex` (value field, never copied — `Client` is always used as `*Client`). `runWithContext(ctx, op string, fn func() (*gosnmp.SnmpPacket, error))` unchanged signature; the test calls it with a `func() (*gosnmp.SnmpPacket, error)` matching. `NewClient(ClientConfig{...})` matches the constructor. No lock-ordering risk: `ioMu` is never held with `c.mu` (c.mu is released in Get/Set/etc. before `runWithContext` runs).
