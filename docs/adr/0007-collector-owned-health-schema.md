# ADR 0007: Collector-owned health event schema

**Status:** Accepted (2026-07-12)

## Context
Device reachability, poll failures, and collector self-health must be
reportable even when — especially when — the wire model is in flux. Gen-1's
poll heartbeat lost its proto home for a period when upstream regeneration
dropped `OperationalStatus`.

## Decision
Health events use a small collector-owned versioned schema with its own
ce-type namespace (`openits-collector.health.*.v1`), JSON-encoded,
documented in the collector's AsyncAPI. They ride the same tenant-scoped
subject scheme but never pass through the openits-models emitter.

## Consequences
Health can never be hostage to upstream schema churn, and Plan 1's spine
runs end-to-end with zero openits-models dependency. Downstream consumers
accept a second (tiny) schema source. If openits-models later models
equivalent events, mapping them is an optional emitter addition, not a
migration.

## Alternatives considered
Catalog-first with own-schema fallback (rejected by decision: one owner for
all health semantics beats a split); catalog-only (rejected: blocks new
health signals on upstream YANG work).
