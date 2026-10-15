# ADR 0020: Broker absence is latency, not loss

**Status:** Accepted (2026-10-12)
**Implements:** [ADR 0014](0014-config-is-the-trust-boundary.md)'s sanctioned
broker-absent exception, and [ADR 0017](0017-durable-synth-state.md)'s "waits
rather than starting stateless" clause.
**Amends:** [ADR 0017](0017-durable-synth-state.md) — state is seeded before
the first *diff*, not the first poll (clause 2).
**Resolves:** the open problem in
[`specs/2026-08-09-management-surface-design.md` §5](../specs/2026-08-09-management-surface-design.md#5-open-problem-publish-loss-while-the-broker-is-down).

## Context
The cabinet's NATS runs in its own container, next to the collector rather
than embedded in it (fleet spec D2). That was the right call, and it means the
broker is sometimes not there: at boot when the two start together, and for
the duration of every broker restart, including its own updates.

Today the collector handles each of those moments badly, and differently:

- **At boot**, `publish.Connect` dials NATS and provisions streams as part of
  `app.Run`, and a failed dial exits the process. A broker that loses the
  start-up race reads, under [ADR 0012](0012-host-executed-updates.md)'s
  health-gated rollback, as a failed update.
- **While running**, `Publisher.Publish` retries three times over roughly
  750 ms and then drops the event. There is no buffer, so every broker restart
  is a data-loss window.
- **ADR 0017** needs the broker at boot for a second reason: the engine's
  previous state lives in a JetStream KV bucket and must be seeded before the
  first diff. That pulls in the opposite direction from the boot exception —
  one says "don't wait for the broker", the other says "don't diff without it".

These are one question — what does the collector do when the broker is not
there — and they get one answer.

## Decision
**Broker absence costs latency, never correctness, and is counted when it
costs data.**

1. **Boot does not depend on the broker.** Config validation, adapter
   construction and subject validation stay boot-time failures (ADR 0014).
   The broker connection is established in the background, and stream
   provisioning runs on every (re)connect rather than once at boot. The
   collector reaches a running state with no broker present.

2. **Polling never waits; synthesis does.** Runners start immediately, so
   device reachability is known and reported from the first poll. The engine
   *holds* each device's snapshots — diffing nothing — until that device's
   previous state has been seeded from the KV bucket. A missing bucket on
   first boot is created empty, and that empty state is the legitimate fresh
   start ADR 0017 already accepts. There is no timeout that falls back to
   diffing from empty: while the broker is unreachable nothing can be
   published anyway, so a fallback would buy nothing but the restart storm
   ADR 0017 exists to prevent.

   While held, only the latest snapshot per `(device, facet kind)` is kept. A
   held snapshot is evidence of current state, not of the path taken to it;
   intermediate transitions during a long outage are lost, and ADR 0017's
   consumer contract already covers that residue.

3. **Publishing is queued, retried with backoff, and dropped only when the
   queue is full.** Encoded events go into a bounded in-memory FIFO. One
   publisher goroutine drains it in order, retrying a transient failure with
   capped exponential backoff for as long as the broker is away; a permanent
   failure (a stream that cannot exist, a rejected message) is dropped at once
   rather than retried. When the queue is full, the **oldest** event is
   dropped to make room: the newest events describe the device as it is now,
   which is what a consumer reconnecting after an outage needs. The bound is a
   compiled-in constant sized for the deployment's actual rate (about ten
   devices per cabinet, transitions rare by construction), not an operator
   knob.

4. **Every drop is counted.** Queue drops, permanent-failure drops and
   no-emitter drops are cumulative counters, alongside poll and publish
   counters. They are the collector's metrics surface, exposed two ways and
   only two: as JSON on the local `/status` endpoint, and in the
   [ADR 0019](0019-collector-self-health.md) heartbeat on the bus. No
   Prometheus or OpenTelemetry dependency: the fleet already has a transport
   to every cabinet, and it is the bus.

5. **Shutdown drains, bounded.** On shutdown the queue gets the existing drain
   window to empty; whatever remains is counted as dropped and logged once,
   with the count.

## Consequences
- A collector and broker can start in either order, and a broker restart
  delays events rather than losing them, up to the queue's bound.
- The readiness signal (management-surface spec) has a clean definition to
  build on: config accepted, and at least one successful poll. It does not
  require the broker, so a broker outage never fails a rollout of the
  collector.
- `publish.Connect` stops being a boot step, and `Publisher.Publish` stops
  being synchronous from the runner's point of view. A runner's `sink` call
  returns once the event is queued.
- Ordering is preserved per collector (one FIFO, one drainer), which is
  stronger than today: concurrent runners currently publish independently.
- Memory is now a function of outage length, capped by the queue bound.
- The [known gap](../README.md#known-gaps-and-successor-work) for ADR 0014's
  broker-absent exception, and the "unclaimed events drop without a metric"
  gap, close when this is implemented, not when it is accepted.

## Alternatives considered
- **Spill the queue to disk.** It survives a long outage, but it rebuilds on
  the box the durability JetStream already owns, and it adds flash writes on
  exactly the hardware fleet spec M1 worries about.
- **Keep the short retry and only count drops.** Least code, and honest, but
  every broker update becomes a guaranteed loss window. Rejected because the
  fix is small.
- **Block polling until the broker and the KV bucket are reachable.** It is
  the simplest to reason about, but it turns a broker outage into a collector
  outage. The cabinet would report nothing at all, not even device
  reachability, during the window an operator most needs it.
- **Diff from empty after a timeout.** Rejected in clause 2.
- **Prometheus `/metrics` or OpenTelemetry export.** Both are standard, but
  each needs something that reaches into 10–15k cabinets to collect from it.
  The heartbeat already reaches the fleet over the transport it has.
