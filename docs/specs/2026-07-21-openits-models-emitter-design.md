# openits-models emitter + Tier 2 NATS profile (Gen-2 Plan 2)

**Date:** 2026-07-21
**Status:** Approved — **revised 2026-08-08**, see [Revisions](#revisions)
**Depends on:** ADR 0002 (wire emitter boundary), ADR 0006 (tenant subjects),
ADR 0007 (collector-owned health), ADR 0009 (configurable subject templates),
ADR 0010 (lockstep pin), openits-models at main HEAD.

> This document was written against openits-models v0.2.2. That module has
> moved, and an audit on 2026-08-08 found several items here wrong — most
> importantly the `ce-id` rule in §3, which was reversed. Corrections are
> inline; the Revisions section at the end records what changed and why.

## Goal

Wire the collector's domain events to the released openits-models module so
published events conform to the OpenITS **NATS reference profile (Tier 2)**:
per-event protobuf payloads, CloudEvents binary-mode headers, and the
seven-token subject grammar. Today no emitter claims domain events, so they
drop with a warning; after this plan, signal-control and DMS events reach
JetStream in the form profile consumers expect.

## Scope decision

Full Tier 2 conformance in one plan — emitter, envelope mode, and subject
grammar together. Shipping openits payloads inside the current
structured-JSON envelope and five-token subjects would produce events no
conformant consumer can read, and the envelope/subject work would be redone
immediately after. v0.1.0 of the collector has no deployments, so changing
the default subject grammar and envelope now has zero migration cost.

## 1. Dependency and layout

- Add `github.com/Vikasa2M/openits-models` to `go.mod` at **main HEAD**
  (pseudo-version) — lockstep pre-v1 per ADR 0010, never a `replace` on a
  checkout.
- New package `internal/wire/openits` implementing `wire.Emitter`.
  Unsuffixed: during lockstep exactly one models version is ever compiled
  in. The one-package-per-release split starts at the first tagged pin.
- Runner emitter chain becomes `[openits, health]`. Health events keep
  the collector-owned schema (ADR 0007) and are not claimed by the
  openits emitter.
- The CI boundary lint (only `internal/wire` may transitively import
  openits-models) already enforces the layering for the new package.

## 2. Event mapping

Payloads are the generated protos from
`pkg/proto/openits/{signal_control,dms,common}/v1`, serialized as
per-event protobuf, `ce-datacontenttype: application/protobuf`.

| Domain event (`sdk/model`) | ce-type | Proto message |
|---|---|---|
| `ModeChanged` | `openits.signal-control.mode-changed.v1` | `common/v1.ModeChanged` |
| `PlanChanged` | `openits.signal-control.plan-applied.v1` | `signal_control/v1.PlanApplied` |
| `OperationalStatusReport` | `openits.signal-control.operational-status-report.v1` | `signal_control/v1.OperationalStatusReport` |
| `PreemptionActivated` | `openits.signal-control.preemption-activated.v1` | `signal_control/v1.PreemptionActivated` |
| `PreemptionCleared` | `openits.signal-control.preemption-cleared.v1` | `signal_control/v1.PreemptionCleared` |
| `FaultRaised` | `openits.<service>.fault-raised.v1` | `common/v1.FaultRaised` |
| `FaultCleared` | `openits.<service>.fault-cleared.v1` | `common/v1.FaultCleared` |
| `DetectorReport` | `openits.signal-control.detector-report.v1` | `signal_control/v1.DetectorReport` |
| `DMSControlModeChanged` | `openits.dms.mode-changed.v1` | `common/v1.ModeChanged` |
| `DMSDisplayStateChanged` | `openits.dms.mode-changed.v1` | `common/v1.ModeChanged` |
| `DMSMessageActivationFailed` | `openits.dms.message-activation-failed.v1` | `dms/v1.MessageActivationFailed` |

- `<service>` for the shared fault/mode events is routed from
  `Base.DeviceKind` (`asc` → `signal-control`, `dms` → `dms`) — the field
  exists for exactly this purpose. A shared event with a device kind the
  emitter doesn't know is **not claimed** (loud drop), never guessed.
- `DMSDisplayStateChanged` **is claimed**, on `openits.dms.mode-changed.v1`
  alongside `DMSControlModeChanged`. `openits-dms-types:dms-mode-event-kind`
  is the mode-space for both: its description states it spans dms-control-mode
  *and* the sign-mode display-state (off / blank / test / normal), and the
  domain `DMSDisplayState` maps one-to-one onto those `sign-mode` identities.
  The two events are discriminated by payload — `kind` plus which mode-value
  set `prior`/`current` are drawn from.
- Each ce-type carries a constant `ce-dataschema` URL
  (`https://schemas.open-its.org/<module>/<revision>/`) keyed on the
  **defining events module and its revision** (`openits-<service>-events`),
  never on a base or types module the payload happens to compose — see
  openits-models `docs/04-design-decisions.md`. openits-models ships no Go
  catalog API, so the emitter hard-codes these constants and golden tests
  lock them; a pin bump is a deliberate edit to this one package (ADR
  0002's S1 scenario).
- Enum and field mapping domain→proto is dumb by design; where the domain
  model is richer than the wire model, the emitter makes an explicit
  map-or-drop choice per field, recorded in the emitter source.

### Mandatory leaves the mapping table does not show

`openits-types:event-header` is `uses`d by every mapped notification and
makes three leaves mandatory. Two of them have no source in the domain
model today:

- **`sequence`** — per-source-device monotonic counter, +1 per event in
  emission order, MAY reset to 0 on device restart (so a decrease signals a
  reboot, not a gap; a gap signals loss in transit). The collector holds no
  such state anywhere. The emitter needs per-device counters.
- **`observed-by`** — the device or poller that *observed* an event, when
  that differs from `source-device-id`. Its description names synthesized
  events "a collector infers rather than the source reporting" as the case
  it exists for, which is the collector's entire synth-diffed output: we
  derive mode, plan, and fault transitions by diffing polls rather than
  receiving them. So `observed-by` is the collector's own id, and
  `occurred-at` is the observer's clock — `Snapshot.SampledAt`.
- **`kind`** — a service-specific identityref, e.g.
  `openits-signal-control-types:fault-power-failure`. This is where
  `model.FaultCategory` maps, which corrects the note in `sdk/model/enums.go`
  that the catalog kept category only on the state `Fault` message: it did,
  but `kind` is the fault-classification surface on the event, and it is
  mandatory. Mapping category is required, not optional.

Two further wire shapes to map deliberately rather than by reflex:

- **Mode leaves are identityref strings**, rendered `module:identity-name`,
  not enums. `mode-normal` was collapsed into `mode-free` for signal control
  (NTCIP and signal technicians treat normal and free as one operating mode),
  so `ModeNormal` → `openits-signal-control-types:mode-free`. A `mode-normal`
  identity does exist but it is `openits-dms-types:mode-normal`, a sign-mode;
  do not cross them. **`ModeStandby` has no controller-mode identity at all**
  — the set is coordinated / free / flash / preempt / priority / manual / off.
  That gap needs a decision (map defensibly, drop the axis, or add an identity
  upstream), never a nearest-neighbour guess.
- **`DetectorReportDetector.Occupancy` is a string** (YANG decimal64) against
  the domain's `OccupancyTenths uint16`.

## 3. Envelope: CloudEvents binary mode

The publish path switches from structured-mode JSON bodies to **binary
mode** for all events uniformly: CloudEvents attributes ride as `ce-*`
NATS message headers, the body is the raw encoded payload. One publish
path, no per-emitter mode fork.

- `ce-specversion: 1.0`.
- `ce-type`: catalog-verbatim from the emitter (unchanged rule).
- `ce-id`: **rewritten.** openits-models' `ce-id-spec.md` makes the
  derivation normative:

  ```
  digest = SHA-256( ce-source ‖ ce-type ‖ stable-time ‖ payload-bytes )
  ce-id  = ULID(timestamp = ce-time-ms, randomness = digest[0:10])
  ```

  `‖` is concatenation with a `0x1f` unit separator *between* fields —
  three separators, no trailing one. `stable-time` is `occurred-at` when
  present, else `ce-time`, as RFC 3339 with millisecond precision, UTC `Z`.
  `payload-bytes` is the canonical binary protobuf payload.

  The current `internal/cloudevents.New` emits sha256 **hex** over
  `(type, source, time, data)` with `0x00` separators: wrong field order,
  wrong separator, wrong output encoding. All three change.

  The ULID's leading 48 bits are `ce-time` in milliseconds. That does not
  make it non-deterministic — `ce-time` is an input, not a clock read at
  encode time — so replay dedup is preserved. Do not "restore" a bare
  content hash on the belief that a ULID is random. The published test
  vector is the golden, and openits-models states the reference
  *implementation* of this algorithm is the collector: this repo has to be
  right, not merely self-consistent.
- `ce-time`: RFC 3339 UTC, unchanged.
- `ce-source`: becomes the profile URN
  `urn:openits:<entity-kind>:<region>:<agency>:<unit>:<id>`, replacing the
  current `//<agency>/<site>[/<device>]` URI-ref.

  `entity-kind` is **not** `Base.DeviceKind` — it is a third mapping off
  it, alongside service and the emitter claim: `asc` → `controller`,
  `dms` → `sign`, collector-level events → `collector`. (Read off
  openits-models' conformance mocks, which are the only enumeration of
  entity kinds it publishes.)

  Note that `ce-id` hashes the literal `ce-source` bytes, so the two are
  coupled: any change to source formatting changes every id. openits-models
  currently contradicts itself on segment order — its profile README,
  conformance regex, and every mock put entity-kind first, while
  `ce-id-spec.md`'s test vector puts region first. Resolve that upstream
  before implementing either.
- `ce-dataschema`: set only on `openits.*` events (from the emitter's
  constant table). Health events omit it.
- `ce-datacontenttype`: `application/protobuf` for openits events;
  health keeps `application/json` bodies inside the same binary-mode
  envelope.

## 4. Subjects and config

- Config identity grows `region` and `agency_unit` alongside `agency`.
  `site` stays (cluster naming, health context) but leaves the default
  subject grammar. All identity tokens validate against the existing
  shape rule `^[a-z0-9][a-z0-9-]*$`; agency-registry-backed validation is
  deferred (registry is upstream data with no Go API — shape-check and
  trust config at cabinet scale).
- The ADR 0009 template engine stays. The **default template** becomes the
  profile's seven-token grammar:
  `{prefix}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}`.
- `{device_id}` graduates from reserved-unsupported to a per-event
  placeholder. Device-less events (collector-started) render it as
  `collector`, so every event renders a legal subject.
- No `{version}` token in the default template — the ce-type carries the
  major version, per the profile. `{version}` remains available to
  operator templates.
- Health events ride the same template (`service` token = `health`), as
  today: subjects are operator routing, ce-type is identity.
- This changes default subjects and the derived stream binding. ADR 0009's
  "zero-config compat" consequence is updated to note the Plan 2 reset —
  the pre-1.0, zero-deployment window is the free moment to do it.

## 5. Testing

- Fixture-driven golden tests per mapped event: domain event in → exact
  header set + proto bytes out, following the health emitter's pattern and
  the ADR 0008 bar (no fixtures, no merge).
- Boot validation already renders every `CETypes()` entry through the
  subject template; the new emitter reports its full ce-type list.
- Envelope/subject changes covered by updating the existing
  `internal/cloudevents`, `internal/subject`, and `internal/publish` tests.
- Follow-up (not a blocker): run openits-models' Tier 2 conformance
  harness (`tools/conformance`) against a live collector stream in CI.

## Deferred work

- **Upstream: tier the `ce-id` / `ce-source` rules.** openits-models
  currently states both as if normative for every implementer. They are not
  equally flexible and should not be stated the same way: `ce-source` needs
  *global* agreement (it is naming — consumers parse it to identify devices
  and aggregate across agencies), while `ce-id` needs only *intra-deployment*
  agreement (a consumer deduping on it never reproduces the derivation; only
  redundant observers of one device need a shared algorithm, and they share an
  org and a config). So the ce-id algorithm belongs to the NATS reference
  profile (Tier 2) while the ce-source grammar is closer to Tier 1 identity —
  and the grammar needs a private-use escape for operators outside the agency
  registry, so there is one blessed way to be nonstandard instead of
  open-ended variation. **Blocks §3** on the segment-order contradiction.
- **Agency-registry validation** of region/agency/unit tokens. Depends on the
  private-use escape above: without it, shape-only validation reads as a gap
  to close rather than a deliberate floor.
- **Conformance harness in CI** (above).

## Revisions

**2026-08-08** — audited against openits-models main HEAD (`235e878`,
2026-08-07). What changed in this document and why:

- **Pin and package name.** v0.2.2 → main HEAD, `internal/wire/openitsv022`
  → `internal/wire/openits`, per ADR 0010. The version delta itself is
  minor for our surface — the ce-type catalog gained exactly one entry
  (`openits.cctv.ptz-move-commanded.v1`, which the collector does not emit),
  and nine of the ten mapped proto messages are byte-identical between
  v0.2.2 and HEAD; only `dms/v1.MessageActivationFailed` changed, additively.
  The `feat!:` commits on main are breaking in the YANG sense (cabinet-power
  split, shared schedule primitives, service-family corrections) and do not
  touch what this emitter maps.
- **§3 `ce-id` was reversed.** The original text said the existing
  content-addressed sha256 already satisfied the profile and needed no work.
  openits-models added a normative `ce-id-spec.md` on 2026-08-03, after this
  document was written, mandating SHA-256 → ULID. The current implementation
  is non-conformant on field order, separator, and output encoding.
- **§3 `ce-source` entity-kind was wrong** — it is not a passthrough of
  `Base.DeviceKind`, and the upstream segment-order contradiction now blocks
  implementation.
- **§2 `DMSDisplayStateChanged` was wrongly deferred.** It has a home today;
  the corresponding deferred-work item is removed. This was already true at
  v0.2.2, so it was a misreading rather than drift.
- **§2 gained the mandatory-leaf subsection.** `sequence`, `observed-by`, and
  `kind` are mandatory and were absent from the mapping table; `sequence`
  needs per-device state the collector does not have. Also the mode-identityref
  traps and decimal64-as-string on detector occupancy.
- **Deferred work gained the ce-id/ce-source tiering item.**

Tracked in Linear as MON-184 (parent) and MON-196 (the upstream item).
