# ADR 0016: The collector is a transitional shim; synthesis stays here

**Status:** Accepted (2026-08-18)

## Context
Field devices do not speak openits-models. They speak NTCIP over SNMP, vendor
HTTP APIs, and serial protocols older than the agencies running them. The
collector exists to close that gap: adapters poll devices, `internal/synth`
diffs consecutive snapshots into domain events, `internal/wire` encodes them,
and the publisher ships CloudEvents to the cabinet's NATS.

The expected end state is that the collector is unnecessary. Vendors emit
openits-models natively — from the controller, or from their own management
software — and nothing needs to poll and diff on their behalf. This repository
is the bridge for the years before that, and it should be built as something
that will be deleted.

That framing settles a question that kept resurfacing and was answered wrong
twice before it was answered here: is diffing snapshots into transitions
*overreach*? The collector manufactures `FaultRaised` from two polled
snapshots. No device told it a fault was raised; it inferred it. The
alternative is to publish the polled state constant and let every consumer
diff for itself, which deletes `internal/synth` entirely.

The alternative also has a second-order cost that decided the matter. The
pinned catalog has no state-report ce-type for several device domains — DMS
and CCTV among them — so moving the diff downstream would mean *adding*
ce-types to openits-models whose only reason to exist is that one transitional
component happens to poll. That inverts the dependency: the durable artifact
reshaped to fit the temporary one.

## Decision
**Synthesis is the collector's job.** Polling and diffing are implementation
details of the collector, not facts the bus should learn. On the wire the
collector aims to be indistinguishable from a device that emits natively — a
consumer written against the catalog must not need to know which side of that
line a given event came from.

**openits-models is not reshaped to suit the collector.** A gap in the catalog
is a reason to ask whether the domain concept is real, argued on its own
merits upstream; it is never a reason to add a ce-type for the convenience of
a poller. The accommodation runs one way: the collector adapts to the catalog.

The corollary is the useful half. A ce-type the catalog *does* declare and the
collector does not map is a **collector** gap, and closing it is collector
work. `openits.traffic-sensor.traffic-sensor-status-report.v1` is exactly that
today — it exists upstream and `internal/wire/openits` never mapped it.

**Events, not constant state.** Three reasons, in increasing order of weight:
cabinets are on metered cellular and republishing unchanged state costs
bandwidth that changes nothing; the meaning of a transition survives only if
the party that observed it names it; and every consumer would otherwise
reimplement the diff, at which point they no longer agree with each other
about what happened.

The last one is not merely duplicated effort. A consumer diffing published
state cannot distinguish "the device did not answer" from "the value changed"
— by the time a snapshot leaves the collector that distinction is gone, and
preserving it is the whole of
[ADR 0013](0013-absence-of-evidence.md). Downstream diffing does not move the
problem; it makes it unsolvable.

## Consequences
`internal/synth` is load-bearing, and its correctness bar is the collector's
correctness bar. Two rules follow. One is already recorded: absence of
evidence is never a state change (ADR 0013). The other is new and is the
subject of [ADR 0017](0017-durable-synth-state.md) — the engine's memory of
previous state has to survive a restart, because a synthesizer that forgets
tells the bus about transitions that did not happen.

The adapter contract stays small, which is the payoff contributors actually
feel. An adapter author never constructs an event and never touches the wire
layer; they read the device and populate facets on a `model.Snapshot`. Every
event in the system is produced by a differ in `internal/synth`. That is why
[the adapter tutorial](../tutorial/build-your-first-adapter.md) can be as
short as it is, and why adding a vendor is a bounded task for someone who has
never seen the rest of the repository.

The collector may accumulate machinery that only makes sense for a poller —
snapshot memory, restart fidelity, fixture recorders. That is acceptable and
expected; it dies with the collector. What is not acceptable is machinery that
changes the shape of the catalog, because that outlives us.

## Alternatives considered
**Publish constant state and diff downstream** (rejected). It deletes
`internal/synth` and looks like a simplification, but it relocates the diff to
every consumer, loses the absence-of-evidence distinction permanently at the
collector boundary, and requires catalog additions that exist only to serve a
transitional component.

**Argue the missing state-report ce-types upstream, then diff downstream**
(rejected). Same destination by a politer route. The catalog would carry
ce-types shaped by how the collector happens to work, and would still carry
them after the collector is gone.

**Keep synth but publish both events and periodic state** (rejected). It pays
the bandwidth cost of constant state and keeps the synthesis complexity, and
gives consumers two sources of truth that will eventually disagree. If a
consumer genuinely needs current state rather than transitions, that is a
consumer-side materialized view built from the event stream, not a second
stream from the cabinet.
