# Consumer contract

What a consumer reading events off the cabinet bus can and cannot rely on.

Everything below is a property of **this producer**, not of the catalog. The
schema of any `openits.*` event is openits-models' contract and is documented
in that repo's `bindings/nats/asyncapi.yaml`; the collector's own
`openits-collector.health.*` events are documented in
[`asyncapi.yaml`](../../asyncapi.yaml) at the root of this repo. Neither of
those says anything about redelivery, ordering, or loss. This page does.

It is written for the person on the other end of the bus. If you are adding
an adapter or a mapping, [`explanation/wire-boundary.md`](../explanation/wire-boundary.md)
covers the same envelope from the producing side and in more depth.

## Match on `ce-type`, never on the subject

`ce-type` is schema identity and is the catalog's own string, unmodified. The
**subject is not**: subject grammar is operator-configurable
([ADR 0009](../adr/0009-configurable-subject-templates.md),
[ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md)), so the same
ce-type reaches different subjects on different deployments. The default
grammar is seven tokens
(`{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}`),
but a deployment that configures its own template publishes the same events
somewhere else entirely.

Subscribe on whatever subject pattern the deployment uses; **discriminate on
the `ce-type` header**. A consumer that parses meaning out of subject tokens
is coupled to one operator's routing choices.

Two subject roots exist and they are separate streams: `openits.…` for
catalog telemetry and `openits-collector.…` for the collector's own health.
They carry different retention and are separable for access control, which is
why they are not one stream.

## Delivery: neither at-least-once nor exactly-once

This is the part most likely to be assumed rather than read. The collector
makes a **bounded** effort to deliver, and events can be lost in four
distinct ways. All four are visible in logs; none is currently counted
(there is no metrics subsystem — see
[the gap list](../README.md#known-gaps-and-successor-work)).

1. **Publish exhausts its retries.** `Publisher.Publish`
   (`internal/publish/publish.go`) makes three attempts with a fixed 250 ms
   backoff. If all three fail, `internal/app/app.go` logs
   `"publish failed"` at error level and moves on. There is no queue and no
   later retry — deliberately, because unbounded in-process buffering on a
   cabinet with no operator is a worse failure than a dropped event.
2. **No emitter claims the event.** A domain event with no ce-type mapping is
   logged as `"event dropped: no emitter for domain event"` and discarded.
3. **The emitter declines to encode it.** Where a domain value has no honest
   wire identity, the emitter refuses rather than substituting a
   near-neighbour — see
   [the drop rule](../explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate).
   A visible drop beats a wrong value on the bus.
4. **The device's state changed while the collector was down.** See the
   restart section below.

**The consequence to design around: absence of an event is not evidence that
nothing happened.** If your logic needs to know a device's current state, poll
your own materialized view and treat staleness as unknown rather than
inferring "no event, therefore unchanged."

## Duplicates: expect them, and dedupe on `(device, fault-id)`

The collector sets `Nats-Msg-Id` to the event's `ce-id`, and each stream is
provisioned with a **2-minute** JetStream duplicate window
(`dedupWindow`, `internal/publish/publish.go`). So a genuine retransmission
of the *same observation* inside two minutes is suppressed by the broker.

That covers less than it sounds like, and the gap is the important part.

`ce-id` is a deterministic ULID derived from
`(ce-source, ce-type, occurred-at, payload-identity)`
([the full derivation](../explanation/wire-boundary.md#ce-id-a-deterministic-ulid-not-a-content-hash)).
Two collectors observing the same occurrence produce the same id; the same
collector re-encoding the same observation produces the same id. But a
*different observation of the same underlying state* has a different
`occurred-at`, and therefore a different id — correctly, because it is a
different observation.

**This is why the restart re-raise cannot be deduped by id**, and why the
requirement below is contractual rather than something the broker can do for
you.

> **Required:** treat `fault-raised` as idempotent per
> `(source-device-id, fault-id)`. Receiving a raise for a fault you already
> consider raised is a no-op, not a new incident.

[ADR 0017](../adr/0017-durable-synth-state.md) is the record. It holds
whatever the collector stores locally: no local state survives a reflash, a
swapped storage device, or a first boot, and after any of those the collector
will re-raise — correctly, because it genuinely does not know.

## Restart behaviour

**Today the collector's memory of previous state is in-process only.** On
restart it diffs against nothing, and two things follow:

- **Every standing fault re-raises.** `internal/synth/fault.go` structurally
  cannot do otherwise with no previous set: every currently-raised fault is,
  as far as it can tell, a fault that just appeared. A cabinet with four
  standing faults re-announces four raises on every restart — including every
  rollout restart.
- **Transitions during the restart window are lost.** A signal that changed
  mode while the collector was down comes back with no previous state and
  emits no `mode-changed` at all. The state is correct on the next report
  that carries it; the *transition* is gone.

The first manufactures an event that did not happen at that moment; the
second swallows one that did. [ADR 0017](../adr/0017-durable-synth-state.md)
decides the fix — writing previous state through to a JetStream KV bucket in
the cabinet's own NATS and seeding it at boot — and **it is not implemented
yet**. Until it is, the behaviour above is what a consumer sees. It is
tracked in [the gap list](../README.md#known-gaps-and-successor-work).

## Ordering

**Per device, in order. Across devices, no order at all.**

Each configured device gets one `Runner` goroutine running a sequential
poll loop (`internal/runner/`), and each poll's events are encoded and
published in order, synchronously, on that goroutine. So two events about the
same device arrive in the order the collector observed them.

Nothing orders events across devices, and nothing should be inferred from
their relative arrival. Devices poll on jittered, independent timers.

`sequence` is a **per-device** counter that starts at 1 and **resets when the
collector restarts** (`nextSequence`, `internal/wire/openits/emitter.go`). It
is not a global log position, and a gap in it is not proof of loss — an event
that was dropped or declined consumed a number. Use it to order within a
device's stream, not to detect loss.

## Provenance: who observed this

Every catalog payload carries `observed-by`, set from the collector's
configured `collector_id`. That is the field that distinguishes *"the device
reported this"* from *"a poller inferred it by diffing consecutive reads."*
Today the collector is a
[transitional shim](../adr/0016-collector-as-transitional-shim.md) and
essentially everything it publishes is the latter — mode, plan, fault and
incident transitions are all synthesized by comparing two snapshots.

`ce-source` is the profile URN
([ADR 0015](../adr/0015-ce-source-urn-scheme.md)):

```
urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>
```

`<entity-kind>` is the profile's vocabulary, not the collector's device-kind
string — an `asc` device appears as `controller`, a `dms` as `sign`. On
collector-level events (`collector-started`) there is no device, so the
entity kind is `collector` and the id segment is the deployment's configured
`site`.

Note the ordering consequence for operators: because `ce-source`'s literal
bytes are hashed into `ce-id`, changing any of `region`, `agency`,
`agency_unit` — or `site`, for collector-health events — changes the id of
every event published afterwards. See
[the configuration reference](configuration.md#region--agency--agency_unit--site).

## Envelope summary

CloudEvents 1.0, NATS binary mode: `ce-*` attributes ride as message headers
and the body is the raw payload. For `openits.*` events the body is protobuf,
not JSON — the JSON Schema in openits-models' AsyncAPI describes that
protobuf's *structure* for tooling and is not the wire encoding. For
`openits-collector.health.*` events the body is JSON
([ADR 0007](../adr/0007-collector-owned-health-schema.md)).

| Header | Always present | Notes |
|---|---|---|
| `ce-specversion` | yes | `1.0` |
| `ce-id` | yes | Deterministic ULID; also sent as `Nats-Msg-Id`. See the duplicates section for what it does and does not dedupe. |
| `ce-source` | yes | The profile URN above. |
| `ce-type` | yes | Catalog-verbatim. **This is what to match on.** |
| `ce-time` | yes | RFC 3339. Derived from the same `occurred-at` the id is anchored to. |
| `ce-datacontenttype` | yes | protobuf for `openits.*`, JSON for health events. |
| `ce-dataschema` | `openits.*` only | The immutable schema-registry revision the body validates against. Health events omit it entirely rather than point at a registry they were never part of. |

## What is not here yet

The collector has no readiness or liveness endpoint, publishes no metrics,
and exposes no query interface — it is publish-only. Anything resembling
"ask the collector what it currently thinks" does not exist; build a
materialized view from the event stream instead. The management surface is
designed but unbuilt — see
[`how-to/deploy-a-collector.md`](../how-to/deploy-a-collector.md).
