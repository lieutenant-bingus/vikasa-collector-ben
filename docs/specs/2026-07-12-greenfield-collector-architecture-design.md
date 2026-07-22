# Greenfield Collector Architecture — Design

**Date:** 2026-07-12
**Status:** Approved design, pending implementation plan
**Supersedes:** `2026-07-10-vendor-adapter-architecture-design.md` (the gen-1
restructure spec). This is a from-scratch design; existing collector code is
reference material, not a constraint.

## 1. Purpose

Build an open-source edge collector that:

1. Collects data from local ITS cabinet devices across many vendors and
   transports (SNMP/NTCIP, vendor REST APIs, whatever comes next).
2. Normalizes it into a collector-owned domain model.
3. Emits it to the cabinet's local NATS JetStream as CloudEvents whose
   payloads are openits-models protos of the catalog version chosen at boot.

The collector's only concern is local-device → local-JetStream. Upstream
replication, storage, and consumption are other components' jobs.

### Survival scenarios (the "don't get cornered" requirements)

The architecture must make each of these a one-package problem, never an
every-adapter problem:

- **S1 — Regen/rename churn:** openits-models regenerates (YANG toolchain)
  and renames/renumbers/restructures types.
- **S2 — Coexisting wire versions:** different deployments pinned to
  different models releases at the same time.
- **S3 — Wholesale model replacement:** openits-models itself is someday
  replaced by a different schema source.

Old-bytes replay safety (historical JetStream decode) is explicitly a
downstream concern, not the collector's.

### Fixed points

- Wire payloads are openits-models protos; openits-models publishes tagged
  semver releases (breaking changes = new major).
- Local bus is NATS JetStream; events ride in CloudEvents envelopes.
- Deployment: single Go binary per cabinet on edge hardware; WAN may drop
  (local JetStream is the durability story).
- All cabinet devices are **pull** — the collector polls; nothing pushes.
- The core is open source; adapters will be contributed by others over time.

## 2. Architecture overview

Three stages, two hard boundaries:

```
[device] ──transport──▶ ADAPTER ──sdk/model──▶ CORE ──wire emitter──▶ CloudEvent ──▶ local JetStream
          SNMP/REST/            (collect &          (synth/diff,      (catalog version
          gRPC/??               normalize)           health, dedup)     chosen at boot)
```

**The dependency rule (enforced by CI lint):** `sdk/` and adapters never
import openits-models. Only `internal/wire/` does.

- **Adapters** own transport entirely. Their sole obligation is to return
  `sdk/model` types. Whether data arrived by SNMP walk, REST poll, or CSV
  fetch never crosses the boundary; the core has no concept of transport.
- **The core** schedules polls, diffs state into domain events, manages
  health, and publishes. It is written once against `sdk/model` and never
  changes for a new vendor or a new models version.
- **Wire emitters** (one package per pinned models release) map domain
  events to `(proto payload, ce-type)`. S1 = edit one emitter. S2 = ship
  two emitter packages, config picks one. S3 = write a new emitter family.

### Authorship model (Telegraf-shaped)

Adapters are compiled in-tree via registration against a small, stable,
semver-disciplined `sdk/`. Community contribution = a PR adding
`internal/vendors/<vendor>/<kind>/` + fixtures. A future out-of-tree story
(subprocess adapter bridge) slots in as just another adapter — no
rearchitecture. Go runtime plugins are ruled out (toolchain-match fragility).

## 3. Repository layout

Single Go module (splitting `sdk/` into its own module is deferred until
out-of-tree adapters are real; until then it's dev friction with no payoff).

```
sdk/                          # public, semver-stable contract (the open-source surface)
  model/                      # domain model: facets, events, health, commands, enums
  adapter/                    # interfaces, Descriptor, Capability, Registry
  transport/
    snmp/                     # optional helper libs adapters MAY use
    http/                     # (serialized I/O, circuit breaker, auth/backoff)
    ...                       # plus test fakes for fixture-driven adapter tests

internal/                     # core engine — not a contract, free to change
  wire/
    emitter.go                # Emitter interface: domain event → (payload, ce-type)
    openitsv1/                # mapping for the first pinned models release
    health/                   # collector-owned health schema encoder
  runner/                     # per-device poll runners (scheduler)
  synth/                      # facet differs: consecutive Snapshots → domain events
  cloudevents/                # envelope, content-addressed ce-id, subject builder
  publish/                    # JetStream publisher, stream provisioning
  config/                     # load + fail-fast validation (trust boundary)

internal/vendors/<vendor>/<kind>/   # in-tree adapters; import ONLY sdk/*
cmd/collector/                # main: registry wiring + boot
```

openits-models is a normal tagged dependency (no `replace` on a moving
checkout, ever again). Each `wire/openitsvN` package pins the major it maps.
An upstream regen cannot break the build; it sits ignored until an emitter
is written or updated for it.

## 4. The domain model (`sdk/model`)

Collector-owned types; no proto imports; collector-owned enums
(`model.ControllerMode`, `model.FaultSeverity`, `model.FaultCategory`, …).
The domain model may be richer than any given wire version — upstream schema
gaps are emitter decisions (map-or-drop), never collection blockers.

### State facets

A snapshot is a header plus typed facets — no god-struct:

```go
type Snapshot struct {
    DeviceID  string
    SampledAt time.Time
    Facets    []Facet       // typed structs implementing Facet
    Errors    []FacetError  // facets the adapter tried and failed to read
}

type Facet interface{ FacetKind() Kind }
```

Initial facets: `SignalStatus` (mode, conflict flash, active plan,
preemption), `FaultSet`, `DetectorSamples`, `RSUBroadcastCounters`. A new
device kind (DMS, ESS, camera…) adds a facet type + differ + emitter mapping
without touching existing types. Facet families anchor loosely to the
9-service taxonomy the models catalog settled on (signal-control, rsu, dms,
ess, perception, ramp-metering, traffic-sensor, reversible-lane, gateway).

### Domain events

What the core publishes: typed events (`PlanChanged`, `FaultRaised`,
`PreemptionActivated`, `DetectorReport`, `OperationalStatusReport`, …)
produced by synth from consecutive Snapshots, or returned directly by
EventReader adapters. Domain events are the emitters' only input.

### Health events (collector-owned wire schema)

Device reachability, poll success/failure, and collector self-health are
domain events encoded by a small **collector-owned versioned schema**
(`openits-collector.health.*.v1` ce-types), fully outside openits-models by
decision. Documented in the collector's AsyncAPI alongside the catalog
events. Health can therefore never be hostage to upstream schema churn.

### Commands (reserved seam, dormant in v1)

`model.Command` variants and the `Commander` adapter interface are designed
and reserved (capability bit exists), but v1 ships collect-only: no
dispatcher, no safety-validation subsystem. Bolting commands on later breaks
no adapter.

### Governance rails

- Facets and events are per-device-**kind**, never per-vendor. Vendor extras
  go through kind-level design review or a constrained
  `Attributes map[string]string` escape hatch that emitters map or drop
  explicitly.
- Synth is extensible by facet type (registered differs); core diff logic
  never grows vendor knowledge.

## 5. The adapter SDK (`sdk/adapter`)

```go
type Capability uint8
const (
    CapState   Capability = 1 << iota // StateReader: snapshot; core diffs
    CapEvents                         // EventReader: pull returns discrete events
    CapCommand                        // Commander: reserved, dormant in v1
)

type Descriptor struct {
    Vendor     string // "econolite", "qfree", "ntcip" (generic standards-only)
    DeviceKind string // "asc", "rsu", "dms", ...
    Caps       Capability
}
// Registry key: "<vendor>-<device_kind>"

type Adapter interface {
    Descriptor() Descriptor
    Close() error
}

type StateReader interface {          // state semantics: poll → snapshot, core diffs
    Adapter
    Read(ctx context.Context) (*model.Snapshot, error)
}

type EventReader interface {          // event semantics: poll → discrete events
    Adapter                            // (log fetchers, e.g. ATSPM hi-res logs);
    Fetch(ctx context.Context) ([]model.Event, error) // still PULL — no push machinery
}
```

Everything is pull. `StateReader` vs `EventReader` distinguishes *semantics*
(state to be diffed vs events to be forwarded), not transport. `ntcip` is
modeled as a vendor: the generic standards-only implementation and the
compatibility target. Shared vendor-family code (an NTCIP base with an
OID-overlay table, a REST-polling helper) is ordinary library reuse inside
adapters, invisible to the architecture — deliberately **not** designed in
this spec (rule of three; revisit after ~3 NTCIP-variant adapters exist).

Factory signature: `func(deviceID string, conn map[string]any) (Adapter, error)`
— the `connection` block is opaque to the core and parsed by the adapter alone.

## 6. Core pipeline

### Boot

1. Load config; validate every device's `vendor`/`device_kind` against the
   registry; refuse to start on unknown adapter, malformed connection block,
   or unknown catalog version (fail-fast trust boundary).
2. Instantiate the single wire emitter matching `model_version`.
3. Ensure local JetStream streams exist (bind `openits.<agency>.<site>.>`).
4. Start one runner per device.

### Config

```yaml
agency: metro-atlanta          # tenant identity, stamped on every event
site: cabinet-042
model_version: openits/v1      # selects the emitter at boot — the ONLY place a
                               # models version appears; one version per instance,
                               # coexistence (S2) happens across the fleet
devices:
  - id: asc-main-and-5th
    vendor: econolite          # + device_kind picks adapter "econolite-asc"
    device_kind: asc
    poll_interval: 1s
    connection:                # opaque to core; adapter-parsed
      snmp: { address: 10.0.0.12:161, community: public }
```

### Poll path

Per-device runner goroutine: jittered interval, per-poll timeout,
panic-guarded, transport I/O serialized per device. `Read` → `Snapshot` →
synth (per-facet registered differs; first poll yields current-state
events) → emitter → CloudEvents envelope → publish. EventReader adapters
skip synth and rejoin at the emitter. One road after the ramps.

### Envelope, subjects, versioning

- **CE `type`** = catalog ce-type verbatim (`openits.signal-control.fault-raised.v1`)
  — schema identity, matches the models repo's generated AsyncAPI exactly.
  Note the catalog versions **per event** (`.v1` suffix per ce-type);
  `model_version` pins a *catalog snapshot* (a models release), which carries
  each event's version.
- **NATS subject** = tenant-scoped: `openits.<agency>.<site>.<service>.<event>.v<n>`
  (the ce-type with agency/site spliced after the prefix). Documented in
  AsyncAPI 3 as a parameterized address.
- **CE `source`** = `//<agency>/<site>/<device-id>` — device identity lives
  here, not in the subject (subject-per-device explodes cardinality for no
  routing value).
- **CE `id`** = content-addressed hash (payload + occurred-at) so JetStream
  dedup survives collector restarts. All hashed iteration is order-sorted —
  determinism is a construction-time requirement, not a fix.
- Health events use collector-owned ce-types (`openits-collector.health.*.v1`)
  on the same tenant-scoped subject scheme.

### Publish

Local JetStream is same-box: publish is must-succeed with bounded retry and
backpressure into the runner — never unbounded in-process buffering. WAN
outages are invisible here; upstream replication is out of scope.

## 7. Error handling

- **Runner isolation:** one sick device never stalls the cabinet (own
  goroutine, panic guard, timeout, serialized transport I/O).
- **Partial reads are first-class:** Snapshots carry `FacetError`s. Iron
  rule for synth: **absence of evidence is never a state change** — a failed
  read suspends diffing for that facet ("unknown"); it must never emit
  fault-cleared/zeroed reports. Read failures become health events with
  backoff/circuit-breaking.
- **Unmappable domain events:** emitter drops loudly (metric + log), never
  silently. Health events structurally can't hit this (own schema).
- **Boot fails fast;** runtime metrics keep bounded label cardinality
  (device_id acceptable; nothing unbounded).

## 8. Testing strategy

The contribution bar that keeps a zoo of community adapters maintainable:

- **Adapters — fixture-golden:** recorded raw transport responses (SNMP walk
  dump, API JSON) → expected Snapshot. `sdk/transport` ships fakes; **an
  adapter PR without fixtures does not merge.** No hardware needed to review.
- **Synth — table-driven per differ,** fixed timestamps (no clock seam
  needed; construct Snapshots with fixed SampledAt).
- **Emitters — two mechanical guards:** (1) golden: domain event → exact
  proto bytes + ce-type; (2) **catalog conformance:** every ce-type an
  emitter can produce must exist in the pinned models release's
  `asyncapi.yaml` — collector↔models drift is a CI failure, not a production
  surprise.
- **Subjects — byte-literal golden** for the tenant-splice rule.
- **End-to-end:** in-process nats-server; config + fixture adapter → assert
  events on `openits.<agency>.<site>.>`.
- **Boundary lint in CI:** `sdk/` and `internal/vendors/` must not import
  openits-models; only `internal/wire/` may.

## 9. Build order

Thin vertical slice first; every step ends with events observable on a
subject.

1. `sdk/model` + `sdk/adapter` + boundary lint — the contract.
2. Core spine with **one facet** (`SignalStatus`) + one adapter
   (`ntcip-asc` on fixtures) + health events + envelope + publisher.
3. `wire/openitsv1` pinned to the first tagged models release + catalog
   conformance test.
4. Remaining initial facets (`FaultSet`, `DetectorSamples`,
   `RSUBroadcastCounters`) + `ntcip-rsu`.
5. Real vendors one at a time (econolite, qfree, …) — each now just
   adapter + fixtures.
6. `EventReader` path when the first log-shaped source lands (ATSPM-style).

Deferred by decision: NTCIP OID-overlay mechanism (after ~3 variant
adapters), out-of-tree/subprocess adapters, command dispatch + safety
validation (seam reserved), `sdk/` module split.

## 10. Decision log

| Decision | Choice |
|---|---|
| Scope of conversation | Full architecture (model insulation + adapter design + vendor scale), greenfield — existing code is not a constraint |
| Survival scenarios | S1 regen churn, S2 coexisting versions, S3 wholesale replacement; replay safety excluded |
| Vendor scale | Broad device zoo (10+ vendors, many kinds, diverse transports) |
| Authorship | Open-source core; stable SDK + in-tree contributed adapters (Telegraf model); out-of-tree later via subprocess bridge |
| openits-models consumption | Tagged semver releases; per-emitter pinning; no moving-HEAD `replace` |
| Approach | A — collector-owned domain model + versioned wire emitters (B "pin harder" and C "declarative engine" rejected; C survives as a possible later overlay library) |
| Wire-version scope | One catalog version per collector instance, chosen at boot; fleet-level coexistence |
| Push sources | None exist — everything is pull; `StateReader`/`EventReader` split by semantics, no push machinery |
| Subjects | Tenant-scoped `openits.<agency>.<site>.<service>.<event>.v<n>`; CE type = catalog string verbatim; device in CE source |
| Health | Fully collector-owned versioned schema (`openits-collector.health.*.v1`), outside openits-models |
| Commands | Collect-only v1; `Commander` capability + `model.Command` types reserved as a dormant seam |
