# ADR 0001: Rebuild the collector greenfield

**Status:** Accepted (2026-07-12)

## Context
The gen-1 collector grew four divergent ingest paths and three inconsistent
organizing axes (drivers by transport, translators by device-type, decoders
by vendor). A 2026-07 restructure (P0–P4a) hardened it and introduced a
vendor×device-kind adapter registry, but its "internal" `Reading` struct
embedded openits-models proto enums directly — so a YANG-driven regeneration
of openits-models broke the entire build. The wire model and the collector's
working model were the same types; upstream schema churn reached every
translator.

## Decision
Rebuild from scratch on branch `gen2` per the 2026-07-12 architecture spec.
Gen-1 code is deleted (git history preserves it) and mined for lessons:
golden determinism via fixed timestamps, sorted iteration for
content-addressed CE ids, per-device serialized SNMP I/O, panic-guarded
long-lived goroutines, and "a failed read must never synthesize a
state-change event."

## Consequences
Short gap with no shippable binary until the Plan 1 spine lands. In
exchange: no dead code shadowing new code, no gen-1 compatibility drag, and
the openits-models dependency re-enters only behind the wire-emitter
boundary (ADR 0002).

## Alternatives considered
Incremental refactor of gen-1 (rejected: the model coupling was load-bearing
everywhere); parallel build in-tree (rejected: two architectures, colliding
`sdk/` paths); new repository (rejected: loses history/doc continuity).
