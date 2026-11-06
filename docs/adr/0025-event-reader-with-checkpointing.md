# ADR 0025: EventReader, with a core-owned checkpoint

**Status:** Proposed (2026-11-06)
**Amends:** [ADR 0021](0021-sdk-interfaces-ship-with-their-first-implementer.md) —
the event-shaped reader it removed returns, in the change that implements
it, as that ADR requires. [ADR 0004](0004-pull-only-state-and-event-readers.md)'s
split stands.

## Context
A signal controller's high-resolution log (the ATSPM / Indiana enumeration
log) is the only place an NTCIP-era controller records phase, pedestrian,
detector, overlap and preemption transitions at tenth-of-a-second
resolution. A state poll cannot observe them: the transitions happen many
times per poll interval. Six signal-control notifications in the pinned
catalog (phase-state-change, pedestrian-event, detector-transition,
overlap-change, preemption-event, unmapped-event) exist for exactly these
rows and were allowlisted as "not yet assessed" because nothing produced
them.

The first implementer is `maxtime-asc`: Q-Free MaxTime serves its log as
XML over HTTP, a ring of about ten thousand rows with a monotonically
increasing row ID and controller-local timestamps.

ADR 0021 said the interface ships with its implementer, its runner path and
its checkpointing, in one change. ADR 0004 deferred checkpointing ("don't
re-emit the same log window") to this point.

## Decision
**`sdk/adapter.EventReader` and `CapEvents`.**

```go
type EventReader interface {
	Adapter
	ReadEvents(ctx context.Context, after []byte) (events []model.LogEvent, next []byte, err error)
	EventInterval() time.Duration
}
```

- **The core owns the checkpoint; the adapter owns its meaning.** The
  cursor is opaque bytes (at most 1 KiB) the adapter returned last time.
  The core stores it per device in the durable-state bucket of
  [ADR 0017](0017-durable-synth-state.md) under the reserved kind
  `event-log-cursor`, write-through, and passes it back. An adapter does no
  persistence of its own.
- **No backfill on first contact.** With no cursor, `ReadEvents` returns
  no events and a cursor at the log's end. Replaying a ring of history as
  fresh events would publish an hour of transitions with old `ce-time`s on
  every new deployment or cleared bucket.
- **The runner waits for stored cursors to load** before its first read.
  Reading first would prime at the log's end and skip what the device
  logged while the collector was down.
- **Publish, then advance.** The cursor is recorded after the events are
  handed to the publisher. A crash between the two replays that read's
  rows; their `ce-id` is content-addressed (the same row encodes to the
  same identity bytes regardless of sequence), so JetStream's duplicate
  window and consumers drop them. The guarantee is the same as synthesized
  events have: a row lost from the bounded publish queue
  ([ADR 0020](0020-broker-absence-is-latency.md)) is lost.
- **Resynchronisation is the adapter's.** A device that renumbers its log
  (reboot, clear) or wraps past the cursor is recognised by the adapter,
  which resumes from what it can place rather than failing every read.
- **Two deliberate losses, both bounded.** A cursor the adapter cannot
  parse (a format change across an upgrade) is treated as none and primes
  at the log's end: failing every read until an operator clears the bucket
  would lose more. A log row that does not parse is skipped and the cursor
  moves past it: retrying it would pin the cursor on that row forever.
  The skip is silent on the wire: a published row carries its code and
  parameter, not the device's row ID, so a consumer cannot see the gap.
- **Identical rows share a `ce-id`.** Two rows with the same code,
  parameter and timestamp encode to the same identity, so the duplicate
  window keeps one. That is the price of a replay-safe `ce-id` without a
  row ID on the wire. The 2.15.1 lab log had one such pair in 6,165 rows
  (vendor code 600, twice at boot) and none among Indiana codes.
- **The core stamps device identity** (`model.LogEvent.Stamped`), as it
  does a Snapshot's kind, so an adapter cannot publish as another device.
- **A separate loop, sharing the poll loop's isolation but not its health
  reporting.** Per-read timeout, no overlapped reads, panic recovery
  counted as an adapter fault. A failing log read is logged and retried
  from the same cursor; it does not touch reachability, which is the state
  poll's to report. A controller whose log endpoint is disabled is not
  down.
- **Capabilities describe the instance.** The registered Descriptor says
  what the adapter can do (`CapState|CapEvents`); a built adapter's
  Descriptor says what this device is configured to do. An event loop
  starts iff the built adapter declares `CapEvents`; declaring it without
  implementing `EventReader` fails boot.

**`model.HighResEvent`** carries a row as the controller logged it: Indiana
code, param, and the controller's timestamp. `Class()` routes the code to a
catalog family by its Indiana range and the param's validity for that
family; anything else (MaxTime codes above 255, Indiana codes the pinned
release does not define, out-of-range params) is `unmapped-event` with the raw code and param in
`source`. The emitter's Indiana table is generated from, and tested
against, the pinned YANG's identity descriptions, so a code is never given
an identity the release does not define, and the code→identity mapping
cannot drift from the pin.

## Consequences
- Seven ce-types leave the "not yet assessed" allowlist: the six above and
  coordination-change. Indiana 50-54 and 151 carry the phase, 131 the
  pattern (only 1-253: 0, 254 and 255 are free/flash sentinels, so the
  leaf is omitted and the raw param stays in `source`), 132 the cycle
  length, 133 the offset, 134-149 phase 1-16's split, and 150 (param 0-7)
  the cycle state. A row is one change, so the `previous-*` leaves are
  absent: the emitter keeps no per-device state to fill them.
- The SDK gains an exported interface that is now frozen with v1. Its
  signature was written against a real log, with checkpointing, which is
  what ADR 0021 waited for.
- An NTCIP adapter could implement `EventReader` over SNMP (NTCIP 1202 v3's
  event log tables) without SDK changes.
- Cursor writes add KV writes on every read that returns rows. At the
  default 15 s interval that is at most four writes per minute per device,
  into a bucket whose history is 1; the flash-wear argument of ADR 0017
  holds. A shorter interval is the operator's trade.
- Controller-local timestamps need the controller's zone in config. In the
  repeated hour of an autumn DST change the instant is ambiguous and
  `OccurredAt` may be an hour off for that hour; ordering and the cursor
  use the row ID and are unaffected.

## Alternatives considered
- **Adapter-owned checkpoint (a file per device).** Puts persistence and
  its failure modes in every log adapter, and a second store beside the KV
  the cabinet already has.
- **Diff the log as state.** A log is not a state: the same transition
  twice is two events. It is exactly the flag-on-StateReader design ADR
  0004 rejected.
- **Backfill on first contact.** Useful for analytics, wrong for an event
  bus whose consumers act on events as they arrive. A backfill tool, if
  wanted, publishes to an analytics subject, not the live one.
- **Advance the cursor on publish ack.** Exactly-once in the face of queue
  loss, but it couples the reader to the publisher's acks and holds the
  log loop on a broker outage, which ADR 0020 deliberately avoids.
