# Architecture: the pipeline end to end

This is the document a newcomer reads first. It does not explain any one
package in depth — later documents in this tier do that — it explains how
the pieces fit together, so that when you land on `internal/synth/signal.go`
or `internal/wire/openits/emitter.go` you already know what calls it and
what it hands off to next.

## The pipeline, in one line

```
[device] --transport--> ADAPTER --sdk/model--> SYNTH ENGINE --domain event--> WIRE EMITTER --ce-type + payload--> PUBLISHER --CloudEvent--> local JetStream
```

Four stages sit between the device and the bus. Each one owns exactly one
kind of decision, and nothing downstream can see how an upstream stage made
its decision — only its output.

## The four stages, and what each one owns

### 1. Adapters — own the transport

Package: `internal/vendors/<vendor>/` (a file per device kind, e.g.
`internal/vendors/ntcip/asc.go` — not a `<kind>/` subdirectory).

An adapter's only obligation is to return `sdk/model` types. Whether the
data arrived over SNMP, REST, or something else never crosses the boundary
into the core — the core has no concept of transport. `sdk/` and
`internal/vendors/` are held to this by lint, not just convention; see
[`docs/reference/invariants.md`](../reference/invariants.md#adapters-and-sdk-never-import-openits-models)
for the enforced rule and what checks it.

### 2. The synth engine — diffs snapshots into domain events

Package: `internal/synth/`.

Each device kind has one `Differ` registered per facet kind. On every poll,
`synth.Engine.Apply` (`internal/synth/synth.go`) compares the facet just
read against the last value it saw for that device and facet kind, and
emits domain events for whatever changed. What happens when a facet's read
failed is a load-bearing rule with its own canonical statement, not
restated here; see
[`docs/reference/invariants.md`](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).

Why the collector diffs at all, rather than publishing polled state and
letting consumers diff, is
[ADR 0016](../adr/0016-collector-as-transitional-shim.md). The engine's
memory of previous state is in-process today and does not survive a
restart; [ADR 0017](../adr/0017-durable-synth-state.md) decides the fix and
[the known-gaps tracker](../README.md#known-gaps-and-successor-work)
records what today's behaviour actually is.

### 3. Wire emitters — map a domain event to a ce-type and a payload

Package: `internal/wire/openits/` (one package per pinned openits-models
release; `internal/wire/health/` is the separate collector-owned schema) —
the one layer on the other side of the boundary linked in stage 1.

An emitter's `Encode` method looks up the incoming event's kind and device
kind in a routing table and returns the wire ce-type and proto payload, or
declines the event (`ok == false`) if it cannot encode it faithfully.

### 4. The publisher — renders the subject and ships to JetStream

Package: `internal/publish/`.

Given a ce-type and a device ID, the publisher renders a NATS subject from
the operator's configured template (`internal/subject/`), builds the
CloudEvents binary-mode headers, and publishes to local JetStream with
bounded retry (three attempts, a fixed 250ms backoff between them) — never
unbounded in-process buffering. Each subject's stream is derived from its
own binding, not separately configured, so the two cannot disagree
(`publish.StreamNameForBinding`).

## Worked trace: one OID, start to finish

The clearest way to see the four stages meet is to follow one reading all
the way through. Take the NTCIP operation-status OID,
`.1.3.6.1.4.1.1206.4.2.1.2.7.0`, on an `ntcip-asc` device.

**1. Adapter.** `asc.readSignalStatus` (`internal/vendors/ntcip/asc.go`)
looks up that OID in the poll's varbind map:

```go
op, ok := vals[oidOperationStatus]
```

If the OID answered, it builds a `model.SignalStatus` facet with
`Mode: modeFromOperation(op)` and appends it to the snapshot. If the OID
did not answer, the adapter does *not* invent a mode — it records a
`model.FacetError` for `KindSignalStatus` and returns, leaving the facet
absent from this poll's snapshot entirely.

**2. Synth engine.** `synth.Engine.Apply` (`internal/synth/synth.go`) finds
the `SignalStatus` facet in the snapshot, looks up its registered differ
(`signalDiffer`, `internal/synth/signal.go`), and calls `Diff` against the
device's last-known `SignalStatus`. `signalDiffer.Diff` always emits a
current-state `OperationalStatusReport`, and — only when a previous value
exists and the mode actually changed — a `model.ModeChanged{From: ..., To:
...}` domain event.

**3. Wire emitter.** The `openits` emitter's `Encode`
(`internal/wire/openits/emitter.go`) is called with the `ModeChanged`
event. It looks up `ceTypeFor[key{ev.EventKind(), ev.EventDeviceKind()}]` —
here `key{"mode-changed", "asc"}` — which resolves to
`openits.signal-control.mode-changed.v1`, then builds the proto payload
(declining the event instead if the mode has no mappable controller-mode
identity upstream).

**4. Publisher.** `internal/app.encodeAndPublish` wraps the encoded payload
in a CloudEvents envelope and calls `Publisher.Publish` with the ce-type and
device ID. `Publisher.Publish` renders the subject from the operator's
template (`internal/subject/subject.go`) and publishes. With the default
seven-token template and a device ID of `asc-main-and-5th` on tenant
`region=us-ga, agency=metro-atlanta, agency_unit=d01`, the rendered subject
is:

```
openits.us-ga.metro-atlanta.d01.signal-control.asc-main-and-5th.mode-changed
```

— confirmed by rendering it directly against `subject.Template.Render`
rather than deriving it by hand. That subject is what actually goes out on
the wire; the ce-type (`openits.signal-control.mode-changed.v1`) is a
separate, fixed piece of identity that travels in the CloudEvents `type`
header on the same message, not encoded into the subject's version.

## The poll loop: jitter, per-poll timeout, and panic isolation

Package: `internal/runner/`. One `Runner` per configured device, each on its
own goroutine (`internal/app.Run` starts one `go r.Run(ctx)` per entry in
`cfg.Devices`).

- **Jittered start.** Before the first poll, `Run` waits a random duration
  between zero and the configured interval (`r.jitter(r.interval)`) rather
  than firing immediately. A fleet of devices configured with the same
  interval does not all poll in lockstep.
- **Per-poll timeout.** Each poll runs under its own
  `context.WithTimeout(ctx, r.timeout)` (`pollOnce`), independent of the
  next poll's schedule. A device that hangs mid-read is cut off at the
  timeout, not left to block the runner indefinitely.
- **Panic isolation.** `readGuarded` recovers a panic from the adapter's
  `Read` call and turns it into an ordinary failed-poll error. Adapters are
  third-party code by design (`sdk/adapter`); one adapter panicking can
  never take the runner loop down.
- **Serialized transport I/O, one goroutine per device.** Because each
  device has exactly one `Runner` goroutine running a sequential
  `pollOnce` → wait-for-ticker loop, that device's adapter is never asked
  to do two things at once. Concurrency exists across devices, not within
  one.

A poll that returned no error resets `consecutiveFailures` and hands the
snapshot to `engine.Apply`; a poll cut short by the runner's own shutdown
(`ctx.Err() != nil`) is not reported as a device failure at all — the same
absence-of-evidence rule the synth engine applies to a single facet, applied
here to an entire poll.

## What stays canonical: the envelope vs. the subject

The worked trace above produced two different outputs from the same
`ModeChanged` event: a ce-type (`openits.signal-control.mode-changed.v1`),
fixed by the wire emitter's routing table, and a subject
(`openits.us-ga.metro-atlanta.d01.signal-control.asc-main-and-5th.mode-changed`),
built from the operator's configured template. What relationship, if any, is
allowed to hold between the two is a load-bearing rule with its own
canonical statement, not restated here; see
[`docs/reference/invariants.md`](../reference/invariants.md#subjects-are-operator-configurable-the-cloudevents-envelope-stays-canonical).
