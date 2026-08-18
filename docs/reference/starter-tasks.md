# Starter tasks

## The safest first PR in this repo

Eight facet kinds are modeled in `sdk/model`, diffed by a registered differ
in `internal/synth`, golden-tested, and mapped to real ce-types in
`internal/wire/openits/emitter.go`. Only one vendor adapter exists in the
whole repo, `internal/vendors/ntcip/asc.go`, and it produces just three of
those eight facets (`signal-status`, `fault-set`, `detector-samples`). The
other five — `dms-status`, `cctv-status`, `traffic-intervals`,
`zone-incidents`, `zone-intervals` — are fully wired end to end with
nothing anywhere producing them.

That makes a first adapter for one of those five the safest possible way
into this codebase. It touches `internal/vendors/<vendor>/` alone: no
`sdk/model` change (the facet already exists), no `internal/synth` change
(the differ already exists and is tested), no `internal/wire` change (the
ce-type mapping already exists and is golden-tested). You get to learn one
thing at a time — how an adapter reads a device and returns `sdk/model`
types — without simultaneously learning the layering rules that are
hardest to get right on a first PR (ADR 0002's boundary, the
absence-of-evidence rule, wire mapping). Ship the adapter, and events
start flowing through parts of the pipeline that have been sitting ready
and untested-by-production since they were built.

## The five open facets

| Facet kind | Differ | Ce-types it lights up | What a device must provide |
|---|---|---|---|
| `dms-status` | `synth.NewDMSDiffer` (`internal/synth/dms.go`) | `openits.dms.mode-changed.v1` (fires for either a control-mode or display-state change), `openits.dms.message-activation-failed.v1` | `model.DMSStatus` (`sdk/model/dms.go`): control mode, display state (blank vs. showing a message), which memory slot is active, and message-activation status (success/error, with syntax-error detail on failure). A sign with no message-activation reporting can still light up the mode-changed ce-type alone. |
| `cctv-status` | `synth.NewCCTVDiffer` (`internal/synth/cctv.go`) | `openits.cctv.mode-changed.v1`, `openits.cctv.tour-state-changed.v1` | `model.CCTVStatus` (`sdk/model/cctv.go`): who is driving the camera (central/local/central-override/other) and the run state of each configured preset tour (stopped/running/paused). A camera with no tours configured yields an empty `Tours` slice, not an error. |
| `traffic-intervals` | `synth.NewTrafficIntervalDiffer` (`internal/synth/trafficsensor.go`) | `openits.traffic-sensor.traffic-interval-report.v1` | `model.TrafficIntervals` (`sdk/model/trafficsensor.go`): the sensor's own completed-interval start time and duration (not poll timing — the differ uses `IntervalStart` to tell a fresh interval from a re-read), plus per-lane volume, occupancy, optional speed, and optional per-class-bin volumes. |
| `zone-incidents` | `synth.NewZoneIncidentDiffer` (`internal/synth/perception.go`) | `openits.perception.zone-incident-detected.v1`, `.zone-incident-updated.v1`, `.zone-incident-cleared.v1` | `model.ZoneIncidents` (`sdk/model/perception.go`): every incident currently active in the sensor's detection zones, each keyed by the sensor's own stable `IncidentID` (must persist across polls or every poll looks like clear-and-redetect), with type, severity, object class, and optional speed. |
| `zone-intervals` | `synth.NewZoneIntervalDiffer` (`internal/synth/perception.go`) | `openits.perception.zone-interval-report.v1` | `model.ZoneIntervals` (`sdk/model/perception.go`): device-reported interval start/duration plus per-zone crossed-volume, observed-count, occupancy, optional speed, and optional per-object-class counts. |

Two of the five (`zone-incidents`, `zone-intervals`) share one device kind,
`perception` — a device that reads both facets lights up both rows' ce-types
from a single adapter.

**Not required, but available if your device also reports faults:** every
device kind above also has `openits.<kind>.fault-raised.v1` /
`.fault-cleared.v1` ce-types already mapped (see
`internal/wire/openits/emitter.go`'s `ceTypeFor`). Producing those needs
your adapter to also emit the pre-existing `fault-set` facet
(`model.FaultSet`, consumed by the already-registered `synth.NewFaultDiffer`)
— genuinely optional, and a good second PR rather than a requirement of the
first.

Before writing an adapter, read
[`docs/reference/test-requirements.md`](test-requirements.md)'s "A new
adapter" checklist. The step-by-step workflow — reader contract,
registration, connection parsing, fixtures — is
[`docs/how-to/add-a-vendor-adapter.md`](../how-to/add-a-vendor-adapter.md),
the canonical guide; `.claude/skills/add-vendor-adapter/` is the same
workflow in terse checklist form for an agent. If you have never seen this
repo before, [`docs/tutorial/build-your-first-adapter.md`](../tutorial/build-your-first-adapter.md)
walks a throwaway adapter end to end first.

## The reference implementation

`internal/vendors/ntcip/asc.go` is the one adapter in the repo, and is
designated the reference to read before writing your own — not because its
fixtures meet the bar (they don't; see
[`test-requirements.md`](test-requirements.md#a-new-adapter)'s noted gap),
but for three things its `Read` implementation demonstrates cleanly:

1. **Per-facet failure isolation.** `Read` calls `readSignalStatus`,
   `readFaultSet`, and `readDetectors` unconditionally and independently —
   a comment on `Read` states the rule directly: "Each facet is an
   independent failure domain: one unanswered OID must never suppress a
   facet that WAS readable." One facet's OID going unanswered produces a
   `model.FacetError` for that facet alone; the other two still populate
   normally.
2. **The absence-of-evidence rule in practice.** `readFaultSet` and
   `readSignalStatus` both comment on the distinction between "the device
   told us nothing" (append a `FacetError`, leave prior state alone) and
   "the device told us zero/empty" (a real facet value — e.g. zero alarm
   bits is a healthy controller, not a read failure). `readDetectors`
   applies the same distinction to a third case: the controller answering
   "I have zero detectors" is an empty facet, not an error, and is coded
   separately from "the detector table read failed."
3. **The synthesized-index table read that avoids ~510 round trips.**
   `readDetectors`'s doc comment explains the choice directly: rather than
   walking the detector table one OID at a time (`GetNext`/`BulkWalk`,
   ~510 round trips for a full 255-channel table at 2 OIDs/channel), it
   builds the full list of synthesized indexed OIDs up front and issues
   them as a single `Get` call, which the transport chunks into batches of
   16 varbinds per round trip — of order 32 round trips instead.

One practical note on following its shape: some older docs describe the
adapter tree as `internal/vendors/<vendor>/<kind>/` (a subdirectory per
device kind). That is not what `asc.go` actually does —
`find internal/vendors -type f` shows `internal/vendors/ntcip/asc.go`,
`register.go`, and `asc_test.go` sitting directly in `internal/vendors/ntcip/`,
with no `asc/` subdirectory. Lay your new adapter out the way `ntcip` is
actually laid out — `internal/vendors/<vendor>/<kind>.go` plus
`<kind>_test.go` and a shared `register.go` — not the subdirectory shape.
