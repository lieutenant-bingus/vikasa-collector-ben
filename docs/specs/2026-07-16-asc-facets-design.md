# ASC Facets: FaultSet + DetectorSamples — Design

**Date:** 2026-07-16
**Status:** Approved design, pending implementation plan
**Implements:** architecture spec §9 build-order item 4, in part. The RSU facet
(`RSUBroadcastCounters` + `ntcip-rsu`) is deliberately a separate spec — see §8.

## 1. Purpose

Plan 1 shipped the spine with one facet (`SignalStatus`). This adds the two
remaining signal-controller facets — `FaultSet` and `DetectorSamples` — to the
existing `ntcip-asc` adapter, with their differs.

No openits-models dependency: this is domain model, synth, and adapter only.
That is the architecture working as designed — new facets are a `sdk/model` +
`internal/synth` + adapter problem, and the wire mapping is Plan 2's.

## 2. Prior art, and what we reject

Gen-1 implemented both of these. The code is deleted but readable at `a97bf7f^`.
It is mined here for its OIDs and its mistakes.

**Kept:** the OIDs, the fault-id keying (stable strings, not bit positions), the
batched-indexed-Get strategy for the detector table, and putting the raw counter
in the reading with the delta in synth.

**Rejected, each for a stated reason:**

| Gen-1 behavior | Why it is not ported |
|---|---|
| `snmp-unreachable` synthetic fault | On any SNMP failure gen-1 returned a reading containing *only* this fault. Because `diffFaults` was a pure set-difference, that **cleared every real fault**, then re-raised them on recovery. The mechanism meant to signal unreachability manufactured false clears. Gen-2 has no need: `synth.Engine` suspends a facet listed in `snap.Errors`, and reachability is already a health event (ADR 0007). |
| Unsorted raise/clear order | `diffFaults` iterated a Go map, so event order was nondeterministic. This repo requires sorted iteration wherever output is goldened or hashed. |
| First-poll volume as absolute | With no previous sample, gen-1 reported the raw cumulative counter as if it were an interval count — a large bogus spike on every collector restart. |
| `Lane` / `Approach` on detectors | Present in the struct, plumbed to the wire, and **never populated** by any code path. Dead. |
| Per-channel detector `Status` | Read every poll, consumed by nothing. `detector-fault` comes from alarm bit 6, not this column. |
| `FirstObserved` on the fault | The adapter has no memory, so gen-1 set it to `SampledAt` every poll — it never carried a true first observation. The raise event's `OccurredAt` is that value. |

## 3. Domain model (`sdk/model`)

```go
// KindFaultSet is the set of faults currently raised on a device.
const KindFaultSet Kind = "fault-set"

// Fault is one raised fault. ID is stable and human-readable ("mmu-fault"),
// not a bit position: it is the identity synth diffs on and the value the
// wire's fault_id carries.
type Fault struct {
    ID          string
    Severity    FaultSeverity
    Category    FaultCategory
    Description string
}

type FaultSet struct{ Faults []Fault }

func (FaultSet) FacetKind() Kind { return KindFaultSet }
```

```go
// KindDetectorSamples is per-channel vehicle detector data at one poll.
const KindDetectorSamples Kind = "detector-samples"

// DetectorSample is one channel as read. VolumeCount is the RAW cumulative
// counter the controller reported; the differ turns consecutive values into an
// interval delta. Keeping the raw value here leaves the adapter memoryless.
type DetectorSample struct {
    Channel         uint32 // NTCIP channel number == table row, 1-based
    VolumeCount     uint32 // cumulative Counter32
    OccupancyTenths uint16 // 0..1000
}

type DetectorSamples struct{ Samples []DetectorSample } // sorted by Channel

func (DetectorSamples) FacetKind() Kind { return KindDetectorSamples }
```

### Enums (collector-owned)

```go
type FaultSeverity uint8
const (
    SeverityInfo FaultSeverity = iota
    SeverityWarning
    SeverityMinor
    SeverityMajor
    SeverityCritical
)

type FaultCategory uint8
const (
    CategoryUnknown FaultCategory = iota
    CategoryConflict
    CategoryCabinet
    CategoryPower
    CategoryCommunication
    CategoryDetector
    CategoryLamp
)
```

`FaultSeverity`'s order mirrors the catalog's `FAULT_SEVERITY_*` (INFO=0 …
CRITICAL=4) so Plan 2's mapping is a straight table — but the type is
collector-owned and does not move when upstream renumbers. Both enums get
`String()`.

`FaultCategory` has **no wire home today**: the catalog folded category into a
free-form `kind` string on `FaultRaised`/`FaultCleared` and keeps `Category`
only on the *state* `Fault` message. Category stays in the domain regardless —
this is the map-or-drop decision ADR 0002 exists to localize in one emitter,
and the domain model being richer than a given wire version is the point, not a
problem.

### Occupancy units

NTCIP reports occupancy in **half-percent** (0..200). The catalog's
`DetectorReport` wants **percent** (0..100). The domain carries **tenths**
(0..1000), which represents half-percent exactly (0.5% = 5) and is therefore
lossless; the emitter rounds to the wire's coarser precision. Adapter conversion
is `tenths = halfPercent * 5`.

## 4. Domain events

```go
type FaultRaised struct {
    Base
    FaultID     string
    Severity    FaultSeverity
    Category    FaultCategory
    Description string
}
func (FaultRaised) EventKind() string { return "fault-raised" }

type FaultCleared struct {
    Base
    FaultID string
}
func (FaultCleared) EventKind() string { return "fault-cleared" }

// DetectorReading is one channel's contribution to a report.
type DetectorReading struct {
    Channel         uint32
    VolumeDelta     uint32 // vehicles in [IntervalStart, OccurredAt]
    OccupancyTenths uint16
}

type DetectorReport struct {
    Base
    IntervalStart    time.Time
    IntervalDuration time.Duration
    Readings         []DetectorReading // sorted by Channel
}
func (DetectorReport) EventKind() string { return "detector-report" }
```

Event kinds match the catalog's ce-type event tokens (`fault-raised`,
`fault-cleared`, `detector-report`) so Plan 2's mapping stays mechanical.

## 5. Synth

### Fault differ

Set-difference on `Fault.ID`, **iterating in sorted order** so event sequence is
deterministic:

- in `curr`, not in `prev` → `FaultRaised`
- in `prev`, not in `curr` → `FaultCleared`
- present in both → no event, even if Severity/Description changed (a fault's
  attributes changing is not a raise; nothing consumes an "amended" event, and
  inventing one is YAGNI)
- `prev == nil` (first observation) → everything currently raised is a
  `FaultRaised`. Correct: we have just learned these exist.

**A failed read cannot clear a fault.** `synth.Engine` already keeps the
previous facet and emits nothing when the kind is in `snap.Errors`. This is the
gen-1 bug (§2) prevented structurally rather than by special-casing.

### Detector differ

- `prev == nil` → **no event.** There is no interval to attribute counts to.
- otherwise → one `DetectorReport`, `IntervalStart = prev.SampledAt`,
  `IntervalDuration = curr.SampledAt - prev.SampledAt` carried **losslessly** as
  a `time.Duration`, readings sorted by channel. Rounding to the wire's whole
  seconds is the emitter's job, for the same reason occupancy is tenths: a
  `uint32` of seconds here would round a sub-second poll interval to zero while
  the volume delta stayed non-zero — a divide-by-zero for any rate consumer, and
  invalid against the catalog's 1..3600 constraint.
- per channel: `VolumeDelta = curr - prev` when `curr >= prev`; when
  `curr < prev`, treat as a controller reset and report `curr`. At vehicle
  volumes a genuine Counter32 wrap is ~136 years away, so a decrease is a reset
  in practice; reporting `curr` is right for a reset and the case is documented.
- a channel with no previous sample is **omitted from that report** — same rule
  as first poll, applied per channel. It appears in the next one.
- occupancy is never delta'd: it is a fraction, not a counter.

## 6. Adapter (`internal/vendors/ntcip`)

`ntcip-asc` gains two reads. New OIDs (NTCIP 1202, verbatim from gen-1):

| OID | Meaning |
|---|---|
| `.1.3.6.1.4.1.1206.4.2.1.5.1.0` | short alarm status — bitmap of raised alarms |
| `.1.3.6.1.4.1.1206.4.2.1.2.3.0` | maxVehicleDetectors |
| `.1.3.6.1.4.1.1206.4.2.1.2.4.1.2.<ch>` | detector volume (Counter32) |
| `.1.3.6.1.4.1.1206.4.2.1.2.4.1.4.<ch>` | detector occupancy (half-percent) |

**Detector table strategy:** bound the channel count with `maxVehicleDetectors`
(accept `1..255`, else default 32), then issue synthesized indexed OIDs
(`<prefix>.<ch>`) in a **single `Get` call** rather than a walk. The transport
chunks that call into batches of 16 varbinds per round-trip, so a full
255-channel table is still ~32 round-trips — but a `GetNext`/`BulkWalk` walk
would multiply round-trips by channel count instead (~510 for that same
table), so the synthesized-Get approach is still far better. Channels
answering `NoSuchInstance` are absent and skipped; a sparse table therefore
works, and a channel that answers volume but not occupancy is skipped rather
than half-reported.

### The alarm bitmap

Ported verbatim from gen-1, **including its caveat**, which is load-bearing
documentation rather than decoration:

> Bit positions are conservative; real-world NTCIP 1202 deployments vary per
> vendor. These are the well-known bits downstream dashboards typically surface.
> **This table has never been validated against a physical controller.**

| Bit | ID | Severity | Category |
|---|---|---|---|
| 0 | `conflict-monitor` | Critical | Conflict |
| 1 | `mmu-fault` | Critical | Conflict |
| 2 | `cabinet-door` | Minor | Cabinet |
| 3 | `power-loss` | Major | Power |
| 4 | `low-battery` | Major | Power |
| 5 | `comm-loss` | Major | Communication |
| 6 | `detector-fault` | Minor | Detector |
| 7 | `lamp-out` | Major | Lamp |

A wrong bit assignment emits a confidently-mislabeled fault, and fixtures cannot
catch it because they encode the same assumption. Validating against real
hardware is follow-up work, not a blocker; per-vendor overlays are deferred by
the architecture spec until ~3 variant adapters exist (rule of three).

### Failure domains

Three facets, three independent failure domains, one snapshot — this is the
facet design earning itself:

- alarm OID unanswered → `FacetError{Kind: KindFaultSet}`; `SignalStatus` and
  `DetectorSamples` still publish.
- detector OIDs unanswered → `FacetError{Kind: KindDetectorSamples}` only.
- transport error (the whole `Get` fails) → hard `Read` error, as today. The
  runner turns that into a health event; it is not a fault.

An empty detector table (`maxVehicleDetectors == 0`, or every channel absent) is
an **empty facet, not an error** — a controller with no detectors is a normal
deployment, and a `FacetError` there would be a permanent false alarm.

## 7. Testing

- **Adapter fixture goldens** (ADR 0008): bitmap decode (no bits, single bit,
  several bits, an undefined high bit ignored), detector table (dense, sparse,
  volume-without-occupancy), and each partial-failure case producing exactly the
  right `FacetError` and leaving the other facets intact.
- **Differ table tests** on fixed timestamps: raise, clear, raise+clear in one
  poll, no-event when unchanged, first-poll-raises-everything.
- **Determinism:** an explicit test that raise/clear order is sorted, and that
  report readings are sorted by channel — the gen-1 bug (§2) has a regression
  test, not just a comment.
- **Suspension:** a failed `FaultSet` read emits no `FaultCleared`, and the
  facet survives to the next successful poll. This is the iron rule; it gets a
  named test.
- **Volume semantics:** first poll emits nothing; second poll deltas; a decrease
  reports the current value; a new channel is omitted for one poll.
- **Every guard shown to fail.** For each validation and each invariant above,
  the test must be demonstrated to reject — twice this session a check that
  passed turned out to be incapable of failing.

## 8. Out of scope

- **RSU** (`RSUBroadcastCounters` + `ntcip-rsu`) — its own spec. The catalog has
  no broadcast-*counter* event at all, only `rsu-broadcast-sample.v1` carrying
  `rate_hz` over a window, so that facet defines a shape rather than matching
  one. That deserves its own design conversation.
- **Wire mapping** — Plan 2. This spec deliberately produces domain events with
  no emitter; they will hit the loud-drop path until `wire/openitsv1` lands,
  exactly as `SignalStatus`'s events do today.
- **Per-vendor OID overlays** — deferred by the architecture spec (rule of
  three). Gen-1 modeled an override mechanism and never consumed it; we are not
  repeating that.
- **`DetectorTransition`** (per-detector on/off events) — the catalog has the
  ce-type, but NTCIP polling cannot see sub-poll transitions. That is an
  `EventReader` (hi-res log) concern.
- **Validating the alarm bitmap against hardware** — real work, tracked
  separately (§6).
