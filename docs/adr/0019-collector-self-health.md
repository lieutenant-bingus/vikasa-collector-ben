# ADR 0019: Collector self-health is a periodic heartbeat on a per-instance subject

**Status:** Proposed (2026-08-23)
**Amends:** [ADR 0007](0007-collector-owned-health-schema.md) — adds to the
collector-owned health schema it established, and changes how device-less
events render their subject.

Breaking subject and ce-type changes are taken freely here rather than
carried compatibly. The project is pre-1.0, nothing outside this repository
consumes `openits-collector.*`, and the cost of every one of these changes
only grows with the first subscriber. They are made as though greenfield,
deliberately.

## Scope

**The `openits-collector` root carries the collector's own health and nothing
else.** Everything the collector observes about a *downstream device* belongs
on the `openits` root, in the catalog's own vocabulary, because a device is a
catalog entity and its condition is a catalog fact.

That is a rule about the root, and it settles what has been an unexamined
conflation. `device-status-changed` lives under `openits-collector.health.*`
today for no better reason than that it was convenient to own the schema. But
the collector is not the subject of that event — a signal controller is — and
publishing it under the collector's own namespace says otherwise.

So device reachability leaves this root entirely. It maps to
`openits.<service>.comm-health-event.v1`, whose `kind` identityref carries
exactly the transitions the collector already detects:

| domain event today | catalog `kind` |
|---|---|
| `DeviceStatusChanged{Reachable: false}` | `comm-lost` |
| `DeviceStatusChanged{Reachable: true}` | `comm-restored` |

The catalog's `comm-attempt-window` kind, with `attempts-total`,
`attempts-failed` and `percent-loss`, is also a strictly better home for
failure counts than the collector's own `consecutive_failures` — a required
integer that is provably always 1 or 0 today and therefore duplicates
`reachable` exactly.

**What this costs, stated plainly.** [ADR 0007](0007-collector-owned-health-schema.md)
exists because gen-1's poll heartbeat vanished when upstream regeneration
dropped a message, and it concluded that health must never be hostage to
schema churn. Moving device reachability into the catalog re-accepts that
exposure *for device health*. That is the right trade: a device's condition is
a catalog concept, and if the catalog cannot express "this controller stopped
answering," that is a gap worth fixing upstream rather than routing around
locally ([ADR 0016](0016-collector-as-transitional-shim.md)). ADR 0007's
guarantee narrows to what actually needs it — the collector's own health,
which no catalog will ever model, and which must stay reportable precisely
when the wire model is broken.

**One upstream gap this exposes, and why asking is legitimate.**
`comm-health-event` is declared in the shared `openits-common-comm-health-events`
module but exposed as a channel on `signal-control` alone; no other service
imports it. The parallel `openits-common-fault-events` module is exposed on
all nine. So the asymmetry reads as an oversight rather than a decision, and
asking upstream to expose comm-health the way faults already are is arguing a
domain concept on its own merits — a DMS that stops answering is exactly as
real a fact as an ASC that does — not adding a ce-type for a poller's
convenience. ADR 0016 permits the first and forbids the second.

Nothing blocks on that answer. `ntcip-asc` is the only shipped adapter and it
is a `signal-control` device, so the mapping works end to end today. The
services that lack the channel also lack adapters.

## Context

**The collector's entire self-report is one event, once, at boot:**
`collector-started`, carrying `occurred_at` and `version`. Nothing else it
ever publishes is about itself.

Meanwhile it privately observes at least five conditions about its own
health and publishes none of them — all go to `slog` on stderr, inside a
cabinet nobody can visit:

| `internal/app/app.go` | Condition |
|---|---|
| `:132` | an adapter failed to close |
| `:143` | an emitter returned an encode error |
| `:155` | an event was dropped: no ce-source entity kind |
| `:169` | a publish failed after all three attempts |
| `:179` | an event was dropped: no emitter claimed it |

A collector that is failing to publish *every single event* is, from the
bus, indistinguishable from one that has nothing to report.

### Silence is unfalsifiable

[ADR 0016](0016-collector-as-transitional-shim.md) makes events — not
constant state — the collector's output, for good reasons that still hold.
The consequence for self-health is that **silence is the normal, healthy
condition**. A cabinet where nothing has changed and a cabinet whose
collector is dead produce identical bus traffic, indefinitely.

There is no fix for this that is not periodic. A process that dies emits
nothing, by definition; absence cannot be published by the absent party.

This is worth stating plainly because it looks like a contradiction of ADR
0016 and is not. ADR 0016 rejects republishing *device state*, which the bus
can learn from transitions because a transition is observable. Collector
liveness has no transition to observe. It is the one signal that must be
periodic, and it is a category ADR 0016's reasoning never covered.

[ADR 0007](0007-collector-owned-health-schema.md)'s own Context is the
supporting evidence: it cites gen-1's lost **poll heartbeat** as the incident
that motivated owning this schema. The schema it created has no heartbeat.

### Self-health has no per-instance address

Two different cabinets render the identical subject today, because
`{device_id}` renders as the literal `"collector"` for device-less events
and `site` is not a token in the default grammar:

```
cabinet-042  -> openits-collector.us-ga.metro.d01.health.collector.collector-started
cabinet-9001 -> openits-collector.us-ga.metro.d01.health.collector.collector-started
```

Device events do not have this problem —
`openits.us-ga.metro.d01.signal-control.asc-main-and-5th.mode-changed` names
its device. So the only events without an individual address are the ones
about the 15,000+ things that need individual management.

On a shared subject, NATS cannot give us four things it otherwise would:
per-cabinet subscription, per-subject retention (the natural way to make a
heartbeat stream *be* a bounded fleet-health table), subject-scoped read
authorization, and consumer sharding.

### The collector never names itself

`health.NewHealthEmitter()` takes no arguments. `collector_id` is passed only
to the openits emitter, where it becomes `observed-by` on telemetry. So
telemetry says `observed-by: cabinet-042-collector` while self-health is
identified only by `ce-source`, keyed on `site`. **Nothing on the bus ever
states that binding.**

### The constants that shape the design

15,000+ cabinets, exactly one collector each ([ADR 0017](0017-durable-synth-state.md)),
on metered cellular, behind carrier NAT that prevents a control plane from
dialing in ([ADR 0012](0012-host-executed-updates.md)). Pull-based scraping
is therefore structurally unavailable — the repo's open "no metrics
subsystem" gap has no scrape-shaped answer, and never will.

And one constant that should lower the urgency more than it usually does:
**the collector is not in the control path.** The signal controller runs
traffic whether or not the collector is alive. A dead collector costs
observability, not safety, and nobody rolls a truck in three minutes for it.

## Decision

**1. `openits-collector` carries only the collector's own health, and its
`{service}` token names the entity kind.**

```
openits-collector.instance.heartbeat.v1  -> …d01.instance.cabinet-042.heartbeat
openits-collector.instance.started.v1    -> …d01.instance.cabinet-042.started
openits-collector.instance.stopping.v1   -> …d01.instance.cabinet-042.stopping
```

Device reachability moves to `openits.<service>.comm-health-event.v1` and off
this root entirely (see Scope), so `openits-collector` has exactly one
`{service}` value and one entity: the collector instance.

`{service}` is the ce-type's second segment (`decompose`,
`internal/subject/subject.go`). `instance` names the kind of entity the
*following* token identifies, which is the rule the catalog root already
follows — `signal-control.<controller-id>`, and now
`instance.<site>`.

An earlier draft kept device health on this root under a `device` token,
making the two siblings. That is no longer the shape: with device health on
the catalog root, there is no sibling to be consistent with, and `instance`
is simply the entity this root is about.

A fleet operator subscribes `openits-collector.*.*.*.>` — the whole root —
and gets collector health and only collector health. That is the property the
root rule buys, and it is stronger than any subscription a shared root could
offer.

**This adds no stream, and removes nothing.** Bindings truncate above the
service token, so `OPENITS-COLLECTOR-<region>-<agency>-<unit>` stays as ADR
0011 defined it — now carrying a single, coherent family.

**2. Device-less events render `{device_id}` as `site`.** One collector per
cabinet makes `site` the collector's identity, and `ce-source` already uses
it as the id segment for exactly these events — so subject and envelope come
to agree on one identifier instead of disagreeing. `site` is already
validated to `^[a-z0-9][a-z0-9-]*$`, strictly stronger than subject-legal.

```
openits-collector.us-ga.metro.d01.instance.cabinet-042.heartbeat
```

The token count stays constant, which is what the literal `"collector"` was
protecting.

**3. The interval is operator-configurable, defaulting to 300s.** A
heartbeat is ~750 bytes on the wire (~330 B body, ~270 B CloudEvents
headers, plus subject and framing):

| Interval | Per cabinet/month | Fleet aggregate | Detection, link up (3 beats) |
|---|---|---|---|
| 60 s | 32 MB | 250 msg/s | ~3 min |
| **300 s** | **6.5 MB** | **50 msg/s** | **~15 min** |
| 900 s | 2.2 MB | 17 msg/s | ~45 min |

The detection column holds only while the uplink is up. It is not a
dead-man timer — see the next clause.

60 s buys detection latency nobody acts on at 5× the cost on a metered link.
300 s is the default because ~15 minutes is well inside any real response
time for a non-safety-critical observability outage. It is configurable
because a lab, a pilot deployment, and a 15,000-unit production fleet do not
want the same number, and because the right value depends on a carrier plan
this project does not choose.

**4. Counters are cumulative since boot, never deltas, and every heartbeat
carries a `boot_id`.**

The reason is *not* that beats get lost in transit — they largely do not.
`Publisher.Publish` uses `js.PublishMsg`, a JetStream publish with ack, so a
successful publish means the beat is persisted to the cabinet-local stream.
From there the cellular link runs between two JetStream servers (the cabinet
is a leaf node with its own domain, [ADR 0017](0017-durable-synth-state.md)),
and replication catches up after an outage rather than dropping. Beats arrive
**late**, sometimes very late. They are not silently discarded.

Cumulative wins for a different and stronger reason: **deltas are
incompatible with last-value retention.** The property that makes 15,000
cabinets tractable is a hub-side stream keyed per collector holding only the
newest beat — a fleet-health table bounded by fleet size rather than by
fleet size × time. Under that retention a delta is destroyed: all that
survives is the most recent interval's increment, which says nothing about
totals. A cumulative counter under last-value retention is exactly right, and
a late-joining consumer gets full totals from the single message it reads.

The narrow loss paths that do remain argue the same way. A beat is genuinely
lost if the local broker is unavailable across all three publish attempts —
which is narrow, but correlates with NATS restarts during host-executed
updates (ADR 0012), precisely when someone is watching — or if cabinet-stream
retention rolls over during a long outage before the hub catches up. Either
silently corrupts a delta sum; with cumulative counters they cost resolution
and nothing else.

The `boot_id` is what tells a consumer the counters reset rather than went
backwards.

This also repairs the boot-event problem: because every heartbeat carries
version and boot identity, a consumer that joined late — or whose retention
window rolled past the `collector-started` — learns what a cabinet is running
within one interval instead of waiting for its next restart, which under
host-executed updates (ADR 0012) could be weeks.

**5. Heartbeat jitter is derived from the collector's identity, and is
stable.** 15,000 aligned publishers are a thundering herd. 15,000
independently re-randomizing ones make the arrival interval unpredictable for
the dead-man timers a consumer has to run. A stable per-instance offset gives
both a spread fleet and a regular beat.

**6. Liveness is read from the heartbeat *and* the leaf connection state,
never from beat arrival alone.** A gap in arrivals is ambiguous by itself,
because a cellular outage delays beats without the collector being dead. The
leaf-node topology resolves it, and a consumer should be told to use both
axes:

| Leaf connection | Beats arriving | Conclusion |
|---|---|---|
| up | yes | healthy |
| up | stopped | **the collector process is dead or wedged** — NATS and the link are demonstrably fine |
| down | — | cabinet or uplink is down; collector status unknown, and beats are queuing |
| reconnects | backlog floods in | the collector was alive throughout, provable from each beat's `occurred_at` |

Two things follow. `occurred_at` must be the collector's own observation
time and a consumer must never infer liveness from arrival time — a beat
that arrives six hours late still proves the collector was alive six hours
ago, which is exactly the fact wanted. And detection of collector death is
correctly *deferred* while the uplink is down, rather than being reported as
a false positive.

This is also the clearest answer to "why not just watch the leaf
connection." That tells you the cabinet's **NATS server** is up. The
collector is a separate process on the same host and can be dead, wedged, or
crash-looping while NATS holds the leaf connection open perfectly. The
heartbeat is the only thing that distinguishes those.

**7. The heartbeat states the collector's identity, both ways.** It carries
`collector_id` in the body while `ce-source` carries `site`, so the binding
nothing currently states is stated once per interval.

**8. `started` and `stopping` are discrete events; the heartbeat does not
replace them, and the edge stores nothing to produce them.**

A restart is a *transition* — a discrete, observable occurrence — and
[ADR 0016](0016-collector-as-transitional-shim.md) already says transitions
get events. Liveness is the thing that has no transition to observe, which is
why it needs a periodic beat. The two are the categories that ADR 0016
distinguishes, not two sources of truth about one fact.

The retention profiles are what make the distinction load-bearing at fleet
scale. They pull in opposite directions and a single signal cannot serve
both:

| | rate | what the hub wants | retention |
|---|---|---|---|
| `heartbeat` | 15,000 ÷ interval — ~50/s at the default | current liveness, current totals | last value per collector: a table bounded by fleet size, not by time |
| `started` | only on restart | the restart record | full history, cheap because the rate is low |

**Restart counting belongs at the hub, not the edge.** The collector reports
each occurrence and never counts them; a cabinet does not need durable local
state to know it has restarted forty-seven times, and asking it to would mean
another writer on the same flash that [ADR 0017](0017-durable-synth-state.md)
is already careful about. The hub aggregates `instance.started` over whatever
window an operator asks for. Nothing about restart history requires the
edge KV bucket.

`stopping` is best-effort, and that is the honest bar: a process that is
killed cannot send it. The asymmetry is the signal — a `started` with no
preceding `stopping` is an unclean restart, which is exactly what a fleet
operator wants to page on after a rollout (ADR 0012).

The pair also distinguishes a process restart from a cabinet power cycle
without any new field: if the leaf connection stayed up across the gap, the
collector restarted; if the leaf dropped and reconnected, the cabinet did.

## Consequences

A fleet-management consumer can, for the first time, answer "which cabinets
are alive, what are they running, and are any of them silently dropping
events" from the bus alone. That question currently has no answer at all.

Bandwidth grows by ~6.5 MB per cabinet per month at the default, which is
real on a metered plan and is the price of the previous paragraph. Operators
who cannot afford it can lengthen the interval; operators who need faster
detection can shorten it and pay for it.

**The `collector-started` subject changes.** Anything subscribed to
`…health.collector.collector-started` breaks. Doing this now, while the
repository has no external consumers, is strictly cheaper than doing it
later; `asyncapi.yaml` must be updated with it, and
`TestAsyncAPIAddressesMatchRenderedSubjects` (`internal/docs/asyncapi_test.go`)
fails the build until it is, so the drift cannot ship quietly.

`collector-started` moves to `openits-collector.instance.started.v1` rather
than being retired. An earlier draft argued for retiring it, on the grounds
that a heartbeat carrying `boot_id` and `uptime` subsumes it — which is true
of the *information* and false of the *retention*. Under the last-value
retention that makes 15,000 collectors a bounded table, only the newest beat
survives, so a heartbeat-only design silently discards restart history. The
low-rate discrete event is what carries it, and carries it cheaply.

Crash-looping is visible either way and remains a useful cross-check: a
collector restarting every few seconds produces `started` events in a burst
and heartbeats with `uptime` near zero and a new `boot_id` each time.

The heartbeat's counters are the collector's metrics surface. This does not
close the "no metrics subsystem" gap so much as decide its shape: on a fleet
behind carrier NAT, push-on-a-schedule is not a workaround for the absence of
scraping, it is the only form that fits the topology. A future Prometheus
endpoint would serve a local operator standing in the cabinet, not the fleet.

**Device health changes root, schema and emitter.**
`openits-collector.health.device-status-changed.v1` ceases to exist. Its
domain event (`model.DeviceStatusChanged`, produced by `internal/runner`) is
unchanged and keeps producing; what changes is that the *openits* emitter
claims it instead of the health emitter, mapping it to
`openits.<service>.comm-health-event.v1` with a `kind` of `comm-lost` or
`comm-restored`. It gains a byte-exact golden like every other catalog
mapping, and `internal/wire/health` shrinks to the collector's own events.

This also dissolves a defect found while reviewing the current design rather
than papering over it. `SourceFor` returns `""` for a device kind absent from
the profile's `entityKindFor` vocabulary, and `encodeAndPublish` drops any
event with an empty source — so today a device kind the profile does not name
would have its *health* silently dropped, which directly contradicted ADR
0007's promise that health survives upstream churn. Under this record that
dependency is correct rather than contradictory: device health is a catalog
event and *should* require catalog vocabulary. Collector self-health uses
entity kind `collector`, which is unconditionally mapped, so it is never
subject to it.

The collector's `consecutive_failures` field disappears with the schema. It
was a required integer that was provably always 1 or 0. Where a real failure
count is wanted, the catalog's `comm-attempt-window` kind carries
`attempts-total`, `attempts-failed` and `percent-loss`, which is what the
field's name always implied and never delivered.

## Alternatives considered

**A readiness/liveness HTTP endpoint instead** (rejected as a substitute;
still wanted for its own reasons). ADR 0012's health-gated rollback needs a
local readiness signal and the management-surface spec designs one. It cannot
answer this record's question: carrier NAT means nothing outside the cabinet
can poll it, so it serves the host's supervisor, not the fleet. The two are
complements — readiness gates a rollout locally, the heartbeat tells the
fleet what happened.

**Emit a heartbeat only when something is wrong** (rejected). It inverts the
failure mode: a collector that dies stops sending its "something is wrong"
signal too, which is precisely the case that must be detectable. Only a
positive periodic signal makes absence meaningful.

**Watch the leaf-node connection state instead of publishing anything**
(rejected as a substitute; adopted as the second axis). The hub already knows
whether each cabinet's NATS server is connected, and that is genuinely useful
— it is what distinguishes "collector dead" from "uplink down" in the table
above. It cannot replace the heartbeat, because the collector is a separate
process from the NATS server it publishes to: it can be dead, wedged, or
crash-looping while NATS holds the leaf connection open perfectly. Leaf state
reports on the broker; the heartbeat reports on the collector.

**Piggyback liveness on existing telemetry** (rejected). A cabinet whose
devices are genuinely quiet emits nothing for hours, and that is correct
behaviour under ADR 0016 — so telemetry silence cannot distinguish a healthy
quiet cabinet from a dead one. That is the whole problem, not a workaround
for it.

**Put self-health under the existing `health` service token and only fix
`{device_id}`** (rejected). It would make self-health addressable, which is
most of the value, but leaves fleet consumers filtering device health out of
their subscription by event name — and NATS wildcards are whole-token, so
there is no filter that means "collector events only" as the family grows.
One extra token now buys a stable subscription forever.
