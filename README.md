# vikasa-collector

Open-source edge collector for ITS cabinets: polls local devices (signal
controllers, RSUs, …) through vendor adapters, normalizes readings into a
collector-owned domain model, and publishes CloudEvents to the cabinet's
local NATS JetStream using versioned openits-models payloads.

## Architecture in one diagram

```
[device] ─transport─▶ ADAPTER ─sdk/model─▶ CORE ─wire emitter─▶ CloudEvent ─▶ local JetStream
```

- **Adapters** (`internal/vendors/<vendor>/<kind>/`) own transport
  entirely and return only `sdk/model` types.
- **The core** diffs snapshots into domain events, tracks device health,
  and publishes on operator-configurable subjects (ADR 0009) — by default
  `openits.<agency>.<site>.<service>.<event>.v1`.
- **Wire emitters** (`internal/wire/`) are the only code that knows
  openits-models. One package per pinned models release.

Why it's built this way: see `docs/adr/`. Full design:
`docs/specs/2026-07-12-greenfield-collector-architecture-design.md`.

## Status

Gen-2 rebuild in progress. Working today: `ntcip-asc` adapter (fixtures +
live SNMP), signal-status synth, collector-owned health events end-to-end
to JetStream. Not yet wired: openits-models emitter (Plan 2), additional
facets and vendors (Plan 3+).

Because only the health emitter is wired, domain events (status reports,
plan changes, …) reach the emitter chain, find no claimant, and are dropped
with a warning. That is expected until Plan 2 lands.

## Run

```bash
make check                             # vet + tests + boundary lint
go run ./cmd/collector -config collector.yaml
```

## Contributing an adapter

Implement `sdk/adapter.StateReader` (or `EventReader`) returning
`sdk/model` types, register a `Descriptor{Vendor, DeviceKind}`, and ship
recorded fixtures with golden tests — **no fixtures, no merge** (ADR 0008).
Adapters must not import openits-models (CI-enforced, ADR 0002).

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full contribution guide.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
Security reports: see [SECURITY.md](SECURITY.md).
