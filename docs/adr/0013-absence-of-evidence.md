# ADR 0013: Absence of evidence is never a state change

**Status:** Accepted (2026-08-17). Records a rule that predates this ADR;
written when its only source, the greenfield architecture spec, was retired.

## Context
Every device in the cabinet is polled — nothing pushes (ADR 0004) — and a
poll can fail. Poll-based collection cannot distinguish "the device says X"
from "the device did not answer." A differ that treats a missing read as a
changed value manufactures events: a fault that never cleared reported as
cleared, a mode that never changed reported as changed. Downstream these are
indistinguishable from real transitions, and the collector is the only place
in the pipeline with enough information to tell them apart — by the time a
snapshot leaves synth, the distinction is gone.

This is not hypothetical. `internal/synth/fault_test.go` carries a comment
recording that an earlier `diffFaults` was a pure set-difference that cleared
every real fault on a failed read, then re-raised them on recovery — a fault
storm manufactured entirely by treating absence as evidence.

## Decision
A failed or absent facet read emits nothing and leaves previous state
untouched. Adapters record `model.FacetError{Kind, Err}` in a snapshot's
`Errors` for a facet they tried and could not read, and simply omit that
facet from `Snapshot.Facets`. `synth.Engine.Apply` only diffs facets present
in `snap.Facets`; a facet that isn't there is never handed to its differ, so
the engine's remembered previous state for that facet survives the poll
unchanged. Absence is never evidence.

## Consequences
Every differ needs a failed-read test, asserting the same shape: a failed
poll emits zero events, and a subsequent recovery poll diffs against the
pre-failure state, not a zero value. Which differs currently have one is a
moving number and deliberately not recorded here — an immutable record must
not carry a count that the next PR falsifies. The live coverage, including
the differs still missing the test, is in
[`docs/reference/invariants.md`](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).

A device that goes silent produces silence, not a storm of false clears —
reachability is reported separately via `DeviceStatusChanged`
(`sdk/model/health.go`), which is where "we cannot see it" belongs. That
split is deliberate: a facet differ answers "did the device's state change,"
never "can we see the device."

Cost: a genuinely-cleared fault during an outage is not reported until the
next successful read. The alternative — reporting it immediately on the
strength of a failed poll — is exactly the fault-storm failure mode this rule
exists to prevent, so the delay is accepted rather than engineered around.

## Alternatives considered
**Treat absence as the zero value** (rejected): it is exactly the
event-manufacturing failure above — a zero-value `SignalStatus` or
`DMSStatus` is indistinguishable from a device that genuinely transitioned
to that state.

**Emit an "unknown" state** (rejected): it puts collector-internal
uncertainty into the ITS catalog's vocabulary, and the catalog has no such
concept for signal mode, fault state, or DMS status. Manufacturing a value
the wire schema was never designed to carry pushes the problem downstream
instead of solving it.
