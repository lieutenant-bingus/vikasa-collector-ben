# Testing strategy: what each layer proves, and what it doesn't

The other documents in this tier each explain one part of the pipeline.
This one is about the tests that run across all of them, and the question a
checklist never answers: what does a green test suite actually tell you,
and what is it silent about?

Each kind of test earns trust in something specific and is blind to
something else. Treating a test as proof of more than it checks is how a
real bug reaches a cabinet, and how a good contribution gets blocked for the
wrong reason. Read this when reviewing a PR, choosing what test to add, or
wondering why `make check` runs what it runs. For the tests a specific
change *must* ship, see
[`reference/test-requirements.md`](../reference/test-requirements.md).

## Golden tests: pin bytes, not meaning

A golden test encodes a known input and asserts that the output matches a
committed value byte for byte. There are goldens at two layers:

- **Emitter goldens.** `TestGoldens` in `internal/wire/openits/golden_test.go`
  pins, for every mapped event, the ce-type, the `ce-dataschema`, the exact
  proto bytes, and the identity bytes that feed `ce-id`. It is byte-exact on
  purpose: a field-by-field assertion would pass through a proto field
  renumbering that changes what consumers receive.
  `TestGoldensCoverEveryCEType` in the same file requires a golden case for
  every ce-type the emitter can produce, so the cases cannot lag the
  routing table.
- **Subject goldens.** `TestDefaultTemplateGolden` in
  `internal/subject/subject_test.go` pins the rendered subject for the
  default template, catalog and collector-owned events alike.

**What a golden proves:** this input produces this output today, and any
change to it shows up as a diff a human must approve.

**What it does not prove:** that the output is *correct*. A golden happily
pins a wrong mapping forever; it only complains when the mapping *changes*.
`TestGoldens`'s doc comment states the discipline that makes the bytes
worth anything: every case "was verified by decoding it and reading the
result against its fixture before being pasted". Regenerating without that
step "turns a golden into a record of whatever the code happened to do,
which is worse than no golden at all — it looks like coverage." A golden is
a change detector, not a correctness oracle. The correctness judgment is
made once, by a person, when the golden is written or updated.

The rule itself is canonical in
[every mapped ce-type has a byte-exact golden](../reference/invariants.md#every-mapped-ce-type-has-a-byte-exact-golden).

## Fixture replay: reproducible, not verified

Adapter tests never talk to a live device. `snmptest.Static`
(`sdk/transport/snmp/snmptest`) replays fixed OID → value maps in place of
a real SNMP agent: `Values` for integers, `Octets` for byte strings, and
`FailCall` to fail one particular request. An adapter's `Read` runs against
it exactly as it would against the real `snmp.Client`, with no network and
no hardware.

**What fixture replay proves:** the adapter parses, decodes, and assembles
a `Snapshot` correctly *from the values it was handed*. It is deterministic
and hardware-free, so a maintainer can review an adapter by reading the
mapping and the fixture.

**What it cannot prove:** that the fixture resembles what a real device
sends. A value captured from a live device and a value typed in until the
test passed produce the same test run and the same green check. A file
from `collector record-fixture` narrows this: it carries required
provenance and was written by the adapter's own poll, and a hand edit to
an octet string fails to load. It still cannot prove the device it names
is the one it came from.

The two shipped adapters show both sides. `ntcip-dms`'s fixture in
`internal/vendors/ntcip/dms_test.go` is transcribed from live SNMP reads of
a bench sign, and its comment says when and how. `ntcip-asc`'s
`healthyFixture` in `asc_test.go` is hand-written, and the adapter's own
comment says its alarm bitmap has never been checked against a physical
controller. That falls short of
[no fixtures, no merge](../reference/invariants.md#no-fixtures-no-merge),
and the gap is tracked in
[known gaps](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state).
[`record-fixtures-from-a-device.md`](../how-to/record-fixtures-from-a-device.md)
is how to record a real one.

### Provenance is a review question, not a test

No test can tell a recording from an invention, so **provenance is judged
by a reviewer reading the diff**, not asserted by CI. This matters in
practice. While the `review-adapter-contribution` skill was being evaluated
against synthetic adapter PRs, a review without the skill's guidance
blocked a clean contribution twice, both times for the fixture being a Go
literal (commit `5161b3f`). Every fixture in this repo at the time was a
Go literal, so that objection carried no information and would have turned
away a real contributor. Prefer a recorded file now that one can be made,
but a transcribed literal with a provenance comment is still valid
evidence.

What a reviewer can usefully look at is the *values*, not the format:

- **Signs of hand-typing:** round numbers, values that exactly match the
  test's assertions, a suspiciously minimal OID set, no note of when and
  where the capture was made, comments explaining what each value "should"
  mean.
- **Signs of a real recording:** extra fields nobody would bother
  inventing, values the test never asserts on, device quirks that only
  appear on real hardware (an odd default, an optional field the device
  always fills).

The reviewer checklist lives in
`.claude/skills/review-adapter-contribution/SKILL.md`; this section records
why the question has to be asked at all.

## Differ tests: state transitions, and the absence rule

Each facet kind has one `Differ` in `internal/synth`, tested table-driven
against fixed timestamps (for example `t0` in
`internal/synth/signal_test.go`, rather than `time.Now()`), so expected
output never depends on when the test ran.

**What differ tests prove:** given a previous facet and a new one, the
differ emits exactly the events the change represents. That means a test
per axis that can change independently, one for the first observation (no
previous state), and one confirming nothing fires when nothing changed.

Differ tests are also where the most important invariant is proven:
[absence of evidence is never a state change](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).
Every registered differ has a failed-read test: a good poll, then a failed
read, then a recovery. The recovery is chosen so that an engine which had
forgotten the pre-failure state would emit something different, because
"the failed poll emitted nothing" alone cannot tell a correct engine from
one that wiped its memory. Engine-level tests in
`internal/synth/engine_test.go` and `durable_test.go` cover the guards
around the differs: a facet also reported as failed, a facet of the wrong
type, and an engine held until it is seeded.

**What they do not prove:** anything about a facet kind that does not exist
yet. A new differ needs its own failed-read test; see
[`test-requirements.md`](../reference/test-requirements.md#a-new-differ).

## Conformance tests: does the published shape match a contract?

Conformance tests ask a different question from goldens: not "does this
match what we pinned before" but "does this match a contract this repo
does not write". Several tests carry that idea, and they check different
contracts.

**`TestCollectorStreamPassesTheTier2Harness`** (`internal/wire/tier2`) runs
openits-models' own Tier 2 conformance checks (`tools/conformance/tests`),
imported rather than copied, against events a live collector publishes. The
collector runs against an embedded NATS server, with scripted
signal-controller, sign, camera, traffic-sensor and perception adapters. Before any check runs, the test waits
until every event type in its `want` list has been published, so no check
can pass for lack of the event it judges. It then runs each device kind's
event-stream checks:
- subject shape, `ce-type` format, `ce-source` URN, and `ce-id` presence;
- per-device sequence continuity;
- that each fault kind matches its service;
- each service's event shapes, including whether class breakdowns
  reconcile with their totals.

The rest of each suite judges a device's YANG state tree, which a collector
never publishes, or needs an event the collector does not emit.
`TestEveryHarnessCheckIsRunOrRuledOut` holds those lists to the pinned
harness in both directions. A check the harness renames or drops fails
instead of silently not running, and a check a release adds fails until it
is classified. Because the checks come from the pinned release, a pin bump
that tightens the profile shows up here with no edit on our side.

It has paid for itself. When it was extended to perception, it found that
one domain class breakdown fed two reports whose breakdowns must sum to
different totals, crossed and observed. `ZoneMeasurement` now carries one
breakdown for each.

**`internal/app/conformance_test.go`** covers what the harness does not:
the binding's header constants (`ce-specversion`, `ce-datacontenttype`, and
a `ce-dataschema` on every catalog event), and that collector-owned events
stay off the catalog subject space
([ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md)).
`internal/app/permissions_test.go` proves that the least-privilege broker
grant the deploy guide documents is enough for everything the collector
does ([ADR 0022](../adr/0022-transport-security-and-credentials.md)).

**`internal/wire/health/conformance_test.go`** checks the collector-owned
schema against this repo's own `asyncapi.yaml`, in both directions. Every
ce-type the health emitter can produce must be documented, every
documented channel must be one it can produce, and the documented payload
shape must match the bytes emitted.

### The catalog-conformance check

[ADR 0008](../adr/0008-fixture-golden-testing-bar.md) sets the bar for the
catalog emitter: every ce-type it can produce must exist in the **pinned
openits-models release's own** `asyncapi.yaml`, so drift between what the
collector emits and what the catalog defines fails CI.

`internal/wire/openits/catalog_test.go` is that check. It finds the pinned
release in the module cache (`go list -m`, so `go.mod` alone picks the
version), reads its `bindings/nats/asyncapi.yaml`, and checks both
directions. `TestEmittedCETypesAreInPinnedCatalog` fails on a ce-type the
emitter produces that the release does not declare.
`TestEveryCatalogCETypeIsMappedOrAllowlisted` fails on a declared ce-type
the emitter neither maps nor lists, with a reason, in `unmapped`.
`TestUnmappedAllowlistIsCurrent` fails on an allowlist entry that is now
mapped or no longer declared.

`TestGoldensCoverEveryCEType` is not part of this. It checks the goldens
against the emitter's own routing table and says nothing about the catalog.

## `-race`: because polling and publishing are concurrent

`make check` runs the whole suite once, with the race detector on. There is
no separate non-race pass, because a race-enabled run proves strictly more.
Concurrency here is not incidental: each device polls on its own
goroutine, and the publisher's drainer empties a shared queue while runners
keep adding to it.

**What `-race` proves:** for every interleaving that actually happened
during the run, no two goroutines touched the same memory without
synchronisation. Bugs like a snapshot mutated while being read, or a map
written from two goroutines, can pass every sequential test and still
corrupt state in the field. `-race` is aimed at exactly those.

**What it does not prove:** that no race exists. The detector is dynamic:
it watches one execution, not every possible one. A race on a path the
tests never run, or on an interleaving that did not occur this time, is
invisible. A clean run is real evidence, not proof, which is why
concurrency-sensitive code still deserves reasoning about its
synchronisation.

## Fuzzing: what a device can send

A device's response is untrusted input, and
[`SECURITY.md`](../../SECURITY.md) puts "a malformed or malicious device
response that crashes the collector" in scope. Fixtures show a device's
honest answers; native Go fuzz targets show what happens with any answer:

- `FuzzResponseDecode` (`sdk/transport/snmp`) feeds arbitrary bytes through
  gosnmp's packet decode and this package's conversion of each varbind;
- `FuzzASCRead` and `FuzzDMSRead` (`internal/vendors/ntcip`) give each
  adapter any value, or no answer, for every OID its `Read` asks for, and
  check the snapshot is coherent: no facet both present and failed, and
  percentages and occupancy in range;
- `FuzzPollSurvivesAnyAdapter` (`internal/runner`) has an adapter panic,
  return nil, or hand back nil, wrong-typed or failed-and-present facets,
  through a runner with every differ registered.

Each target's seed corpus, drawn from the existing fixtures plus every input
that has ever failed it (under `testdata/fuzz/`), runs as an ordinary test in
`make check`. `make fuzz` adds coverage-guided mutation. CI runs it for 15
seconds per target on every change; run it longer locally
(`make fuzz FUZZTIME=10m`) after touching a decode path.

**What fuzzing proves:** the inputs it tried did not crash, hang or break
the stated checks. It has already paid for itself twice. A nil facet from
an adapter panicked in the engine, outside the runner's recovery, which would
have taken down every device. And a large photocell reading overflowed
`percentOf` into a published 255% ambient light.

**What it does not prove:** that no such input exists. Coverage guidance
finds a lot quickly, but the search is still a sample, and the checks only
catch what they state. A value decoded wrongly but in range passes.

## Doc guards: structural, not semantic

`internal/docs` is a package of tests, not production code, that keeps
specific documentation claims mechanically true. Its package doc sets the
scope: "Only STRUCTURAL claims are checked — a named enforcer that
vanished, a config field that appeared, a subject address that drifted.
Prose accuracy is a review problem and is deliberately not attempted here."

There are four guards. Three are Go tests in `internal/docs`; the fourth is
a shell script that `make check` runs.

- `TestConfigReferenceDocumentsEveryField` — every `collector.yaml` field
  reachable by reflection over `config.Config` appears in
  `docs/reference/configuration.md`.
- `TestInvariantsTableNamesRealEnforcers` — every enforcer named in
  `docs/reference/invariants.md`'s table (a file path, a `Test...`
  function, or a `make` target wired into `check`) actually exists.
- `TestAsyncAPIAddressesMatchRenderedSubjects`, with two sibling tests in
  the same file — the example subject addresses in `asyncapi.yaml` render
  correctly through the real `internal/subject.Template`, and the `ce-source`
  and `ce-id` descriptions match the code.
- `scripts/lint-docs.sh` — every relative markdown link under `docs/`, in
  the root documents, in `.claude/skills/`, and in the pull-request
  template resolves to a real file, and every `#fragment` on such a link
  matches a real heading. It also checks that each `SKILL.md` has the
  sections the skill contract requires. The anchor check is what makes
  "link, don't restate" workable: each rule lives in one place and
  everything else links to its heading, so a renamed heading would
  otherwise quietly disconnect a rule from every document that cites it.
  `docs/specs/` is deliberately not scanned, because designs not yet built
  may reference things that do not exist.

**What doc guards prove:** a specific, named claim (a field exists, an
enforcer exists, an address renders) is still true of the code. They turn
"the docs might be stale" into "CI would have caught that".

**What they cannot prove, by design:** that a claim is relevant, or that
the prose around it is accurate. A guard checks existence and equality. It
has no opinion on whether a still-existing enforcer is the right one, or
whether an accurate field list is missing the context a reader needs. That
is a review problem, on purpose.

## Summary: what to reach for

| Layer | Proves | Blind to |
|---|---|---|
| Golden tests | Encoding is stable; any change is visible as a diff | Whether the pinned mapping is correct |
| Fixture replay | The adapter parses what it was handed | Whether what it was handed resembles a real device |
| Differ tests | State transitions are correct, including the absence rule, for every registered differ | Facet kinds that do not exist yet |
| Conformance tests | Published shape matches an external contract | Anything the loaded contract does not state; the catalog check reads the pinned `asyncapi.yaml`, not openits-models' prose or JSON Schemas |
| `-race` | No race on the interleavings this run exercised | Paths not run, and interleavings that did not occur |
| Fuzzing | Device-facing decode and the poll path survive the inputs tried | Inputs not tried; values decoded wrongly but in range |
| Doc guards | A specific structural claim still matches the code | Whether the claim is relevant, or the prose around it accurate |

No layer substitutes for another. A golden cannot catch a wrong mapping, a
doc guard cannot catch stale prose, and fixture replay cannot catch an
invented fixture. Knowing which layer you are relying on, and which
question it was never going to answer, is most of what "the tests pass"
can tell you.
