# DMS Domain Model + Differ — Design

**Date:** 2026-07-16
**Status:** Approved design, pending implementation plan
**Scope:** domain model and synth only. The `ntcip-dms` **adapter is deliberately
not in this spec** — see §2.

## 1. Purpose

Add DMS (dynamic message sign) as the collector's second device kind: the
`DMSStatus` facet, its differ, and the domain events they produce. `FaultSet` is
reused rather than duplicated.

This also closes an architectural gap that only appears once a second device kind
exists: domain events cannot currently say which kind of device produced them
(§3).

## 2. Why there is no adapter here

**NTCIP 1203 OIDs do not exist in any repo we control.** openits-models
references 1203 objects **by name only** (`dmsControlMode`, `dmsMessageStatus`,
`dmsActivateMessage`, `dmsMultiSyntaxError`, …) and never a numeric OID; its own
docs state *"NTCIP 1203 v03 (functional reference only; no wire compatibility)"*
and *"SNMP OIDs are mapped at translation time"* — i.e. the collector's job.
Gen-1 has no DMS code at all (`git grep -i dms` over the pre-wipe tree at
`a97bf7f^`: zero hits), so unlike the ASC alarm bitmap there is not even a prior
guess to inherit and caveat.

Inventing OIDs would be worse than the bitmap we shipped with a caveat: the
bitmap at least came from somewhere, and its failure mode is a mislabeled fault.
A fabricated OID fails silently against real hardware, and fixtures cannot catch
it because they would encode the same fabrication.

The domain model, the differ, and (later) the emitter mapping need **no OIDs**.
Only the adapter does. So this spec builds everything that is knowable now and
leaves `internal/vendors/ntcip/dms.go` as the single open piece, unblocked the
moment a MIB or an `snmpwalk` of a real sign is available.

## 3. `Base.DeviceKind` — the gap a second device kind exposes

The catalog defines `fault-raised`/`fault-cleared` **once**, in
`openits.common.v1`, reused by all eight services. DMS faults and ASC faults are
the same proto; they differ only in the ce-type they are published as —
`openits.dms.fault-raised.v1` vs `openits.signal-control.fault-raised.v1`. The
same is true of `mode-changed`.

`model.FaultRaised` carries `DeviceID` and nothing else, so an emitter receiving
one **cannot tell which ce-type to emit**. Today this accidentally works because
ASC is the only kind that produces faults. Adding DMS breaks it — and it breaks
in the emitter, which does not exist yet, so nothing would catch it until Plan 2.

**Decision:** `model.Base` gains `DeviceKind string`, and `model.Event` gains
`EventDeviceKind() string`.

**The runner stamps it, not the adapter.** After `Read` returns, the runner sets
`snap.DeviceKind` from the adapter's own `Descriptor().DeviceKind`. Adapters
cannot forget it or misreport it, and `Snapshot` stays self-describing.
`synth.Engine.Apply` copies it into each event's `Base`. Health events are
stamped by the runner directly, from the same `Descriptor`.

This is what makes the `FaultSet` reuse honest: the facet genuinely *is*
cross-kind, exactly as the catalog models it, and the kind rides on the event
rather than being inferred.

## 4. Domain model

```go
// KindDMSStatus is a sign's operational state at one poll.
const KindDMSStatus Kind = "dms-status"

// DMSStatus is what a DMS reports about what it is doing. It deliberately
// carries only state the collector acts on; see §7 for what is left out.
type DMSStatus struct {
    ControlMode      DMSControlMode   // who is driving the sign
    DisplayState     DMSDisplayState  // what it is doing with the display
    ActiveMemoryType MessageMemoryType
    ActiveSlot       uint32
    MessageStatus    MessageStatus
    SyntaxError      MultiSyntaxError // meaningful only when MessageStatus == StatusError
    SyntaxErrorPos   uint32           // character offset into the MULTI string
}

func (DMSStatus) FacetKind() Kind { return KindDMSStatus }
```

### Enums (collector-owned)

Values mirror the catalog's DMS identities and enums so a later mapping is a
straight table, but the types are ours and do not move when upstream renumbers.

```go
type DMSControlMode uint8   // Unknown, Local, External, Central, CentralOverride, Simulation, Other
type DMSDisplayState uint8  // Unknown, Off, Blank, Test, Normal
type MessageMemoryType uint8 // Unknown, Permanent, Changeable, Volatile, Schedule, Blank
type MessageStatus uint8    // Unknown, NotUsed, Modifying, Validating, Valid, Error
type MultiSyntaxError uint8 // None, Syntax, UnsupportedTag, FontNotFound, GraphicNotFound, TooLong, Hardware, Other
```

Each gets a `String()`. Each has an `Unknown`/`None` zero value: a sign that does
not answer an object must not silently read as a real state. (`MultiSyntaxError`
uses `None` as zero because the catalog's `ErrorType` puts `SYNTAX` at 0 — our
zero must mean "no error", not "syntax error", or an unanswered object would
fabricate one.)

**Two mode axes, not one.** `ControlMode` (who is driving: local vs central vs
override) and `DisplayState` (off/blank/test/normal) are independent. The
catalog models both as `mode-changed` discriminated by `kind`; conflating them
would lose information the sign genuinely reports separately.

### FaultCategory additions

DMS reuses `model.FaultSet`. Its fault identities require three categories the
enum lacks; `Power`, `Communication`, and `Lamp` already exist:

```go
CategoryPixel        // dms-fault-pixel
CategoryController   // dms-fault-controller
CategoryEnvironment  // dms-fault-environment
```

## 5. Domain events

```go
type DMSControlModeChanged struct { Base; From, To DMSControlMode }
func (DMSControlModeChanged) EventKind() string { return "control-mode-changed" }

type DMSDisplayStateChanged struct { Base; From, To DMSDisplayState }
func (DMSDisplayStateChanged) EventKind() string { return "display-state-changed" }

type DMSMessageActivationFailed struct {
    Base
    MemoryType    MessageMemoryType
    Slot          uint32
    Error         MultiSyntaxError
    ErrorPosition uint32
}
func (DMSMessageActivationFailed) EventKind() string { return "message-activation-failed" }
```

Plus `FaultRaised`/`FaultCleared`, reused unchanged.

`message-activation-failed` matches the catalog's event token exactly. The two
mode events do not — the catalog has one `mode-changed` ce-type carrying both
axes via `kind`. Splitting them in the domain is deliberate: they are different
transitions with different consequences, and collapsing them to fit one wire
shape would be the wire dictating the domain (ADR 0002). The emitter maps both
to `openits.dms.mode-changed.v1` with the appropriate `kind`.

## 6. Synth

`NewDMSDiffer()`, registered alongside the existing differs.

- `ControlMode` changed → `DMSControlModeChanged{From, To}`
- `DisplayState` changed → `DMSDisplayStateChanged{From, To}`
- `MessageStatus` **transitioned into** `StatusError` → `DMSMessageActivationFailed`
  carrying the active slot and the syntax error. Only the transition emits: a
  sign sitting in error state must not re-report every poll.
- `prev == nil` (first observation) → **no events.** Nothing has transitioned; we
  have merely learned the current state.
- Event order within a poll is fixed by construction (control-mode, then
  display-state, then activation-failed) — deterministic without a sort, as
  `alarmBitmap` already is.

A facet in `snap.Errors` emits nothing — `synth.Engine` never calls `Diff` for
it. The iron rule holds for free, as with every other differ.

**Known consequence:** DMS emits only on transitions, so after a collector
restart a sign's current state is not re-announced. Signal-control avoids this
via `operational-status-report.v1`, emitted every poll; the catalog has no DMS
equivalent (nor for rsu, ess, ramp-metering, or reversible-lane — 5 of 8
services). This is a **wire-mapping concern, not a collection one**: the
collector has the state and models it faithfully. Whether it can be published is
the emitter's map-or-drop decision (ADR 0002), and closing it properly means a
status-report notification upstream. Recorded here so Plan 2 inherits the
question rather than rediscovering it.

## 7. Deliberately not modeled in v1

The DMS YANG models a large state tree; these parts are omitted because **nothing
has asked for them**, not because the wire lacks a home:

- brightness (setpoint and current), illumination control
- diagnostics: pixel counts (total / stuck-on / stuck-off / failed), lamp counts,
  last self-test
- environment: cabinet temperature, humidity, ambient light, door-open
- controller uptime, schedules, day plans, fonts, sign capabilities/geometry

Adding any of them is additive — a facet field plus a differ branch — not a
refactor. The argument for including them later is that the poll round-trip is
already being paid; the argument against today is YAGNI.

## 8. Testing

- **Differ table tests** on fixed timestamps: each axis transitioning
  independently, both at once, no-change emitting nothing, first-poll emitting
  nothing, and `MessageStatus` entering error exactly once across a sustained
  error state.
- **`DeviceKind` propagation:** a test proving the runner stamps it from the
  `Descriptor` and that it reaches an event's `Base` — including for health
  events, which do not come from a snapshot.
- **Suspension:** a failed `dms-status` read emits nothing and the previous facet
  survives.
- **Enum zero values:** `MultiSyntaxError`'s zero is `None`, not `Syntax` — a
  test pinning this, since getting it backwards would fabricate a syntax error
  from an unanswered object.
- **Every guard shown to fail.** Each invariant above gets a test verified by
  deliberately breaking the thing it guards. Three times this session a check
  that passed turned out to be incapable of failing.

## 9. Out of scope

- **The `ntcip-dms` adapter** (§2) — blocked on an OID source. When it lands it
  needs only `internal/vendors/ntcip/dms.go` + fixtures; the differ registration
  is already in place.
- **Wire mapping** — Plan 2. These events will take the loud-drop path until
  `wire/openitsv1` exists, exactly as every other domain event does today.
- **DMS commands** (activating a message) — the `Commander` seam stays dormant;
  v1 is collect-only.
- **Vendor augments** — the catalog carries e.g. a Ledstar travel-time augment.
  Per the architecture's rule of three, no vendor overlay mechanism until ~3
  variant adapters exist.
