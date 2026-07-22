# openits-models emitter + Tier 2 NATS profile (Gen-2 Plan 2)

**Date:** 2026-07-21
**Status:** Approved
**Depends on:** ADR 0002 (wire emitter boundary), ADR 0006 (tenant subjects),
ADR 0007 (collector-owned health), ADR 0009 (configurable subject templates),
openits-models v0.2.2 (first public release line).

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

- Add `github.com/Vikasa2M/openits-models v0.2.2` to `go.mod`. Tagged
  release only — never a `replace` on a checkout (ADR 0002).
- New package `internal/wire/openitsv022`, one package per pinned models
  release, implementing `wire.Emitter`.
- Runner emitter chain becomes `[openitsv022, health]`. Health events keep
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
| `DMSMessageActivationFailed` | `openits.dms.message-activation-failed.v1` | `dms/v1.MessageActivationFailed` |

- `<service>` for the shared fault/mode events is routed from
  `Base.DeviceKind` (`asc` → `signal-control`, `dms` → `dms`) — the field
  exists for exactly this purpose. A shared event with a device kind the
  emitter doesn't know is **not claimed** (loud drop), never guessed.
- `DMSDisplayStateChanged` has no ce-type in v0.2.2 and stays **unclaimed**:
  the existing loud-drop path (metric + warning) handles it. Deferred, not
  silent — see Deferred work.
- Each ce-type carries a constant `ce-dataschema` URL pinned to the
  v0.2.2 schema-registry snapshot revision
  (`https://schemas.open-its.org/<module>/<revision>/`). openits-models
  ships no Go catalog API, so the emitter hard-codes these constants and
  golden tests lock them; a pin bump is a deliberate edit to this one
  package (ADR 0002's S1 scenario).
- Enum and field mapping domain→proto is dumb by design; where the domain
  model is richer than the wire model, the emitter makes an explicit
  map-or-drop choice per field, recorded in the emitter source.

## 3. Envelope: CloudEvents binary mode

The publish path switches from structured-mode JSON bodies to **binary
mode** for all events uniformly: CloudEvents attributes ride as `ce-*`
NATS message headers, the body is the raw encoded payload. One publish
path, no per-emitter mode fork.

- `ce-specversion: 1.0`.
- `ce-type`: catalog-verbatim from the emitter (unchanged rule).
- `ce-id`: stays content-addressed (sha256 over type/source/time/payload) —
  already deterministic, which is what the profile's replay-dedup rule
  requires.
- `ce-time`: RFC 3339 UTC, unchanged.
- `ce-source`: becomes the profile URN
  `urn:openits:<entity-kind>:<region>:<agency>:<unit>:<id>`.
  `entity-kind` = `Base.DeviceKind` for device events, `collector` for
  collector-level events (collector-started). Replaces the current
  `//<agency>/<site>[/<device>]` URI-ref.
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

- **DMS display-state event upstream:** add a display/message-changed
  notification to openits-models (additive minor release), then claim
  `DMSDisplayStateChanged` on the next pin bump. Until then it drops
  loudly.
- **Agency-registry validation** of region/agency/unit tokens.
- **Conformance harness in CI** (above).
