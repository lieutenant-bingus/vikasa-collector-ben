# ADR 0005: One catalog version per collector instance

**Status:** Accepted (2026-07-12)

## Context
openits-models versions events *per ce-type* (`openits.<service>.<event>.v1`)
and publishes catalog snapshots per release. Deployments migrate at
different speeds (S2 in ADR 0002).

## Decision
Each collector instance selects exactly one catalog version at boot
(`model_version` in config), instantiating one wire emitter. Version
coexistence happens across the fleet — different cabinets pinned to
different releases — never inside one instance.

## Consequences
No per-event fan-out, no double publish volume, no version routing inside
the collector. A cabinet migrates by config change + restart. If a true
dual-emit migration window is ever required, it's a new ADR (publisher
fan-out), not a rearchitecture.

## Alternatives considered
Dual-emit per instance (rejected: 2x volume and subject ambiguity for a
window nobody has needed yet); per-stream version routing (rejected:
complexity without a driving consumer).
