# Vendor-Adapter Architecture — Design

- **Date:** 2026-07-10
- **Status:** Approved design, pre-implementation
- **Scope:** Restructure the OpenITS edge collector around a vendor-agnostic core plus
  per-vendor × per-device-type adapters, and converge the four divergent ingest paths
  onto a single emission Sink. Q-Free is the first real vendor onboarded on the new model.

## 1. Motivation

The collector polls ITS cabinet devices (ASC, RSU, DMS, ESS, ramp meters, traffic sensors)
and publishes OpenITS CloudEvents to the cabinet's local NATS JetStream buffer. The core
poll → translate → diff → publish pipeline is sound, but the codebase has drifted into
**three inconsistent organizing axes** and **four divergent ingest paths**:

- **Drivers** are keyed by *transport* — `snmp`, `gnmi`.
- **Translators** are keyed by *device type* — `signalcontrol`, `rsu` (via `pb.DeviceType`).
- **ATSPM decoders** are keyed by *vendor* — `econolite`, `mccain`.
- Emission happens four different ways: the SNMP scheduler (`synth.Diff` → `PublishSynth`),
  the ATSPM `Runner`, the TrafficVision `Runner`, and the heartbeat publisher — with the
  `Publisher` carrying both a clean generic `PublishSynth` and ~50 typed façade methods.

We are about to onboard Q-Free (and other vendors) for real. We need a repeatable
"add a vendor" path that a new engineer can follow without touching the core, and we want
to fix the structural mess as the forcing function does its work.

The reality that shapes the axis choice: a device type must cover the **NTCIP standard**
*and* a large amount of **vendor-specific behavior delivered over both SNMP (enterprise OIDs)
and vendor APIs**. Neither axis alone suffices. The resolution: the **device-type axis owns
the standard/contract**, the **vendor axis owns the adapter (the onboarding unit)**, and they
compose.

## 2. Goals / Non-goals

**Goals**

- One vendor-agnostic core: transport clients, the normalized `Reading` model, `synth`,
  and a single emission Sink + event catalog.
- A device-type standard layer (`ntcip/`) of reusable decode building blocks.
- Per-vendor × per-device-type adapters under `vendors/<vendor>/<device_kind>/`, registered
  as `<vendor>-<device_kind>`, that compose transport + standard base + vendor deltas.
- Converge the four ingest paths onto one driving loop and one Sink.
- Config selects an adapter by `vendor` + `device_kind`; config/inventory load becomes the
  trust boundary that validates a device against the registered adapter set.
- Q-Free ASC/RSU built on the new model as proof.
- Land incrementally, behavior-preserving, guarded by characterization tests.

**Non-goals**

- No change to the OpenITS event model / subject grammar / CloudEvents envelope semantics.
- No change to the NATS topology (cabinet `OPENITS_BUFFER` → regional `OPENITS_EVENTS`).
- Not a rewrite: existing translators/decoders are *reframed* as adapters, not rewritten.
- Removing dead central-service config (`CentralConfig`, ClickHouse, Valkey) is welcome
  cleanup but tracked separately from this restructure.

## 3. Package layout

```
internal/
  core/
    transport/        snmp, http, gnmi clients (connection mgmt, circuit breaker, in-flight)
    reading/          the normalized Reading snapshot model
    synth/            Diff(prev,curr) -> events   (logic unchanged, now core-owned)
    emit/             Sink + event catalog + generic envelope construction (collapsed New())
    schedule/         the ONE driving loop (snapshot mode + producer mode)
  ntcip/              device-type STANDARD, reusable across vendors
    asc/              NTCIP-1202 OID tables + Decode(pdus) -> fills Reading
    rsu/              NTCIP-1218 …
    dms/  ess/  ramp/
  vendors/
    ntcip/asc/        generic standard-only adapter (the compat target, see §7)
    ntcip/rsu/
    qfree/asc/        transport + ntcip/asc + Q-Free enterprise deltas + commands (P4)
    qfree/rsu/
    econolite/asc/    existing ATSPM decoder, reframed as an EventProducer adapter
    mccain/asc/
    trafficvision/traffic-sensor/   existing TV decoder, as an EventProducer adapter
    trafficvision/perception/
  adapter/            the Adapter interfaces + registry (keyed "<vendor>-<device_kind>")
```

**Key structural insight — `ntcip` is itself a vendor.** A generic, standard-only adapter
lives at `vendors/ntcip/asc` and uses only the `ntcip/asc` base with zero vendor delta.
`vendors/qfree/asc` = the same base + Q-Free's enterprise OIDs and quirks. This encodes
"must cover NTCIP" structurally and gives existing generic-SNMP devices a natural home
(the compat target).

## 4. Adapter interface + capability model

An adapter is one vendor × one device_kind. It advertises capabilities so the core knows
how to drive it. Capabilities are a bitset, so a single adapter may be **both** a snapshot
reader and an event producer (e.g. a Q-Free ASC polled over SNMP for status *and* pulling
HR logs over an API).

```go
// adapter/adapter.go
type Capability uint8
const (
    CapSnapshot Capability = 1 << iota  // Read() -> Reading; core runs synth.Diff + emit
    CapProducer                          // Produce() emits events straight to the Sink
    CapCommand                           // Command() writes to the device (Phase 4)
)

type Descriptor struct {
    Vendor     string      // "qfree"
    DeviceKind string      // "asc"   → registry key "qfree-asc"
    Caps       Capability
}

type Adapter interface {
    Descriptor() Descriptor
    Close() error
}

// Poll -> normalized snapshot; core runs synth.Diff + emit.  (SNMP poll devices)
type SnapshotReader interface {
    Adapter
    Read(ctx context.Context) (*reading.Reading, error)
}

// Emit events straight to the Sink.  (ATSPM logs, TrafficVision API — streaming/log)
type EventProducer interface {
    Adapter
    Produce(ctx context.Context, sink emit.Sink) error
}

// OpenITS command -> device write, with vendor-owned safety + OID whitelist.  (Phase 4)
type Commander interface {
    Adapter
    Command(ctx context.Context, cmd *pb.DeviceCommand) (Result, error)
}
```

**Composition example.** `vendors/qfree/asc.Read()`:
1. builds `ntcip/asc.OIDs ∪ qfreeEnterpriseOIDs`,
2. issues one `core/transport/snmp` Get,
3. calls `ntcip/asc.Decode(pdus)` to fill standard `Reading` fields, then Q-Free-specific
   decode for the enterprise delta.

`vendors/econolite/asc` implements `EventProducer` instead — it is already a vendor-keyed
decoder today, so it is a near drop-in.

**Registration.** Each vendor package exposes `RegisterTo(reg *adapter.Registry)` wiring a
factory under `Descriptor.Vendor + "-" + Descriptor.DeviceKind`. Wiring is explicit in
`main` (no `init()` side effects), matching the existing translator/driver registry pattern.

**Command locality.** Commands live in the same adapter that reads the device. This is the
structural fix for the current dead `ServiceHandler.DeviceType()` cross-check: nothing can
command a device by a kind no adapter claims, and the read-side OID map and write-side OID
whitelist are co-located per vendor.

## 5. Ingest-path convergence

Today: the scheduler, the ATSPM `Runner`, the TrafficVision `Runner`, and the heartbeat
publisher are four separate drivers with three emission styles. After the refactor there is
one `core/schedule` loop:

- adapter is `SnapshotReader` → poll at `poll_interval` → `synth.Diff` → `Sink`
  (today's scheduler path; the prevRead/firstUnackedAt map leak on device removal and the
  missing panic recovery are fixed in passing).
- adapter is `EventProducer` → run `Produce` (interval or streaming) → `Sink`
  (subsumes both `Runner`s).
- heartbeat stays core (it describes the poller process, not a device).

Everything emits through one `Sink`, so the Publisher's ~50 typed façade methods collapse
behind a single generic `emit.New(ceType, …)` constructor (the `eventName` is derived from
the ce-type, not hand-typed), removing ~300 lines of envelope + publisher boilerplate and
the constant-vs-eventName drift class.

## 6. Config schema

```yaml
devices:
  - id: asc-001
    vendor: qfree
    device_kind: asc          # -> adapter "qfree-asc"
    poll_interval: 10s
    connection:               # opaque to core; the adapter parses + validates it
      snmp: { host: 10.0.0.1, port: 161, community: public, version: 2c }
      # api: { url: …, bearer_token_env: QFREE_ASC_001_TOKEN }   # dual-transport is allowed
```

- Field names: `vendor` / `device_kind`. `device_kind` already drives the metric label and
  fault routing, so it is reused rather than renamed.
- `connection` holds per-transport sub-blocks (`snmp`, `api`, `sftp`), because a single
  adapter may need several. The core treats it as opaque; the adapter validates it.
- Secrets (community strings, bearer tokens, SFTP passwords) stay env-sourced via
  `*_env` references or the existing env-only YAML tags — never inline in inventory.

## 7. Validation, compatibility, inventory mapping

**Validation is the trust boundary.** At config/inventory load, before any device goroutine
starts: `(vendor, device_kind)` must resolve to a registered adapter, and the adapter
validates its own `connection` (host present, port in range, `poll_interval` sane). This
closes the current gap where inventory-sourced devices bypass `DeviceConfig.Validate()` and
a zero `poll_interval` reaches `time.NewTicker(0)` and panics.

**Compatibility shim.** The legacy shape `driver_type: snmp` + `device_kind: asc` (with
`device_kind` inside `driver_config`) maps to `vendor: ntcip`, `device_kind: asc` → the
`ntcip-asc` generic adapter. Existing configs keep working with zero edits through the
phase-in; the shim is removed once callers migrate.

**Inventory mapping.** `embedded`, `yaml`, and `infrahub` all project into
`{vendor, device_kind, connection}`. Infrahub's manufacturer field maps to `vendor`, so
onboarding a Q-Free cabinet is a data change, not a code change.

## 8. Phasing

Nothing is big-bang; each phase is independently shippable.

- **P0 — hardening (behavior-preserving, independent of the restructure):** `safeGo` panic
  recovery on every long-lived goroutine; guard the heartbeat `Stop` / ratelimit `Close`
  double-close races; validate inventory `poll_interval` before `NewTicker`.
- **P1 — core extraction:** carve out `core/{transport,reading,synth,emit,schedule}` and the
  generic `emit.New`; wrap today's SNMP translators as `ntcip/asc` + `ntcip/rsu`
  `SnapshotReader` adapters behind the compat shim. Behavior-identical; guarded by golden
  tests (§9). This is the risky-but-mechanical move.
- **P2 — reframe existing vendors:** ATSPM econolite/mccain and TrafficVision become
  `EventProducer` adapters; the two `Runner`s delete.
- **P3 — Q-Free (the forcing function):** build `qfree/asc` (+ `rsu`) read side on the new
  base; prove a real vendor onboards cleanly.
- **P4 — commands into adapters:** migrate signalcontrol/rsu command handlers into
  `Commander`, enforcing the vendor type-check and moving execution + ack retries off the
  serial NATS callback.

## 9. Testing

- **Characterization/golden tests** are the P1–P2 safety net: capture the current
  `(Reading, emitted events, subjects, ce-ids)` for representative SNMP / ATSPM / TrafficVision
  inputs *before* moving code; assert byte-identical output after. The synthetic source and
  synthetic decoder provide deterministic fixtures.
- **Pre-refactor behavior fix:** the ce-id is currently non-deterministic because
  `DetectorReport` samples are appended in Go map-iteration order. Sort samples by channel
  *first* so goldens are stable; this is an intentional, called-out behavior change that must
  land before P1.
- **Per-adapter table tests:** OID → `Reading` field mapping, and (P4) command → SET mapping
  with the vendor OID whitelist.
- **Per-capability integration test** against the synthetic source: one SnapshotReader, one
  EventProducer, driven through `core/schedule` to the Sink.

## 10. Decisions locked

- Approach A: vendor-agnostic core + device-type `ntcip` standard layer + `vendors/<vendor>/<device_kind>` adapters.
- Config Option 1: explicit `vendor` + `device_kind`, adapter-parsed `connection` with
  per-transport sub-blocks.
- Field names `vendor` / `device_kind`.
- Capabilities are a bitset; snapshot + producer may coexist on one adapter.
- Adapters own read *and* command; the command migration is phased last (P4).
- `ntcip` is modeled as a vendor (the generic standard-only adapter and compat target).

## 11. Open items to confirm during planning

- Directory naming: `vendors/` vs `adapters/`, `ntcip/` vs `standard/` (cosmetic; default as written).
- Whether any real near-term device needs snapshot + producer simultaneously (the bitset
  supports it; confirm a concrete case exists before optimizing the driving loop for it).
- Exact `connection` sub-block schema per transport (finalized per-adapter during P1/P3).
