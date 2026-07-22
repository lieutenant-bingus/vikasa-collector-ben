---
name: add-domain-facet
description: Add a new facet (device-state slice) and its differ + domain events to the collector's domain model. Use this whenever the domain model needs a new concept — a new kind of device reading, a new event family, or a new device domain (e.g. "model ramp meter state", "add a CCTV facet", "emit an event when X changes") — and when reviewing changes under sdk/model or internal/synth.
---

# Adding a domain facet, differ, and events

The domain model (`sdk/model`) is the collector's own vocabulary — richer
than any wire schema, owned by this repo, and the only thing adapters are
allowed to produce (ADR 0002). A new concept lands in three parts: a
**facet** (what a device's state looks like at one poll), a **differ**
(how consecutive facets become events), and **events** (discrete domain
occurrences). The DMS domain is the cleanest worked example: facet in
`sdk/model/dms.go`, differ in `internal/synth/dms.go`, events in
`sdk/model/events.go`; its design rationale is
`docs/specs/2026-07-16-dms-domain-design.md`.

## Facet (`sdk/model/<domain>.go`)

- A facet is a plain struct implementing `FacetKind() Kind`, with a
  `Kind<Name>` constant. Keep it lossless: store what the device reports
  at the precision it reports (see `DetectorSample.OccupancyTenths` — the
  domain keeps half-percent exactly; the wire emitter rounds later).
  Mapping-to-wire losses belong in `internal/wire`, never here.
- Document zero-value and empty semantics on the type. "Device doesn't
  have this subsystem" must be representable as an empty facet, not an
  error — a `FacetError` for a normal deployment is a permanent false
  alarm.
- Enums get their own named types with explicit values and a `String()`;
  see `sdk/model/enums.go`. Include an "unknown" zero value so an
  unrecognized device reading is representable without guessing.

## Events (`sdk/model/events.go`)

- Embed `Base` (DeviceID, DeviceKind, OccurredAt) and implement
  `EventKind() string`. `DeviceKind` is stamped by the runner, never by
  adapters — it exists so wire emitters can route shared events (the same
  `FaultRaised` publishes under different services depending on device).
- Prefer transition events carrying `From`/`To` over bare state reports;
  periodic reports are their own event kind (`OperationalStatusReport`),
  emitted every poll by design, not by diffing.

## Differ (`internal/synth/<domain>.go`)

- Implement `Differ` (`Kind()` + `Diff(prev, curr, base)`) and register it
  in the engine's construction. `prev == nil` means first observation.
- The iron rule, enforced by the engine and expected of every differ:
  **absence of evidence is never a state change.** A failed or absent
  facet emits nothing and keeps previous state. First observation emits
  nothing for transition events (there is no known prior state to
  transition from) — the fault differ is the deliberate exception (it
  raises everything currently raised, because a standing fault is a
  state, not a transition).
- Independent axes diff independently: the DMS differ emits separate
  events for control-mode and display-state changes even when both move
  in one poll.
- Counters: treat a decrease as a device reset, not a negative delta
  (see `internal/synth/detector.go`).

## Tests

Mirror `internal/synth/dms_test.go` per differ: first poll emits nothing,
no-change emits nothing, each axis transitions independently, combined
transitions, failed-read emits nothing, events carry DeviceKind. Facet
decode tests live with the adapter that produces the facet (see the
`add-vendor-adapter` skill).

## Ripple checklist

A new facet usually touches: `sdk/model/<domain>.go` (+ tests),
`sdk/model/events.go`, `internal/synth/<domain>.go` (+ tests), engine
registration, and — eventually — a wire-emitter mapping decision (map or
drop, see the `wire-emitter` skill). Unmapped events drop loudly at the
emitter chain; that's an acceptable interim state, not a bug. Gate with
`make check` and `go test ./... -race`.
