# DMS Domain Model + Differ Implementation Plan


**Goal:** Add DMS as the collector's second device kind — the `DMSStatus` facet, its differ, and its domain events — plus `Base.DeviceKind`, the field a second kind makes necessary.

**Architecture:** Facets carry what the device said; differs carry the arithmetic. `FaultSet` is reused rather than duplicated, matching the catalog's one-generic-fault-proto-per-eight-services shape — which is exactly why events must now carry their device kind. There is **no adapter** in this plan: NTCIP 1203 OIDs exist in no repo we control, and the domain and differ need none.

**Tech Stack:** Go 1.26. No new dependencies.

**Spec:** `docs/specs/2026-07-16-dms-domain-design.md`

## Global Constraints

- Branch from `main`. Conventional commits, **no Co-Authored-By trailers**.
- **NEVER bypass commit signing.** This repo signs via 1Password (`commit.gpgsign=true`). If `git commit` fails with a signing error, do NOT retry with `--no-gpg-sign` / `-c commit.gpgsign=false` and do NOT change git config — stop and report BLOCKED. Verify after committing: `git cat-file commit HEAD | grep -c '^gpgsig'` must print 1. (`git log --show-signature` errors locally due to a missing allowedSignersFile — expected, not a failure.)
- Module path `github.com/Vikasa2M/vikasa-collector`. Go 1.26.
- **The iron rule:** absence of evidence is never a state change. A facet the adapter failed to read emits nothing. This holds for free — `synth.Engine.Apply` iterates `snap.Facets`, and failures live in `snap.Errors` — so no differ may special-case it.
- **Enum zero values must mean "unknown", never a real state.** A device that does not answer an object must not read as a definite value.
- `sdk/model` must never import wire schemas (openits-models). CI-enforced by `scripts/lint-boundary.sh`, which runs in `make check` with its own selftest.
- All times UTC; tests use fixed timestamps.
- **Every new guard must be shown to fail.** For each invariant, write a test proving it rejects, then verify by deliberately breaking the thing it guards and watching the test fail. Three times this session a check that passed turned out to be incapable of failing.
- Run `make check` before every commit claim.

## File Structure

**New:**
- `sdk/model/dms.go` + `dms_test.go` — DMS enums and the `DMSStatus` facet (they change together)
- `internal/synth/dms.go` + `dms_test.go` — the DMS differ

**Modified:**
- `sdk/model/events.go` — `Base.DeviceKind`, `Event.EventDeviceKind()`, the three DMS events
- `sdk/model/model.go` — `Snapshot.DeviceKind`
- `sdk/model/enums.go` — three `FaultCategory` additions
- `internal/synth/synth.go` — `Engine.Apply` copies `DeviceKind` into each event's `Base`
- `internal/runner/runner.go` — stamps `snap.DeviceKind` and health events from the adapter's `Descriptor`
- `internal/app/app.go` — register the DMS differ
- test files alongside each

---

### Task 1: `Base.DeviceKind` end-to-end

**Files:**
- Modify: `sdk/model/events.go`, `sdk/model/model.go`, `internal/synth/synth.go`, `internal/runner/runner.go`
- Test: `sdk/model/events_test.go`, `internal/synth/signal_test.go`, `internal/runner/runner_test.go`

**Interfaces:**
- Produces:
  - `Base` gains `DeviceKind string`; `Base.EventDeviceKind() string`
  - `Event` interface gains `EventDeviceKind() string`
  - `Snapshot` gains `DeviceKind string`
  - `runner.Runner` stamps both, from `adapter.StateReader.Descriptor().DeviceKind`

**Why this task exists:** the catalog defines `fault-raised`/`fault-cleared` **once** (`openits.common.v1`) and reuses them across all eight services; `openits.dms.fault-raised.v1` and `openits.signal-control.fault-raised.v1` are the same proto published under different ce-types. So a `model.FaultRaised` alone cannot tell a future emitter which ce-type to emit. It works today only because ASC is the sole kind producing faults. DMS breaks it — in the emitter, which does not exist yet, so nothing would catch it until then.

- [ ] **Step 1: Write the failing tests**

Append to `sdk/model/events_test.go`:
```go
// Events must be self-describing about their device kind: the catalog reuses
// one fault proto across every service, so the kind is what tells a wire
// emitter whether this is dms.fault-raised or signal-control.fault-raised.
func TestEventsCarryDeviceKind(t *testing.T) {
	at := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "dms-1", DeviceKind: "dms", OccurredAt: at}

	var ev Event = FaultRaised{Base: b, FaultID: "dms-fault-pixel", Severity: SeverityMinor}
	if got := ev.EventDeviceKind(); got != "dms" {
		t.Fatalf("EventDeviceKind() = %q, want %q", got, "dms")
	}
	if got := ev.EventDeviceID(); got != "dms-1" {
		t.Fatalf("EventDeviceID() = %q", got)
	}
}
```

Append to `internal/synth/signal_test.go`:
```go
// The Engine must propagate the snapshot's device kind onto every event it
// synthesizes; a differ never sets it.
func TestEngineCopiesDeviceKindOntoEvents(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	snap := &model.Snapshot{
		DeviceID: "asc-1", DeviceKind: "asc", SampledAt: t0,
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}},
	}
	evs := e.Apply(snap)
	if len(evs) == 0 {
		t.Fatal("expected at least one event")
	}
	for _, ev := range evs {
		if got := ev.EventDeviceKind(); got != "asc" {
			t.Errorf("%s: EventDeviceKind() = %q, want %q", ev.EventKind(), got, "asc")
		}
	}
}
```

Append to `internal/runner/runner_test.go`:
```go
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
		if len(events) >= 2 {
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
```

Note `okSnap` in that file returns a `*model.Snapshot` with **no** `DeviceKind` set — deliberately. The runner must stamp it; if the test only passed because a fixture pre-set it, it would prove nothing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./sdk/model/ ./internal/synth/ ./internal/runner/`
Expected: FAIL — `DeviceKind` and `EventDeviceKind` undefined.

- [ ] **Step 3: Implement**

In `sdk/model/events.go`, replace the `Event` interface and `Base`:
```go
// Event is a discrete domain occurrence — produced by synth (from
// consecutive Snapshots) or returned directly by EventReader adapters.
// Emitters (internal/wire) are the only consumers that turn Events into
// wire payloads.
type Event interface {
	EventKind() string
	EventDeviceID() string
	EventDeviceKind() string
	EventOccurredAt() time.Time
}

// Base carries the fields every event has; embed it.
type Base struct {
	DeviceID string
	// DeviceKind is the emitting device's kind ("asc", "dms", ...). It exists
	// because the catalog defines fault-raised/fault-cleared and mode-changed
	// ONCE and reuses them across every service: the same proto is published
	// as dms.fault-raised.v1 or signal-control.fault-raised.v1 depending only
	// on the device. Without it an emitter cannot route a shared event.
	// Stamped by the runner from the adapter's Descriptor — never by adapters.
	DeviceKind string
	OccurredAt time.Time
}

func (b Base) EventDeviceID() string       { return b.DeviceID }
func (b Base) EventDeviceKind() string     { return b.DeviceKind }
func (b Base) EventOccurredAt() time.Time  { return b.OccurredAt }
```

In `sdk/model/model.go`, add to `Snapshot`:
```go
// Snapshot is the state of one device at a single poll.
type Snapshot struct {
	DeviceID string
	// DeviceKind is stamped by the runner from the adapter's Descriptor after
	// Read returns; adapters do not set it. Synth copies it onto every event.
	DeviceKind string
	SampledAt  time.Time
	Facets     []Facet
	Errors     []FacetError
}
```

In `internal/synth/synth.go`, change the base construction in `Apply` (currently line ~51):
```go
	base := model.Base{DeviceID: snap.DeviceID, DeviceKind: snap.DeviceKind, OccurredAt: snap.SampledAt}
```

In `internal/runner/runner.go`, cache the kind at construction and stamp both paths. Add the field:
```go
type Runner struct {
	dev        adapter.StateReader
	deviceID   string
	deviceKind string
	// ... existing fields unchanged
```
In `New`, after the timeout defaulting, set it from the adapter itself:
```go
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
```
In `pollOnce`, stamp the snapshot before synth sees it, and both health events. The success branch becomes:
```go
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
```
and the failure branch's health event gains `DeviceKind: r.deviceKind` the same way.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -v -run 'DeviceKind'`
Expected: PASS — all three new tests.
Run: `make check` — Expected: green (every existing event and test still compiles; `DeviceKind` is additive).
Run: `go test ./internal/runner/ -race -count=3` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sdk/model internal/synth internal/runner
git commit -m "feat(sdk): events carry their device kind

The catalog defines fault-raised/fault-cleared and mode-changed ONCE and
reuses them across all eight services — dms.fault-raised.v1 and
signal-control.fault-raised.v1 are the same proto under different ce-types.
So a model.FaultRaised alone cannot tell a wire emitter which to publish.
That works today only because ASC is the only kind producing faults; a
second kind breaks it, and it would break inside the emitter, which does
not exist yet.

The runner stamps the kind from the adapter's own Descriptor, so adapters
can neither forget it nor report one that disagrees with their
registration. Covers both paths: snapshots (which reach synth) and health
events (which do not come from a snapshot)."
```

---

### Task 2: `sdk/model` — DMS enums and the `DMSStatus` facet

**Files:**
- Create: `sdk/model/dms.go`, `sdk/model/dms_test.go`
- Modify: `sdk/model/enums.go` (three `FaultCategory` additions)

**Interfaces:**
- Produces:
  - `const KindDMSStatus Kind = "dms-status"`
  - `DMSControlMode`, `DMSDisplayState`, `MessageMemoryType`, `MessageStatus`, `MultiSyntaxError` — each with `String()`
  - `type DMSStatus struct{ ... }` implementing `Facet`
  - `FaultCategory` gains `CategoryPixel`, `CategoryController`, `CategoryEnvironment`

- [ ] **Step 1: Write the failing test**

`sdk/model/dms_test.go`:
```go
package model

import "testing"

func TestDMSStatusIsAFacet(t *testing.T) {
	var f Facet = DMSStatus{
		ControlMode: ControlCentral, DisplayState: DisplayNormal,
		ActiveMemoryType: MemoryChangeable, ActiveSlot: 4,
		MessageStatus: StatusValid,
	}
	if f.FacetKind() != KindDMSStatus {
		t.Fatalf("FacetKind() = %q, want %q", f.FacetKind(), KindDMSStatus)
	}
	if got := f.(DMSStatus).ActiveSlot; got != 4 {
		t.Fatalf("ActiveSlot = %d", got)
	}
}

// Every enum's zero value must mean "we do not know", never a real state: a
// sign that fails to answer an object must not read as a definite value.
func TestDMSEnumZeroValuesMeanUnknown(t *testing.T) {
	if ControlUnknown != 0 || DisplayUnknown != 0 || MemoryUnknown != 0 || StatusUnknown != 0 {
		t.Error("control/display/memory/status zero values must be the Unknown variant")
	}
	// MultiSyntaxError is the sharp one. The catalog's ErrorType puts SYNTAX at
	// 0 with NO unspecified value — mirroring that numbering would make an
	// unanswered object report a genuine syntax error. Ours must be None.
	if SyntaxErrorNone != 0 {
		t.Error("MultiSyntaxError zero must be None, or an unanswered object fabricates a syntax error")
	}
	if SyntaxErrorSyntax == 0 {
		t.Error("SyntaxErrorSyntax must NOT be zero — that is the catalog's numbering, not ours")
	}
	var zero DMSStatus
	if zero.ControlMode.String() != "unknown" || zero.DisplayState.String() != "unknown" {
		t.Error("a zero DMSStatus must describe itself as unknown")
	}
	if zero.SyntaxError.String() != "none" {
		t.Errorf("zero SyntaxError.String() = %q, want none", zero.SyntaxError.String())
	}
}

func TestDMSEnumStrings(t *testing.T) {
	for got, want := range map[string]string{
		ControlLocal.String(): "local", ControlCentral.String(): "central",
		ControlCentralOverride.String(): "central-override", ControlSimulation.String(): "simulation",
		ControlExternal.String(): "external", ControlOther.String(): "other",
		DisplayOff.String(): "off", DisplayBlank.String(): "blank",
		DisplayTest.String(): "test", DisplayNormal.String(): "normal",
		MemoryPermanent.String(): "permanent", MemoryChangeable.String(): "changeable",
		MemoryVolatile.String(): "volatile", MemorySchedule.String(): "schedule", MemoryBlank.String(): "blank",
		StatusNotUsed.String(): "not-used", StatusModifying.String(): "modifying",
		StatusValidating.String(): "validating", StatusValid.String(): "valid", StatusError.String(): "error",
		SyntaxErrorSyntax.String(): "syntax", SyntaxErrorUnsupportedTag.String(): "unsupported-tag",
		SyntaxErrorFontNotFound.String(): "font-not-found", SyntaxErrorGraphicNotFound.String(): "graphic-not-found",
		SyntaxErrorTooLong.String(): "too-long", SyntaxErrorHardware.String(): "hardware",
		SyntaxErrorOther.String(): "other",
	} {
		if got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
	if DMSControlMode(99).String() != "unknown" || MultiSyntaxError(99).String() != "none" {
		t.Error("out-of-range values must fall back to their zero meaning")
	}
}

func TestDMSFaultCategories(t *testing.T) {
	for cat, want := range map[FaultCategory]string{
		CategoryPixel: "pixel", CategoryController: "controller", CategoryEnvironment: "environment",
	} {
		if got := cat.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", cat, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/ -run DMS`
Expected: FAIL — `DMSStatus`, `ControlCentral` etc. undefined.

- [ ] **Step 3: Implement**

`sdk/model/dms.go`:
```go
package model

// KindDMSStatus is a dynamic message sign's operational state at one poll.
const KindDMSStatus Kind = "dms-status"

// DMSControlMode is WHO is driving the sign. It is independent of what the
// sign is displaying (DMSDisplayState) — a sign under central control can be
// blank, and a locally-controlled sign can show a message. NTCIP 1203 reports
// them as separate objects and the catalog models them as separate mode axes;
// conflating them would lose information the sign genuinely provides.
type DMSControlMode uint8

const (
	ControlUnknown DMSControlMode = iota
	ControlLocal                  // a technician at the sign; central commands are not honoured
	ControlExternal               // an external system, not the TMC
	ControlCentral                // the TMC / central system
	ControlCentralOverride        // central has overridden local control
	ControlSimulation             // off-line test control
	ControlOther                  // vendor-specific
)

func (m DMSControlMode) String() string {
	switch m {
	case ControlLocal:
		return "local"
	case ControlExternal:
		return "external"
	case ControlCentral:
		return "central"
	case ControlCentralOverride:
		return "central-override"
	case ControlSimulation:
		return "simulation"
	case ControlOther:
		return "other"
	default:
		return "unknown"
	}
}

// DMSDisplayState is what the sign is doing with its display.
type DMSDisplayState uint8

const (
	DisplayUnknown DMSDisplayState = iota
	DisplayOff
	DisplayBlank
	DisplayTest
	DisplayNormal
)

func (s DMSDisplayState) String() string {
	switch s {
	case DisplayOff:
		return "off"
	case DisplayBlank:
		return "blank"
	case DisplayTest:
		return "test"
	case DisplayNormal:
		return "normal"
	default:
		return "unknown"
	}
}

// MessageMemoryType is which memory bank the active message lives in.
type MessageMemoryType uint8

const (
	MemoryUnknown MessageMemoryType = iota
	MemoryPermanent
	MemoryChangeable
	MemoryVolatile
	MemorySchedule
	MemoryBlank
)

func (m MessageMemoryType) String() string {
	switch m {
	case MemoryPermanent:
		return "permanent"
	case MemoryChangeable:
		return "changeable"
	case MemoryVolatile:
		return "volatile"
	case MemorySchedule:
		return "schedule"
	case MemoryBlank:
		return "blank"
	default:
		return "unknown"
	}
}

// MessageStatus is the health of the active message slot.
type MessageStatus uint8

const (
	StatusUnknown MessageStatus = iota
	StatusNotUsed
	StatusModifying
	StatusValidating
	StatusValid
	StatusError
)

func (s MessageStatus) String() string {
	switch s {
	case StatusNotUsed:
		return "not-used"
	case StatusModifying:
		return "modifying"
	case StatusValidating:
		return "validating"
	case StatusValid:
		return "valid"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

// MultiSyntaxError is why a MULTI message failed to activate.
//
// Note the zero value is None, NOT Syntax. The catalog's ErrorType puts
// SYNTAX at 0 with no unspecified variant; mirroring that numbering here
// would make a sign that simply does not answer the object report a genuine
// syntax error. Mapping ours onto theirs is the emitter's problem — one
// table — and is cheaper than fabricating errors at the collection boundary.
type MultiSyntaxError uint8

const (
	SyntaxErrorNone MultiSyntaxError = iota
	SyntaxErrorSyntax
	SyntaxErrorUnsupportedTag
	SyntaxErrorFontNotFound
	SyntaxErrorGraphicNotFound
	SyntaxErrorTooLong
	SyntaxErrorHardware
	SyntaxErrorOther
)

func (e MultiSyntaxError) String() string {
	switch e {
	case SyntaxErrorSyntax:
		return "syntax"
	case SyntaxErrorUnsupportedTag:
		return "unsupported-tag"
	case SyntaxErrorFontNotFound:
		return "font-not-found"
	case SyntaxErrorGraphicNotFound:
		return "graphic-not-found"
	case SyntaxErrorTooLong:
		return "too-long"
	case SyntaxErrorHardware:
		return "hardware"
	case SyntaxErrorOther:
		return "other"
	default:
		return "none"
	}
}

// DMSStatus is what a sign reports about what it is doing. It carries only
// state the collector acts on; brightness, pixel/lamp diagnostics, and
// environment are modeled upstream but nothing has asked for them, so they
// are omitted until something does (adding them is additive).
type DMSStatus struct {
	ControlMode      DMSControlMode
	DisplayState     DMSDisplayState
	ActiveMemoryType MessageMemoryType
	ActiveSlot       uint32
	MessageStatus    MessageStatus
	SyntaxError      MultiSyntaxError // meaningful only when MessageStatus == StatusError
	SyntaxErrorPos   uint32           // character offset into the MULTI string
}

func (DMSStatus) FacetKind() Kind { return KindDMSStatus }
```

Append the three categories to `sdk/model/enums.go`'s `FaultCategory` const block and `String()`:
```go
	CategoryPixel       // dms-fault-pixel
	CategoryController  // dms-fault-controller
	CategoryEnvironment // dms-fault-environment
```
and in `String()`:
```go
	case CategoryPixel:
		return "pixel"
	case CategoryController:
		return "controller"
	case CategoryEnvironment:
		return "environment"
```
Append them to the END of the const block — `FaultCategory` values are not persisted anywhere, but keeping existing values stable avoids churning the ASC bitmap's meaning.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/ -v -run 'DMS|Fault'`
Expected: PASS, including the pre-existing fault tests.

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): DMS status facet and enums

Control mode (who is driving) and display state (what it shows) are
separate axes because the sign reports them separately — a centrally
controlled sign can be blank.

MultiSyntaxError's zero is None, deliberately unlike the catalog's
ErrorType which puts SYNTAX at 0 with no unspecified variant. Mirroring
that numbering would make an unanswered object report a real syntax error;
the emitter can map one table instead.

FaultCategory gains pixel/controller/environment, the three DMS fault
identities the enum lacked. DMS reuses FaultSet rather than duplicating
it — that is what the catalog does."
```

---

### Task 3: `sdk/model` — DMS domain events

**Files:**
- Modify: `sdk/model/events.go`, `sdk/model/events_test.go`

**Interfaces:**
- Consumes: `Base`, `Event` (Task 1); the DMS enums (Task 2).
- Produces:
  - `DMSControlModeChanged{Base; From, To DMSControlMode}` → `"control-mode-changed"`
  - `DMSDisplayStateChanged{Base; From, To DMSDisplayState}` → `"display-state-changed"`
  - `DMSMessageActivationFailed{Base; MemoryType MessageMemoryType; Slot uint32; Error MultiSyntaxError; ErrorPosition uint32}` → `"message-activation-failed"`

- [ ] **Step 1: Write the failing test**

Append to `sdk/model/events_test.go`:
```go
func TestDMSEventKindsAndAccessors(t *testing.T) {
	at := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "dms-1", DeviceKind: "dms", OccurredAt: at}

	cases := []struct {
		ev   Event
		kind string
	}{
		{DMSControlModeChanged{Base: b, From: ControlLocal, To: ControlCentral}, "control-mode-changed"},
		{DMSDisplayStateChanged{Base: b, From: DisplayBlank, To: DisplayNormal}, "display-state-changed"},
		{DMSMessageActivationFailed{Base: b, MemoryType: MemoryChangeable, Slot: 4,
			Error: SyntaxErrorFontNotFound, ErrorPosition: 12}, "message-activation-failed"},
	}
	for _, c := range cases {
		if got := c.ev.EventKind(); got != c.kind {
			t.Errorf("EventKind() = %q, want %q", got, c.kind)
		}
		if got := c.ev.EventOccurredAt(); !got.Equal(at) {
			t.Errorf("%s: OccurredAt = %v", c.kind, got)
		}
		if got := c.ev.EventDeviceKind(); got != "dms" {
			t.Errorf("%s: DeviceKind = %q", c.kind, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/ -run DMSEvent`
Expected: FAIL — `DMSControlModeChanged` undefined.

- [ ] **Step 3: Implement**

Append to `sdk/model/events.go`:
```go
// DMSControlModeChanged fires when control of the sign moves between local,
// central, override, etc. Separate from DMSDisplayStateChanged: they are
// independent axes and an operator taking local control is a different
// occurrence from the display going blank.
type DMSControlModeChanged struct {
	Base
	From, To DMSControlMode
}

func (DMSControlModeChanged) EventKind() string { return "control-mode-changed" }

// DMSDisplayStateChanged fires when what the sign is showing changes state
// (off / blank / test / normal).
type DMSDisplayStateChanged struct {
	Base
	From, To DMSDisplayState
}

func (DMSDisplayStateChanged) EventKind() string { return "display-state-changed" }

// DMSMessageActivationFailed fires when a message slot transitions INTO an
// error state — once per transition, not once per poll while it stays broken.
type DMSMessageActivationFailed struct {
	Base
	MemoryType    MessageMemoryType
	Slot          uint32
	Error         MultiSyntaxError
	ErrorPosition uint32 // character offset into the MULTI string
}

func (DMSMessageActivationFailed) EventKind() string { return "message-activation-failed" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/ -v`
Expected: PASS, all model tests.

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): DMS domain events

Two mode events, not one: the catalog carries both axes in a single
mode-changed ce-type discriminated by kind, but they are different
transitions with different consequences. Collapsing them in the domain to
fit one wire shape would be the wire dictating the domain (ADR 0002); the
emitter maps both onto dms.mode-changed.v1."
```

---

### Task 4: `internal/synth` — the DMS differ

**Files:**
- Create: `internal/synth/dms.go`, `internal/synth/dms_test.go`

**Interfaces:**
- Consumes: `Differ` (existing), `model.DMSStatus` and the DMS events.
- Produces: `func NewDMSDiffer() Differ`

- [ ] **Step 1: Write the failing test**

`internal/synth/dms_test.go`:
```go
package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func dmsSnap(at time.Time, st model.DMSStatus) *model.Snapshot {
	return &model.Snapshot{DeviceID: "dms-1", DeviceKind: "dms", SampledAt: at,
		Facets: []model.Facet{st}}
}

var dmsNormal = model.DMSStatus{
	ControlMode: model.ControlCentral, DisplayState: model.DisplayNormal,
	ActiveMemoryType: model.MemoryChangeable, ActiveSlot: 4,
	MessageStatus: model.StatusValid,
}

// Nothing has transitioned on the first observation; we have merely learned
// the current state.
func TestDMSFirstPollEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	if evs := e.Apply(dmsSnap(t0, dmsNormal)); len(evs) != 0 {
		t.Fatalf("first poll must emit nothing, got %v", kinds(evs))
	}
}

func TestDMSNoChangeEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	if evs := e.Apply(dmsSnap(t0.Add(time.Second), dmsNormal)); len(evs) != 0 {
		t.Fatalf("unchanged status must emit nothing, got %v", kinds(evs))
	}
}

// The two axes are independent: each must fire on its own without dragging
// the other along.
func TestDMSAxesChangeIndependently(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	// Control mode only.
	local := dmsNormal
	local.ControlMode = model.ControlLocal
	evs := e.Apply(dmsSnap(t0.Add(time.Second), local))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 control-mode-changed", kinds(evs))
	}
	cm, ok := evs[0].(model.DMSControlModeChanged)
	if !ok || cm.From != model.ControlCentral || cm.To != model.ControlLocal {
		t.Fatalf("bad control-mode event: %+v", evs[0])
	}

	// Display state only.
	blank := local
	blank.DisplayState = model.DisplayBlank
	evs = e.Apply(dmsSnap(t0.Add(2*time.Second), blank))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 display-state-changed", kinds(evs))
	}
	ds, ok := evs[0].(model.DMSDisplayStateChanged)
	if !ok || ds.From != model.DisplayNormal || ds.To != model.DisplayBlank {
		t.Fatalf("bad display-state event: %+v", evs[0])
	}
}

func TestDMSBothAxesChangeAtOnce(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	both := dmsNormal
	both.ControlMode = model.ControlLocal
	both.DisplayState = model.DisplayBlank
	evs := e.Apply(dmsSnap(t0.Add(time.Second), both))
	if got := kinds(evs); len(got) != 2 || got[0] != "control-mode-changed" || got[1] != "display-state-changed" {
		t.Fatalf("events = %v, want [control-mode-changed display-state-changed] in that order", got)
	}
}

// Entering the error state reports once. A sign sitting broken must not
// re-report every poll — that would be a fault storm, not information.
func TestDMSActivationFailureReportsOnceOnTransition(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	broken := dmsNormal
	broken.MessageStatus = model.StatusError
	broken.SyntaxError = model.SyntaxErrorFontNotFound
	broken.SyntaxErrorPos = 12

	evs := e.Apply(dmsSnap(t0.Add(time.Second), broken))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 message-activation-failed", kinds(evs))
	}
	f, ok := evs[0].(model.DMSMessageActivationFailed)
	if !ok || f.Error != model.SyntaxErrorFontNotFound || f.ErrorPosition != 12 ||
		f.MemoryType != model.MemoryChangeable || f.Slot != 4 {
		t.Fatalf("bad activation-failed event: %+v", evs[0])
	}

	// Still broken, still the same error: silence.
	if evs := e.Apply(dmsSnap(t0.Add(2*time.Second), broken)); len(evs) != 0 {
		t.Fatalf("sustained error must not re-report, got %v", kinds(evs))
	}

	// Recovers, then breaks again: reports again.
	e.Apply(dmsSnap(t0.Add(3*time.Second), dmsNormal))
	if evs := e.Apply(dmsSnap(t0.Add(4*time.Second), broken)); len(evs) != 1 {
		t.Fatalf("re-entering error must report again, got %v", kinds(evs))
	}
}

// The iron rule, for this facet.
func TestDMSFailedReadEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	failed := &model.Snapshot{DeviceID: "dms-1", DeviceKind: "dms", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindDMSStatus, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}

	// Recovery against the surviving prev: unchanged state, so still silence.
	if evs := e.Apply(dmsSnap(t0.Add(2*time.Second), dmsNormal)); len(evs) != 0 {
		t.Fatalf("post-recovery unchanged state must emit nothing, got %v", kinds(evs))
	}
}

func TestDMSEventsCarryDeviceKind(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	local := dmsNormal
	local.ControlMode = model.ControlLocal
	evs := e.Apply(dmsSnap(t0.Add(time.Second), local))
	if len(evs) != 1 || evs[0].EventDeviceKind() != "dms" {
		t.Fatalf("DMS events must carry DeviceKind=dms, got %+v", evs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/synth/ -run DMS`
Expected: FAIL — `NewDMSDiffer` undefined.

- [ ] **Step 3: Implement**

`internal/synth/dms.go`:
```go
package synth

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// NewDMSDiffer diffs the dms-status facet into transition events.
//
// DMS emits on transitions only — the catalog has no periodic DMS state
// report, so there is no per-poll event to produce. A consequence worth
// knowing: after a collector restart a sign's current state is not
// re-announced until it next changes. That is a wire-mapping gap (5 of 8
// services lack a status-report ce-type), not something the differ should
// paper over by inventing a transition that did not happen.
func NewDMSDiffer() Differ { return dmsDiffer{} }

type dmsDiffer struct{}

func (dmsDiffer) Kind() model.Kind { return model.KindDMSStatus }

func (dmsDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.DMSStatus)
	if prev == nil {
		return nil // nothing transitioned; we just learned the state
	}
	p := prev.(model.DMSStatus)

	// Order is fixed by construction, so events are deterministic without a
	// sort: control mode, then display state, then activation failure.
	var events []model.Event
	if p.ControlMode != c.ControlMode {
		events = append(events, model.DMSControlModeChanged{
			Base: base, From: p.ControlMode, To: c.ControlMode,
		})
	}
	if p.DisplayState != c.DisplayState {
		events = append(events, model.DMSDisplayStateChanged{
			Base: base, From: p.DisplayState, To: c.DisplayState,
		})
	}
	// Only the TRANSITION into error reports. A sign sitting broken would
	// otherwise re-report every poll — a storm, not information.
	if p.MessageStatus != model.StatusError && c.MessageStatus == model.StatusError {
		events = append(events, model.DMSMessageActivationFailed{
			Base: base, MemoryType: c.ActiveMemoryType, Slot: c.ActiveSlot,
			Error: c.SyntaxError, ErrorPosition: c.SyntaxErrorPos,
		})
	}
	return events
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/synth/ -v -run DMS`
Expected: PASS.
Run: `go test ./internal/synth/ -count=3` — Expected: PASS (whole package).

- [ ] **Step 5: Commit**

```bash
git add internal/synth
git commit -m "feat(core): DMS differ — transition events for both mode axes

Only the transition INTO a message error reports; a sign sitting broken
would otherwise re-report every poll, which is a storm rather than
information.

DMS emits on transitions only because the catalog has no periodic DMS
state report. After a restart a sign's state is not re-announced until it
changes — a wire gap (5 of 8 services lack a status-report ce-type), not
something to paper over by inventing a transition that did not happen."
```

---

### Task 5: `internal/app` — register the DMS differ

**Files:**
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: `synth.NewDMSDiffer` (Task 4).
- Produces: no new API.

- [ ] **Step 1: Make the change**

In `internal/app/app.go`, add the DMS differ to the engine:
```go
	engine := synth.NewEngine(
		synth.NewSignalDiffer(),
		synth.NewFaultDiffer(),
		synth.NewDetectorDiffer(),
		synth.NewDMSDiffer(),
	)
```

Registering a differ for a facet nothing currently produces is deliberate and harmless: `Engine.Apply` only diffs facets present in a snapshot, so this is inert until the `ntcip-dms` adapter lands — at which point that adapter needs no core changes at all.

- [ ] **Step 2: Verify**

Run: `make check` — Expected: green.
Run: `go test ./... -race -count=1` — Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git add internal/app
git commit -m "feat(core): register the DMS differ

Inert until the ntcip-dms adapter exists — Engine only diffs facets a
snapshot actually carries — so the adapter will need no core changes."
```

---

## Final verification

```bash
make check                    # vet, tests, boundary lint + selftest
go test ./... -race           # concurrency
go build ./... && go run ./cmd/collector -version
go list -deps ./... | grep -c openits-models    # expect 0
```

Confirm `DeviceKind` reaches events on both paths:

```bash
go test ./... -run DeviceKind -v
```

## Follow-on (not in this plan)

- **The `ntcip-dms` adapter** — blocked on an NTCIP 1203 OID source (a MIB, or an `snmpwalk` of a real sign; there is no sign available at time of writing). When it lands it needs only `internal/vendors/ntcip/dms.go` + fixtures: the facet, differ, and registration are already in place. Note the distinction: a MIB yields the OIDs and unblocks the adapter; a real sign additionally yields the fixture values that prove it works.
- **Plan 2 inherits two things from this work:** `Base.DeviceKind` is what lets it route shared fault/mode events to the right service ce-type, and the missing status-report ce-types (dms, rsu, ess, ramp-metering, reversible-lane — 5 of 8 services) are its map-or-drop decision to make, or an upstream ask to file.
- **The omitted DMS state** — brightness, pixel/lamp diagnostics, environment, uptime, schedules. Modeled upstream, nothing has asked for them, additive when something does.
