# Add a domain facet

This is the guide for adding a new concept to the domain model: a kind of
device state nothing in `sdk/model` represents yet. If you already know
which facet you need and just want to get a device onto the bus, you
probably want
[`add-a-vendor-adapter.md`](add-a-vendor-adapter.md#choose-the-device-kind-and-check-for-an-existing-facet)
instead — most adapter work reuses an existing facet, and that document's
starter-tasks table names five facets that are modeled and diffed with no
adapter producing them yet.

Read [`docs/explanation/adapter-to-model.md`](../explanation/adapter-to-model.md)
first. It covers the conceptual shape a facet, a differ, and the
present/absent/empty distinction all have to respect — this document
assumes that reading and stays procedural: which files a new concept
touches, in what order, and what a reviewer will hold it to.

A new concept lands in three parts, always in this order: a **facet**
(`sdk/model`, what a device's state looks like at one poll), a **differ**
(`internal/synth`, how consecutive facets become events), and **events**
(`sdk/model/events.go`, the discrete occurrences the differ emits). The DMS
domain is the worked example throughout: facet in `sdk/model/dms.go`,
events in `sdk/model/events.go`, differ in `internal/synth/dms.go`.

## Facet (`sdk/model/<domain>.go`)

A facet is a plain struct implementing `FacetKind() Kind`, with its own
`Kind<Name>` constant (`KindDMSStatus`, `KindDetectorSamples`, and so on —
`sdk/model/model.go`'s `Facet` interface is the whole contract).

- **Keep it lossless.** Store what the device reports at the precision it
  reports, not the precision the wire needs. `DetectorSample`'s
  `OccupancyTenths` field keeps half-percent resolution exactly; the wire
  emitter rounds later, in `internal/wire`, never here — a fidelity loss
  belongs at the mapping layer, where it's a reviewable, one-line
  decision, not baked silently into the domain type.
- **Document zero-value and empty semantics on the type.** Say, on the
  type itself, whether "this device doesn't have this subsystem" is
  representable as an empty facet rather than an error. See
  [`adapter-to-model.md`'s "Present, absent, or empty" section](../explanation/adapter-to-model.md#present-absent-or-empty-three-states-a-facet-can-be-in)
  for the three states a facet can be in and `readFaultSet`'s worked
  example of getting both non-obvious cases right.
- **Enums get their own named type with a `String()` and an explicit
  "unknown" zero value**, the way `DMSControlMode` and `DMSDisplayState`
  do in `sdk/model/dms.go` (see `sdk/model/enums.go` for the pattern
  applied to `ControllerMode` and friends). The unknown value exists so an
  adapter that reads a device value it doesn't recognize has somewhere
  honest to put it, rather than guessing or defaulting to whatever value
  happens to be numbered zero.

## Events (`sdk/model/events.go`)

Every event embeds `Base` (`DeviceID`, `DeviceKind`, `OccurredAt`) and
implements `EventKind() string`. Two things worth getting right before you
add one:

- **`DeviceKind` is stamped by the runner, never set by adapters or
  differs.** `Base`'s doc comment in `sdk/model/events.go` says why the
  field exists at all: the catalog defines some event shapes once and
  reuses them across services, and without `DeviceKind` a wire emitter has
  no way to route a shared event to the right one.
- **Prefer transition events over bare state reports.** `DMSControlModeChanged`
  and `DMSDisplayStateChanged` (`sdk/model/events.go`) carry `From`/`To`
  and fire only when the differ observes a change. A periodic report is a
  different, deliberate shape — `OperationalStatusReport` is emitted every
  poll by design, not produced by diffing anything — so don't reach for it
  unless your domain genuinely has no transition to report. Periodic state
  reports are the exception upstream: 8 of the catalog's 10 services have no
  status-report ce-type at all, so for most domains there is nothing to map a
  per-poll report onto even if you wrote one.

  An absence there is not a gap the collector gets to fill on its own
  behalf — see
  [openits-models is not reshaped to suit the collector](../reference/invariants.md#openits-models-is-not-reshaped-to-suit-the-collector).
  `internal/synth/dms.go`'s doc comment works the example through: no periodic
  DMS report exists, so a restarted collector doesn't re-announce a sign's
  state until it next changes, and the answer to that is
  [ADR 0017](../adr/0017-durable-synth-state.md)'s durable previous-state —
  not inventing a transition that did not happen.

## Differ (`internal/synth/<domain>.go`)

A differ implements `synth.Differ`
(`Kind() model.Kind` and `Diff(prev, curr model.Facet, base model.Base) []model.Event`,
`internal/synth/synth.go`) and gets registered as one of the
`synth.NewEngine(...)` arguments in `internal/app/app.go` — see that
constructor call for the full list of differs the collector currently
wires in.

- `prev == nil` means first observation: there is no known prior state to
  transition from, so a differ observing DMS's shape returns no events on
  first poll (`dmsDiffer.Diff` in `internal/synth/dms.go`). The fault
  differ is the deliberate exception — it raises everything currently
  raised on first poll, because a standing fault is a state to report, not
  a transition to detect.
- **A failed or absent read never reaches your differ at all.** See
  [`docs/reference/invariants.md`'s row on this](../reference/invariants.md#absence-of-evidence-is-never-a-state-change)
  for the rule this mechanism exists to hold. Mechanically:
  `synth.Engine.Apply` (`internal/synth/synth.go`) only ever ranges over
  `snap.Facets`, so a facet kind with no entry there this poll is simply
  never looked up, and your differ is never called for it — the engine's
  remembered state survives untouched. A new differ does not need to (and
  should not try to) handle a "failed read" case itself; there is nothing
  to handle, because `Diff` is never invoked for that poll.
- **Independent axes diff independently.** `dmsDiffer.Diff` checks
  control-mode and display-state separately and emits a separate event for
  each that changed, even when both move in the same poll — never one
  combined event covering two unrelated axes.
- **Counters: a decrease is a device reset, not a negative delta.** See
  `internal/synth/detector.go`'s stateful differ (it tracks
  `last map[string]time.Time` per device rather than trusting the engine's
  generic `prev`/`curr` pair alone) for the shape this takes when a differ
  needs more memory than one previous facet value.

## Tests

[`docs/reference/test-requirements.md`'s "A new differ" section](../reference/test-requirements.md#a-new-differ)
is the checklist a reviewer holds a new differ to — read it rather than
this paraphrase. In short, mirror `internal/synth/dms_test.go`
(`TestDMSFirstPollEmitsNothing`, `TestDMSNoChangeEmitsNothing`,
`TestDMSAxesChangeIndependently`, `TestDMSBothAxesChangeAtOnce`,
`TestDMSFailedReadEmitsNothing`, `TestDMSEventsCarryDeviceKind`), not
`internal/synth/signal_test.go` — that document explains why. Facet decode
tests (proving an adapter actually produces the facet you defined) live
with the adapter, not with the differ; see
[`add-a-vendor-adapter.md`](add-a-vendor-adapter.md) for that side.

## The ripple: your new facet usually needs a wire mapping too

A facet and differ with no wire mapping is a legitimate, working interim
state — unmapped events drop loudly at the emitter chain rather than
blocking anything, per the drop rule in
[`docs/explanation/wire-boundary.md`](../explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate) —
but it's rarely the end state you want. Once your facet and differ compile
and pass their tests, the next step is deciding how each new event maps
onto an openits-models ce-type (or, deliberately, that it shouldn't). That
decision, plus the golden test that has to accompany it, is
[`map-an-event-to-the-wire.md`](map-an-event-to-the-wire.md)'s whole
subject.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

`make check` runs `go vet`, the full test suite, and
`scripts/lint-boundary.sh` — the same gate CI runs. A facet or differ
change never needs to touch `internal/wire` or import openits-models, so
if the boundary lint flags your change, that's a sign the concept belongs
somewhere else in the layering, not a lint false positive. `go test ./... -race`
matters here specifically because `synth.Engine.Apply` is called from the
poll loop, which runs one goroutine per device concurrently — see
[`docs/explanation/testing-strategy.md`'s `-race` section](../explanation/testing-strategy.md#-race-because-polling-and-publishing-are-concurrent).
`gofmt -l .` should print nothing.
