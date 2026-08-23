# ADR 0019: Collector self-health is a periodic heartbeat on a per-instance subject

**Status:** Proposed (2026-08-23)
**Amends:** [ADR 0007](0007-collector-owned-health-schema.md) — adds to the
collector-owned health schema it established, and changes how device-less
events render their subject. Device health (`device-status-changed`) is
deliberately untouched; see "Scope" below.

## Scope

This record is about what the collector reports **about itself**. It is not
about device reachability, which is a different subject reporting on a
different entity and stays exactly as it is.

The two have been conflated because both live under
`openits-collector.health.*` and are produced by one emitter. They are not
the same concern: device health is the collector observing something else,
at a cardinality of a handful per cabinet, and its consumer is a traffic
operator. Self-health is the collector observing itself, at a cardinality of
one per cabinet and 15,000+ per fleet, and its consumer is whoever keeps
those 15,000 running. Separating them is part of the decision.

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

**1. Self-health gets its own service token.** Two new ce-types:

```
openits-collector.instance.heartbeat.v1
openits-collector.instance.stopping.v1
```

`{service}` is the ce-type's second segment (`decompose`,
`internal/subject/subject.go`), so this puts self-health in its own subject
space without touching device health. A fleet operator subscribes
`openits-collector.*.*.*.instance.>` and gets every self-health signal that
exists now or later; a traffic operator subscribes `…health.>` and gets
device reachability. Different retention, different consumers, different
authorization — which is the same argument [ADR 0011](0011-namespace-rooted-subject-spaces.md)
made one level up when it split the roots.

**This adds no stream.** Bindings truncate above the service token, so both
families stay in the existing `OPENITS-COLLECTOR-<region>-<agency>-<unit>`
stream. The separation exists so the *hub* has a clean filter when it
aggregates; hub-side retention is not this record's decision.

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

| Interval | Per cabinet/month | Fleet aggregate | Detection (3 missed beats) |
|---|---|---|---|
| 60 s | 32 MB | 250 msg/s | ~3 min |
| **300 s** | **6.5 MB** | **50 msg/s** | **~15 min** |
| 900 s | 2.2 MB | 17 msg/s | ~45 min |

60 s buys detection latency nobody acts on at 5× the cost on a metered link.
300 s is the default because ~15 minutes is well inside any real response
time for a non-safety-critical observability outage. It is configurable
because a lab, a pilot deployment, and a 15,000-unit production fleet do not
want the same number, and because the right value depends on a carrier plan
this project does not choose.

**4. Counters are cumulative since boot, never deltas, and every heartbeat
carries a `boot_id`.** Across 15,000 lossy cellular links, beats will be
missed. A since-last-beat delta is lost permanently when that happens; a
cumulative counter lets a consumer diff across the gap and lose nothing but
resolution. The `boot_id` is what tells a consumer the counters reset rather
than went backwards.

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

**6. The heartbeat states the collector's identity, both ways.** It carries
`collector_id` in the body while `ce-source` carries `site`, so the binding
nothing currently states is stated once per interval.

**7. `stopping` is emitted on clean shutdown**, best-effort, so a rollout
restart (ADR 0012) is distinguishable from a crash. Best-effort is the honest
bar: a process that is killed cannot send it, and that asymmetry is the
point — a missing `stopping` is evidence, not an error.

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

Whether `collector-started` should move to the `instance` service token as
well — becoming `openits-collector.instance.started.v1` — is left open
deliberately. It is the same family and the symmetry is appealing, but it is
a second breaking change for a signal the heartbeat now largely subsumes,
and it may be simpler to retire it than to move it.

The heartbeat's counters are the collector's metrics surface. This does not
close the "no metrics subsystem" gap so much as decide its shape: on a fleet
behind carrier NAT, push-on-a-schedule is not a workaround for the absence of
scraping, it is the only form that fits the topology. A future Prometheus
endpoint would serve a local operator standing in the cabinet, not the fleet.

Nothing here changes device health. `device-status-changed` keeps its
subject, its schema, and its per-device addressing.

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
