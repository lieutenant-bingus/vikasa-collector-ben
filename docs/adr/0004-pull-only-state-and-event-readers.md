# ADR 0004: Pull-only; StateReader vs EventReader split by semantics

**Status:** Accepted (2026-07-12)

## Context
Every device in the cabinet is polled — nothing pushes. But pull transport
≠ snapshot semantics: an ASC status poll returns *state* (diff it against
the previous poll), while an ATSPM high-res log fetch returns *discrete
events* (nothing to diff).

## Decision
Two adapter read interfaces, split by what the data means, not how it
travels: `StateReader.Read → *model.Snapshot` (core diffs consecutive
snapshots via synth) and `EventReader.Fetch → []model.Event` (forwarded
directly to emitters). No push machinery anywhere. A `Commander` capability
is reserved but dormant — v1 is collect-only by decision; commands bolt on
later without breaking any adapter.

## Consequences
The core has no concept of transport at all. Synth logic is written once
against facets. EventReader checkpointing (don't re-emit the same log
window) is deferred to the first log-shaped adapter.

## Alternatives considered
One interface with event-vs-state discrimination inside payloads (rejected:
pushes semantics into every consumer); push/callback sinks (rejected: no
push sources exist; YAGNI).
