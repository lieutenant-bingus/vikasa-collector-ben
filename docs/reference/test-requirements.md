# Test requirements

This is the checklist a PR is measured against. It states the bar, per
contribution type — not a report on where the current code stands against
it. Where the repo itself falls short of a row below, that is a tracked
gap, not a second, looser standard you may copy.

Rule *statements* (what the rule is, why it exists, what enforces it today)
live in [`docs/reference/invariants.md`](invariants.md) — this document
only says what a PR must test, and links there for the rule itself rather
than restating it.

## Any change

- `make check` (vet + tests + boundary lint) passes.
- `go test ./... -race` passes. Poll loops are concurrent; a race here is a
  real bug, not flakiness.

## A new differ

A differ is table-driven and tested against `internal/synth/*_test.go`'s
existing pattern. Copying `internal/synth/dms_test.go` is the safest
starting point (see the gap note below on why not `signal_test.go`). Cover:

- **First poll (no previous state).** The differ's first `Diff` call, with
  no prior snapshot, produces the expected initial events (often none, or a
  single status report — differs vary; match your differ's documented
  first-poll behavior).
- **No change between polls.** Feeding the same facet value twice emits
  nothing on the second call.
- **Each axis changing independently.** If the facet has more than one
  field that can change, prove that changing ONE axis fires only that
  axis's event — an exact event-count assertion, not just "the event I
  expected is present." `TestDMSAxesChangeIndependently`
  (`internal/synth/dms_test.go`) is the reference shape: it moves the
  control-mode axis alone and asserts exactly one event, then the
  display-state axis alone and asserts exactly one event, so the other
  axis's event is proven absent by the count rather than merely unmentioned.

  Two neighbouring tests are named similarly and are a *different* shape —
  useful, but not what this row asks for.
  `TestCCTVDiffer_AxesAreIndependent` (`internal/synth/cctv_test.go`) moves
  both axes in a single poll and asserts a total of 2 events;
  `TestZoneIncidentDiffer_AxesAreIndependent`
  (`internal/synth/perception_test.go`) mutates three incidents at once and
  asserts 1 detected / 1 updated / 1 cleared. Both prove
  multi-axis simultaneity — that concurrent changes each produce their own
  event and don't swallow one another — which is worth having, and neither
  proves that an *isolated* change leaves the other axis silent. A new
  differ ideally has both; if you write only one, write the isolated-axis
  shape.
- **Failed read emits nothing** — the absence-of-evidence rule. See
  [`invariants.md`](invariants.md#absence-of-evidence-is-never-a-state-change)
  for the rule and its enforcement mechanism.
- **DeviceKind stamped onto every event.** Assert the emitted events carry
  the snapshot's `DeviceKind`, e.g. `TestEngineCopiesDeviceKindOntoEvents`
  (`internal/synth/signal_test.go`), `TestDMSEventsCarryDeviceKind`
  (`internal/synth/dms_test.go`).

**Known gap, not a precedent.** Only 4 of the collector's 8 registered
differs currently have a dedicated failed-read test proving the
absence-of-evidence rule holds for them (signal, fault, detector, DMS —
see the invariants row linked above for the exact test names). CCTV,
traffic-interval, zone-incident, and zone-interval do not. A PR touching
one of those four differs is expected to add the missing test as part of
that change, not to treat its current absence as acceptable. Separately,
the signal differ — the one differ that feeds the collector's only shipped
adapter — has no axis-independence test in the shape described above
(`internal/synth/signal_test.go`'s `TestTransitionsEmitChangeEvents`
changes three axes simultaneously and only checks one event is *present*,
never that the others are *absent*). Do not copy that file's pattern for a
new differ; copy `dms_test.go`'s.

## A new adapter

- **A golden read test per facet.** A fixed-input read produces an exact,
  asserted `model.Snapshot` — one test per facet the adapter produces, in
  the shape of `TestASCReadGolden` and `TestASCDetectorGoldenAndOccupancyConversion`
  (`internal/vendors/ntcip/asc_test.go`).
- **A facet-read-failure test producing `model.FacetError`, not a zero
  value.** One unanswered/failed read on a facet must appear in
  `Snapshot.Errors`, not silently become an empty or default facet value.
  `TestASCUnansweredAlarmIsFaultSetFacetError` and
  `TestASCDetectorTableGetFailureIsFacetError`
  (`internal/vendors/ntcip/asc_test.go`) are the pattern; also prove facets
  fail independently of one another (`TestASCFacetsFailIndependently`).
- **Connection-parse rejection for malformed config.** The adapter's
  `Factory` (registered via `adapter.Registry.Register`) must reject an
  invalid `connection` block at build time rather than dialing a broken
  configuration and failing later.
- **`Descriptor()` capability bits correct.** The adapter's `Caps` must
  match what it actually implements — `CapState` only if it is a
  `StateReader`, etc. (`sdk/adapter/adapter.go`).
- **Recorded fixtures, not hand-typed ones (ADR 0008: "no fixtures, no
  merge").** See
  [`invariants.md`](invariants.md#no-fixtures-no-merge) for the rule.
  **Known gap, not a precedent:** the collector's one existing adapter,
  `ntcip-asc`, does not meet this bar today — its `healthyFixture` is a
  hand-typed `map[string]int64`, not a recording captured from a real or
  simulated device (see the invariants row for detail, including the
  adapter's own doc comment admitting its alarm-bitmap table has never been
  validated against physical hardware). A new adapter PR is still measured
  against the recorded-fixture bar; `ntcip-asc`'s fixtures are not a model
  to copy for this requirement, only for the golden-test and
  failure-isolation shapes above.

## A new ce-type mapping

- **A byte-exact golden.** Encode the domain event with a fixed timestamp
  and compare against recorded exact bytes — `TestGoldens`
  (`internal/wire/openits/golden_test.go`) is the pattern; add a case to
  `goldenCases` there.
- **A test asserting no mapped ce-type lacks a golden.** The existing
  `TestGoldensCoverEveryCEType` (`internal/wire/openits/golden_test.go`)
  already does this for every ce-type **the emitter maps** — it iterates
  `CETypes()`, which is derived from the emitter's own `ceTypeFor` table —
  so a new mapping is covered automatically once it's wired into
  `ceTypeFor`, but check the test still passes; it is the thing that would
  catch you forgetting a golden case.

  It does **not** check the mapping against the pinned openits-models
  release's catalog: a catalog ce-type the emitter never mapped is invisible
  to it. `internal/wire/health`'s `conformance_test.go` has that kind of
  check against an external document; the `openits` emitter has no
  equivalent, and building one is successor work. See
  [`invariants.md`](invariants.md#every-mapped-ce-type-has-a-byte-exact-golden)
  and [the gap list](../README.md#known-gaps-and-successor-work). Until it
  exists, adding a *new* catalog ce-type to `ceTypeFor` is a manual
  cross-check against the pinned release, not something CI will prompt you
  for.

## Contribution types not covered above

Wire-emitter version bumps and configuration changes have their own
workflows and validation surfaces documented elsewhere — see
[`docs/how-to/adopt-an-openits-models-release.md`](../how-to/adopt-an-openits-models-release.md)
(with [`map-an-event-to-the-wire.md`](../how-to/map-an-event-to-the-wire.md)
for a single mapping, and `.claude/skills/wire-emitter/` for the checklist
form) and [`docs/reference/configuration.md`](configuration.md)
respectively. This document covers the three contribution shapes most new
contributors touch first: a differ, an adapter, or a ce-type mapping.
