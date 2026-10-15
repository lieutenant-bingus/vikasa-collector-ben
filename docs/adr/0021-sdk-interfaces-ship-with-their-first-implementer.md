# ADR 0021: SDK interfaces ship with their first implementer

**Status:** Accepted (2026-10-13)
**Amends:** [ADR 0004](0004-pull-only-state-and-event-readers.md) — its
semantic split stands; the two interfaces it declared ahead of any
implementer are removed until one exists.

## Context
ADR 0004 split adapter reads by meaning: `StateReader.Read` returns a
snapshot the core diffs, and `EventReader.Fetch` returns discrete events
forwarded as they are. It also reserved a dormant `Commander`, so that
commanding could "bolt on later without breaking any adapter".

Three months on, `sdk/adapter` exported `EventReader`, `Commander`,
`CapEvents`, `CapCommand` and `sdk/model.Command` (with one `SetPlan`
command), and nothing implemented or called any of them. The runner takes
a `StateReader` only, so an `EventReader` adapter would have compiled,
registered, and never been polled — a trap the known-gaps list recorded and
a tracker issue described as bypassing the runner's DeviceKind stamping.

The SDK is pre-1.0, which is exactly why this matters now. Once v1 ships,
an exported interface is frozen: removing it, or adding a method to it, is a
breaking change. And `Fetch`'s signature is already known to be incomplete.
ADR 0004 itself defers checkpointing ("don't re-emit the same log window")
to the first log-shaped adapter, so the first real implementer would need a
different signature from the one we would be promising.

## Decision
**An SDK interface is declared in the same change as its first
implementer.** `EventReader`, `Commander`, `CapEvents`, `CapCommand`,
`model.Command` and `model.SetPlan` are removed. `StateReader` and
`CapState` remain; the `Capability` bitset stays so the next read path is an
added constant, not a changed type.

**ADR 0004's split is unchanged.** State is diffed and events are
forwarded, and a log-shaped source still gets its own read interface rather
than a flag on `StateReader`. What changes is only *when* that interface is
written: with the adapter that needs it, its checkpointing, and its runner
path, in one reviewable change.

## Consequences
- ADR 0004's "without breaking any adapter" still holds. Go interfaces are
  satisfied implicitly, so adding `EventReader` later changes no existing
  adapter.
- The first log-shaped or commanding adapter carries more work: the
  interface, the runner dispatch, and (for events) checkpointing. That is
  the work the interface could never have done for it anyway.
- The "EventReader has no dispatch path" known gap closes by removal: there
  is no declared-but-unwired read path left.
- This is a breaking change to `sdk/adapter` and `sdk/model`, announced in
  the release notes. No adapter in or out of this repository implemented
  the removed types.

## Alternatives considered
- **Keep them, marked experimental.** It matches ADR 0004 as written, but
  it ships a `Fetch` signature known to lack checkpointing, and a doc
  comment does not stop a third party from implementing it.
- **Remove only the command path.** `Commander` is the most clearly
  speculative, since v1 is collect-only. But `EventReader` has the same
  defect, a guessed signature with no runner path, and keeping it keeps the
  trap.
