---
name: add-domain-facet
description: Add a new facet (device-state slice) and its differ + domain events to the collector's domain model. Use this whenever the domain model needs a new concept — a new kind of device reading, a new event family, or a new device domain (e.g. "model ramp meter state", "add a CCTV facet", "emit an event when X changes") — and when reviewing changes under sdk/model or internal/synth.
contract: v1
---

# Adding a domain facet, differ, and events

## When this applies

Adding a new device-state concept to the domain model — a facet
(`sdk/model`), its differ (`internal/synth`), and the domain events it
emits — when nothing in `sdk/model` already represents it. Also applies
when reviewing a change under `sdk/model` or `internal/synth`.

It does not apply when the facet already exists and you're only writing
the adapter that produces it — that's the `add-vendor-adapter` skill. It
does not cover giving an event a wire mapping — that's the `wire-emitter`
skill, and is the natural next step once this one is done.

## Invariants

- [Absence of evidence is never a state change](../../../docs/reference/invariants.md#absence-of-evidence-is-never-a-state-change) — a differ is never invoked for a facet kind absent from `Snapshot.Facets` this poll; `prev == nil` means first observation, not "treat as a change."
- [openits-models is not reshaped to suit the collector](../../../docs/reference/invariants.md#openits-models-is-not-reshaped-to-suit-the-collector) — a new facet with no home in the catalog is a domain question argued upstream on its own merits, never a reason to add a ce-type because a poller now produces one.

## Procedure

1. Read `docs/explanation/adapter-to-model.md` for the present/absent/empty
   distinction a facet, differ, and event set all have to respect before
   writing one.
2. **Facet** (`sdk/model/<domain>.go`): a plain struct implementing
   `FacetKind() Kind`, with its own `Kind<Name>` constant. Keep it
   lossless at the device's reporting precision — rounding for the wire
   belongs in `internal/wire`, never here. Document zero-value and empty
   semantics on the type itself. New enums get a named type, a `String()`,
   and an explicit "unknown" zero value.
3. **Events** (`sdk/model/events.go`): embed `Base`; `DeviceKind` is
   stamped by the runner, never set by adapters or differs. Prefer
   transition events (`From`/`To`) over bare state reports; a periodic
   report is its own event kind, emitted every poll by design, not
   produced by diffing.
4. **Differ** (`internal/synth/<domain>.go`): implement `Differ`
   (`Kind()` + `Diff(prev, curr, base)`) and register it in the
   `synth.NewEngine(...)` call in `internal/app/app.go`. `prev == nil` →
   no events, except the fault differ's deliberate first-poll exception
   (it raises everything currently raised). Independent axes diff
   independently — never one event covering two unrelated changes. A
   counter decrease is a device reset, not a negative delta.
5. Write tests mirroring `internal/synth/dms_test.go`'s shape: first poll
   emits nothing, no-change emits nothing, each axis transitions
   independently, combined transitions, failed-read emits nothing, events
   carry `DeviceKind`.
6. Decide the ripple: does this event need a wire mapping now, or is
   "unmapped, dropping loudly at the emitter chain" an acceptable interim
   state? Either way it's a recorded decision, not a default — see the
   `wire-emitter` skill.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

Expected: `make check` and `go test ./... -race` both pass; `gofmt -l .`
prints nothing. A facet or differ change should never need to touch
`internal/wire` — if the boundary lint flags it, the concept belongs
elsewhere in the layering.

## Canonical doc

[`docs/how-to/add-a-domain-facet.md`](../../../docs/how-to/add-a-domain-facet.md) — the full narrative.
