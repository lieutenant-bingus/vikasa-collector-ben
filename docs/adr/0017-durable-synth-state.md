# ADR 0017: Synth's previous state is durable, in the cabinet's own JetStream KV

**Status:** Accepted (2026-08-18)

## Context
[ADR 0016](0016-collector-as-transitional-shim.md) makes synthesis the
collector's job: the bus learns about transitions because the collector
observed them. `synth.Engine` remembers what it last saw in
`prev[deviceID][kind]`, a plain in-memory map (`internal/synth/synth.go`).
That map is correct while the process runs, and empty when it starts.

The damage is asymmetric across differs, and both halves are wrong in
different directions.

Most differs treat `prev == nil` as "we have just learned the state" and emit
nothing — the DMS, CCTV, detector and interval differs all return early. The
fault differ structurally cannot: with no previous set, every currently
standing fault is a fault that appeared, so `internal/synth/fault.go` emits
`FaultRaised` for all of them. A cabinet with four standing faults
re-announces four raises on every restart, including every rollout restart
(ADR 0012) — which is precisely when a fleet operator is watching most
closely and can least afford noise.

Consumer-side deduplication does not rescue this. The ce-id is derived from
`(source, ce-type, time, identity-bytes)` (`internal/cloudevents/envelope.go`)
and the time is the snapshot's `SampledAt`, so a post-restart re-raise carries
a genuinely different id. That is correct — it *is* a different observation —
and it means no consumer can filter the re-raise out by id.

The quiet half is the one that is easier to miss. Transitions that happen
*during* the restart window are lost outright: a signal that changed mode
while the collector was down comes back with `prev == nil` and emits no
`ModeChanged` at all. The first failure manufactures an event that did not
happen; the second swallows one that did. They are the same defect wearing two
faces — the synthesizer forgot.

Whether anything downstream pages a human on `fault-raised` is not the
deciding factor, and deliberately so. Downstream behaviour is not the
collector's concern. The collector's concern is that it advertises transitions
and currently emits some that did not occur, which is a correctness defect on
its own terms with or without a listener.

## Decision
The engine's previous-state map is **written through to a JetStream key-value
bucket in the cabinet's own embedded NATS**, and seeded from that bucket at
boot before the first poll.

**Cabinet-local, permanently.** The bucket is never mirrored, sourced, or
replicated upward. The cabinet's NATS server runs a JetStream **domain**, so
leaf-local and hub streams occupy separate address spaces and cabinet state
cannot be reached from the hub or accidentally federated to it — a structural
guarantee rather than an operational promise. Fifteen thousand cabinets are
fifteen thousand independent buckets that never speak to one another; this
adds nothing to the hub's load and nothing to the leaf-node count.

**Bounded on flash.** The bucket holds one small digest per `(device, facet
kind)` — the state a differ needs to diff, not the raw snapshot — with a
history limit of 1 and a capped value size, so it compacts in place instead of
accumulating. Writes happen only on an actual transition, which is rare by
construction: the same property that made events preferable to constant state
in ADR 0016 bounds the write rate here.

**A collector that cannot reach its bucket waits rather than starting
stateless.** Coming up with empty memory is the failure this ADR exists to
prevent, so it is not an acceptable degraded mode. This shares a dependency
ordering with the broker connection generally.

**One collector per cabinet, period.** There is exactly one writer, so the
bucket needs no ownership arbitration, no lease, and no merge rule. This is an
assumption the design leans on, not an incidental fact.

**Consumers must still be idempotent per `(device, fault-id)`.** No local
store survives a reflash, a swapped storage device, or a first boot, and after
any of those the collector will re-raise — correctly, because it genuinely
does not know. Durable state removes the routine case; the contract covers the
residue. The two are complements, not alternatives, and the idempotency
requirement belongs in the consumer-facing documentation regardless of what
the collector stores — it is stated in
[`reference/consumer-contract.md`](../reference/consumer-contract.md).

## Consequences
Restarts stop generating fault storms, and transitions that occur across a
restart window are reported on the next successful poll instead of vanishing.
A fleet-wide rollout becomes quiet, which is what makes unattended rollouts
(ADR 0012) tolerable at fifteen thousand units.

A new failure mode arrives with the durability: **stale state outlives the
process**. Today a bug that corrupts `prev` is cleared by a restart. Once
`prev` is persisted, a wrong value survives every restart until something
removes it. Clearing the bucket therefore has to be an operator-visible action
with a documented effect — the next poll re-raises every standing fault — and
not a silent self-repair the collector performs when it dislikes what it
reads.

Boot gains a hard dependency and some latency: state must be loaded before the
first poll, or the first poll diffs against nothing and produces exactly the
storm this record prevents.

Container updates do not clear it. The bucket lives in the NATS server's store
directory, not in the collector's container layer, so ADR 0012's
host-executed update path replaces the collector without touching its memory.
That was the deciding advantage over any store the collector would own itself.

This is a decision, not an implementation. Until it is built, the restart
behaviour above stands as a known gap in
[`docs/README.md`](../README.md#known-gaps-and-successor-work).

## Alternatives considered
**Contractual idempotency alone** (rejected as sufficient; adopted as
necessary). Free, and it does make the fault re-raise harmless for a
well-behaved consumer. It does nothing at all for the second half of the
defect — transitions lost across the restart window are lost no matter how
idempotent the consumer is — and it fixes a collector-side correctness bug by
asking everyone else to compensate for it.

**SQLite** (rejected). It puts a second writer on the cabinet's flash
alongside JetStream, brings its own corruption and recovery story to a device
nobody can visit, and adds a schema migration that has to stay in step with
ADR 0012's host-executed container updates. It buys nothing the KV bucket does
not already provide, on a store we are already running.

**Rebuild `prev` at boot by replaying the collector's own published stream**
(rejected). The stream records what the collector *said*, so rebuilding belief
from it makes any past mistake permanent and unfalsifiable. It also turns the
collector into a consumer of the subject space it publishes to, which the
per-cabinet credential scoping is not shaped to grant.

**Publish state constantly so that restart fidelity stops mattering**
(rejected in [ADR 0016](0016-collector-as-transitional-shim.md)).
