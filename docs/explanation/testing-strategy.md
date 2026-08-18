# Testing strategy: what each layer proves, and what it doesn't

[`architecture.md`](architecture.md) names the four pipeline stages.
[`adapter-to-model.md`](adapter-to-model.md), [`pluggability.md`](pluggability.md),
and [`wire-boundary.md`](wire-boundary.md) each explain one stage's shape in
depth. This document is not about any one stage — it is about the tests that
sit across all of them, and the question a checklist never answers: what does
a green test suite actually tell you, and what is it silent about?

That framing is the point. Every layer below earns trust in something
specific and is blind to something else. Treating a layer as proof of more
than it checks is how a real bug survives to a cabinet nobody is watching,
and how a real contribution gets blocked for the wrong reason (more on that
below). If you are reviewing a PR, deciding what test to add for a change, or
just trying to understand why `make check` runs the things it runs, this is
the page.

## Golden tests: pin bytes, not meaning

A golden test encodes a known input and asserts the output matches an exact,
previously-committed value byte-for-byte. This repo has goldens at two
layers:

- **Emitter goldens** — `internal/wire/openits/golden_test.go`'s
  `TestGoldens` pins the exact proto bytes and ce-type every mapped domain
  event encodes to. It is deliberately byte-exact rather than field-by-field:
  a field-level assertion would pass through a proto field renumbering that
  silently changes what a consumer receives, and a diff here forces a human
  to decide whether the mapping changed on purpose or the models module moved
  under it. `TestGoldensCoverEveryCEType` in the same file is the
  exhaustiveness half — every ce-type the emitter can produce must have a
  golden case, so the set can't quietly lag the mapping table.
- **Subject goldens** — `internal/subject/subject_test.go`'s
  `TestDefaultTemplateGolden` pins the exact rendered subject string for the
  default template, so the multi-tenant token-splice behavior can't drift
  silently either.

**What a golden test proves:** this exact input produces this exact output,
today, and any future change to that mapping will be visible as a diff a
human has to approve.

**What it does not prove:** that the output is *correct*. A golden test
happily pins a wrong mapping forever — it only complains when the wrong
mapping *changes*. `TestGoldens`'s own doc comment states the discipline that
makes the pinned bytes worth anything: every case "was verified by decoding
it and reading the result against its fixture before being pasted."
Regenerating the hex without doing that step "turns a golden into a record
of whatever the code happened to do, which is worse than no golden at all —
it looks like coverage." A golden test is a change detector, not a
correctness oracle; the correctness judgment happens once, by a human, at
the moment the golden is written or updated.

Both golden rules above are enforced rules with their own canonical
statement — see the
["Every mapped ce-type has a byte-exact golden" row](../reference/invariants.md#every-mapped-ce-type-has-a-byte-exact-golden)
in `invariants.md`.

## Fixture replay: reproducible, not verified

Adapters never talk to a live device in a test. `sdk/transport/snmp/snmptest.Static`
(and the equivalents other transports would ship) replays a fixed
OID → value map in place of a real SNMP agent — an adapter's `Read` runs
against `Static` exactly as it would against the real `snmp.Client`
interface, and gets back whatever the fixture says, every time, with no
network and no hardware.

**What fixture replay proves:** the adapter under test parses, decodes, and
assembles a `Snapshot` correctly *from the bytes it was handed*. It is
deterministic and hardware-free — a maintainer can review an adapter PR by
reading the mapping and eyeballing the fixture, with nothing else running.

**What it does not prove — and cannot, by construction:** that the fixture
resembles what a real device actually sends. `Static.Values` is a Go
`map[string]int64` literal. A value recorded from a live SNMP walk and a
value someone typed in until the test passed produce the exact same Go
source, the exact same test run, and the exact same green checkmark. File
format cannot distinguish them, because there is no file format in play —
this repo has **no `testdata/` mechanism** of any kind. Every fixture,
including `ntcip-asc`'s, is a Go literal committed alongside the test that
uses it.

This is not a hypothetical gap. `internal/vendors/ntcip/asc_test.go`'s
`healthyFixture` is exactly this: a hand-typed map, not a device recording,
and the adapter's own doc comment on the alarm bitmap says as much — it has
never been validated against a physical controller. That fails an enforced
rule with its own canonical statement — see the
["No fixtures, no merge" row](../reference/invariants.md#no-fixtures-no-merge)
in `invariants.md`; this repo's one shipping adapter does not meet that bar
today. That gap is tracked, not hidden — see
[known gaps and successor work](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state).

### Provenance is a review question, not a test

Because no test can tell a recording from an invention, **provenance has to
be judged by a reviewer reading the diff**, not asserted by CI. This
distinction is not academic: during an evaluation of the maintainer-review
skill for this repo, a reviewer without this context blocked a genuinely
good adapter contribution on the grounds that its fixture was "a Go map
literal, not a recording" — a category error, since *every* fixture in this
repo is a Go map literal, recorded or not. That objection would have burned
a real contributor over a property the file format was never capable of
proving in the first place.

What a reviewer can actually look for is signal in the *values themselves*,
not the format:

- **Signs of hand-typing:** round numbers, values that exactly match the
  test's assertions, a suspiciously minimal OID set, no capture metadata in
  a comment, prose explaining what each value "should" mean.
- **Signs of a real recording:** unexplained extra fields nobody would
  bother inventing, values the test doesn't even assert on, device quirks
  that only show up on real hardware — an odd default, a field a spec says
  is optional but the device always populates.

`.claude/skills/review-adapter-contribution/SKILL.md` carries this checklist
for reviewers; this document exists so the reasoning behind it — why the
question has to be asked at all — is written down somewhere a newcomer will
actually find it.

## Differ tests: state transitions, and the absence rule

Each facet kind has one `Differ` in `internal/synth`, tested table-driven
against fixed timestamps (`internal/synth/signal_test.go`'s package-level
`t0`, for example, rather than `time.Now()` — so a test's expected output
never depends on when it happened to run).

**What differ tests prove:** given a previous snapshot and a new one, the
differ emits exactly the domain events the change actually represents — one
test per axis that can change independently, a test for the first poll (no
prior state to diff against), and a test confirming no events fire when
nothing changed. Critically, differ tests are also where this repo proves
its most load-bearing invariant: **absence of evidence is never a state
change** (ADR 0013). A failed or absent facet read must produce zero
events and leave the engine's remembered state untouched — not clear a
fault that never cleared, not report a mode change that never happened.

**What they do not prove:** that the underlying mechanism is exercised for
*every* facet kind. The absence-of-evidence gate lives once, in shared code
(`synth.Engine.Apply`), but only four of the collector's eight registered
differs currently have a test that exercises the failed-read path for that
specific facet: `TestFailedFacetSuspendsDiffing` (signal),
`TestFailedFaultReadNeverClears` (fault), `TestFailedDetectorReadEmitsNothing`
(detector), and `TestDMSFailedReadEmitsNothing` (DMS). The CCTV,
traffic-interval, zone-incident, and zone-interval differs have none. The
shared implementation makes a hidden per-facet bug unlikely, but "the shared
code should handle it" is not the same claim as "a test proves it does" —
see
[known gaps and successor work](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state)
for the tracked follow-up.

## Conformance tests: does the published shape match the profile?

Conformance tests check something goldens and differ tests can't: not "does
this match what we pinned before," but "does this match an external
contract this repo does not control the wording of." Two genuinely different
things carry this name here, and conflating them is the mistake to avoid.

**`internal/app/conformance_test.go`'s `TestTier2ProfileConformance`** boots
the real collector against an embedded, in-process `nats-server` and asserts
every published message obeys the NATS reference profile's Tier 2 rules —
subject token shape, `ce-source` as a well-formed URN, `ce-id` present,
`ce-specversion`, catalog ce-types matching `openits.<service>.<event>.v<n>`,
and so on. The regexes are transcribed from openits-models'
`tools/conformance` harness rather than importing it, because the harness is
a Go program in a module this repo may not import outside `internal/wire`
(ADR 0002). This test proves the bytes a consumer would actually receive off
the wire are profile-shaped — nothing is stubbed below the emitter.

**`internal/wire/health/conformance_test.go`** proves something narrower and
different: that the health emitter's `CETypes()` declaration agrees, in both
directions, with this repo's own `asyncapi.yaml` (`TestCETypesMatchesWhatTheEmitterActuallyEmits`,
`TestAsyncAPICoversEveryEmittedType`, `TestAsyncAPIDocumentsNothingUnemittable`).
Every ce-type the emitter can produce must be documented; every documented
channel must be something the emitter can actually produce; and the
documented payload shape must match the bytes actually emitted. This is a
genuine bidirectional check — it fails on drift in either direction, not
just one.

### The catalog-conformance claim that does not hold

ADR 0008 states the bar for the *catalog* emitter (`internal/wire/openits`):
every ce-type it can produce must exist in the **pinned openits-models
release's own** `asyncapi.yaml` — so a drift between what the collector
claims to emit and what the upstream catalog actually defines is a CI
failure, not a production surprise discovered later.

No such check exists for `internal/wire/openits` today. It is tempting to
read `TestGoldensCoverEveryCEType` (above) as satisfying this — it has the
right shape, an exhaustiveness assertion over ce-types — but read closely,
it iterates `New("x").CETypes()` and checks that set against the emitter's
own `goldenCases`, which is itself built from the emitter's own `ceTypeFor`
routing table. It checks the emitter's mapping table against itself. A
ce-type the pinned openits-models release declares but this emitter never
mapped is invisible to every check in this repo — that gap would only
surface as a runtime silent-drop or a human noticing.

`internal/wire/health/conformance_test.go` is the shape a real catalog check
would take — an emitter's `CETypes()` checked against an external document
in both directions — it just checks against this repo's own local
`asyncapi.yaml` (which documents only the collector-owned
`openits-collector.*` namespace) rather than the pinned models release's
catalog. Building the `internal/wire/openits` equivalent, reading the pinned
release's `bindings/nats/asyncapi.yaml` from the module cache, is tracked
work — see
[known gaps and successor work](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state).

## `-race`: because polling and publishing are concurrent

`go test ./... -race` runs the same suite with Go's race detector attached,
and CI runs it as a separate, required pass alongside `make check`. This
matters here specifically because poll loops and the publisher are not
incidentally concurrent — each configured device polls on its own timer in
its own goroutine, and the publisher drains a shared event channel while
new events keep arriving. A bug that only manifests under concurrent access
(a snapshot mutated while being read, a map written from two goroutines) can
pass every sequential test and still corrupt state or crash in the field.
`-race` is what stands between "the tests pass" and "the tests pass and
nothing raced to get there."

## Doc guards: structural, not semantic

`internal/docs` is a small package of tests, not production code, that keep
specific documentation claims mechanically true. Its own package doc states
the scope precisely: "Only STRUCTURAL claims are checked — a named enforcer
that vanished, a config field that appeared, a subject address that
drifted. Prose accuracy is a review problem and is deliberately not
attempted here: a check that cannot fail on real decay is worse than no
check, because it reads as coverage."

Three guards exist today:

- `TestConfigReferenceDocumentsEveryField` — every `collector.yaml` field
  reachable by reflection over `config.Config` appears in
  `docs/reference/configuration.md`.
- `TestInvariantsTableNamesRealEnforcers` — every enforcer named in
  `docs/reference/invariants.md`'s table (a file path, a `Test...` function,
  or a `make` target wired into `check`) actually exists somewhere in the
  tree.
- `TestAsyncAPIAddressesMatchRenderedSubjects` (plus two sibling tests in the
  same file) — the example subject addresses in `asyncapi.yaml` render
  correctly through the real `internal/subject.Template`.

**What doc guards prove:** a specific, named thing a document asserts (a
field exists, an enforcer exists, an address renders) is still true of the
code today. They turn "the docs might be stale" into "CI would have caught
that."

**What they cannot prove — by design, not oversight:** that a claim is
*relevant*, or that the prose around it is accurate. A doc guard checks
existence and equality; it has no opinion on whether a still-existing
enforcer is the *right* one for the rule, or whether a still-accurate field
list is missing the one paragraph of context that would actually help a
reader. That judgment is a review problem, on purpose — a check that could
never fail on real decay would read as coverage without being coverage.

## Summary: what to reach for

| Layer | Proves | Blind to |
|---|---|---|
| Golden tests | Encoding is stable; a change is visible as a diff | Whether the pinned mapping is semantically correct |
| Fixture replay | The adapter parses what it was handed | Whether what it was handed resembles a real device |
| Differ tests | State transitions are correct, including the absence rule | Only for the facets that have a failed-read case (4 of 8) |
| Conformance tests | Published shape matches an external contract | Only the contract the test actually loads — see the catalog-conformance gap above |
| `-race` | No data race under concurrent polling/publishing | Logic bugs that don't involve concurrent access |
| Doc guards | A specific structural claim still matches the code | Whether the claim is relevant, or the surrounding prose is accurate |

None of these layers is a substitute for another. A golden test cannot catch
a semantically wrong mapping; a doc guard cannot catch stale prose; fixture
replay cannot catch an invented fixture. Knowing which layer you're relying
on — and which question it was never going to answer — is most of what
"the tests pass" is worth telling you.
