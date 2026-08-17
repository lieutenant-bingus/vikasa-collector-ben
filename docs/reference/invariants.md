# Invariants

Four architectural rules used to be restated 28 times across 14 files, and
most of the copies drifted from what the code actually does. **This is now
the only document permitted to restate an enforced rule.** Every other
document — README, AGENTS.md, package comments, specs — links to a row
here instead of paraphrasing the rule itself. If you find yourself about
to write "never import openits-models outside internal/wire" anywhere but
this file, link this file instead.

Each row names an **enforcer**: something that actually fails if the rule
is broken, not just a place the rule is written down. `internal/docs`'s
`TestInvariantsTableNamesRealEnforcers` parses this table on every `go
test ./...` run and fails if a named enforcer doesn't exist — a path that's
gone missing, or a `Test...` function nobody can find. That test cannot
check that an enforcer actually *enforces* the rule (that's a review
problem), but it can and does check that the table isn't naming ghosts.

A rule with no automated enforcer is marked `Review (manual)` rather than
left blank, so the gap is visible instead of implied.

| Rule | Decided by | Enforced by |
|---|---|---|
| Adapters and `sdk/` never import openits-models | ADR 0002 | `scripts/lint-boundary.sh` |
| openits-models is pinned at main HEAD; never a `replace` directive | ADR 0010 | `scripts/lint-boundary.sh` |
| Absence of evidence is never a state change | ADR 0013 | `internal/synth/synth.go`, `TestFailedFacetSuspendsDiffing`, `TestFailedFaultReadNeverClears`, `TestFailedDetectorReadEmitsNothing`, `TestDMSFailedReadEmitsNothing` |
| Config is the trust boundary; boot fails on the unrecognized | ADR 0014 | `internal/config` |
| Subjects are operator-configurable; the CloudEvents envelope stays canonical | ADR 0009, ADR 0011, ADR 0015 | `internal/subject`, `internal/cloudevents` |
| Every mapped ce-type has a byte-exact golden | ADR 0008 | `internal/wire/openits/golden_test.go` |
| No fixtures, no merge | ADR 0008 | Review (manual) — automated by the conformance kit in successor A |
| Every guard must be shown to fail | Practice (no ADR; promoted here) | `scripts/lint-boundary.sh` |

## Adapters and `sdk/` never import openits-models

Decided by [ADR 0002](../adr/0002-domain-model-and-wire-emitter-boundary.md).

Violating this looks like an adapter (`internal/vendors/...`) or an `sdk/`
package reaching for an openits-models type directly instead of returning
`sdk/model` and letting `internal/wire` do the mapping — even transitively,
through a helper package that itself imports the wire model.
`scripts/lint-boundary.sh` checks both the transitive case (Rule A: does
`sdk/...` or `internal/vendors/...` reach the wire model by any import
path at all) and the direct case (Rule B: does any package outside
`internal/wire` import it directly), and `make check` runs it on every CI
build.

## openits-models is pinned at main HEAD; never a `replace` directive

Decided by [ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md).

Violating this looks like a `replace github.com/Vikasa2M/openits-models =>
...` line in `go.mod` — the shortcut every dependency-pinning problem
tempts you toward when the two repos are moving in lockstep pre-v1.
`scripts/lint-boundary.sh`'s Rule C checks `go.mod` for exactly that and
fails the build if it finds one.

## Absence of evidence is never a state change

Decided by [ADR 0013](../adr/0013-absence-of-evidence.md).

Violating this looks like a differ that treats a failed or absent facet
read as if the device reported a new value — clearing a fault that never
cleared, or reporting a mode change that never happened, because the poll
came back empty instead of wrong. The mechanism lives in
`synth.Engine.Apply` (`internal/synth/synth.go`): a facet that failed to
read is simply never present in `Snapshot.Facets`, so its differ is never
invoked and the engine's remembered previous state survives untouched.

**This enforcement is partial, and the table says so rather than implying
otherwise.** Only 4 of the collector's 8 registered differs have a test
proving the failed-read path actually holds for them:
`TestFailedFacetSuspendsDiffing` (signal), `TestFailedFaultReadNeverClears`
(fault), `TestFailedDetectorReadEmitsNothing` (detector), and
`TestDMSFailedReadEmitsNothing` (DMS). CCTV, traffic-sensor, zone-incident,
and zone-interval do not have one yet — the shared `Apply` mechanism almost
certainly covers them too, but "almost certainly" is not "proven," and
writing the missing four tests is tracked as a successor work item rather
than papered over here.

## Config is the trust boundary; boot fails on the unrecognized

Decided by [ADR 0014](../adr/0014-config-is-the-trust-boundary.md).

Violating this looks like a bad `collector.yaml` value that gets accepted
at boot and surfaces later as an unroutable event, mislabeled provenance,
or silently wrong data discovered downstream — instead of a boot failure
at the moment the mistake was made. `internal/config`'s `Config.validate`
rejects an unknown vendor/device-kind, tenant tokens that would corrupt
subject grammar or the source URN, a subject template that can never yield
a static stream binding, a malformed `collector_id`, a missing
`model_version`, zero devices, a duplicate or missing device ID, and a
negative `poll_interval`.

ADR 0014 names one sanctioned exception — tolerating an absent broker at
startup as transient rather than a config error — but that exception is
**planned, not implemented**. `publish.Connect`
(`internal/publish/publish.go`) still dials NATS and provisions streams
during boot today, so a broker that is slow to come up currently makes the
collector exit, same as any other boot-time failure. Nothing in the code
carves out the exception yet.

## Subjects are operator-configurable; the CloudEvents envelope stays canonical

Decided by [ADR 0009](../adr/0009-configurable-subject-templates.md),
[ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md), and
[ADR 0015](../adr/0015-ce-source-urn-scheme.md).

Violating this looks like deriving `ce-type`, `ce-source`, or `ce-id` from
the operator's subject template, or deriving the subject from the
envelope — either direction collapses two things that are deliberately
independent. Subject grammar belongs to the operator: agencies fit the
collector into namespaces they already own (`internal/subject`, template
validated by `Config.validate`). The envelope stays canonical regardless
of local routing choices: `ce-type` is catalog-verbatim, `ce-source` is the
fixed profile URN `urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>`
built by `SourceFor`, and `ce-id` is deterministic — built in
`internal/cloudevents`. The `ce-id` derivation itself is openits-models'
contract, not this repo's; see its `ce-id-spec.md`. Deterministic does not
mean "a bare content hash."

## Every mapped ce-type has a byte-exact golden

Decided by [ADR 0008](../adr/0008-fixture-golden-testing-bar.md).

Violating this looks like a domain event that encodes to different bytes
than a prior release did — a silent wire-format drift no reviewer would
catch by reading the diff. `internal/wire/openits/golden_test.go`'s
`TestGoldens` encodes one fixture per mapped ce-type at a fixed timestamp
and compares against recorded exact bytes; `TestGoldensCoverEveryCEType`
separately checks that every ce-type the pinned models release declares
has a fixture, so a newly-mapped ce-type can't quietly ship without one.

## No fixtures, no merge

Decided by [ADR 0008](../adr/0008-fixture-golden-testing-bar.md).

Violating this looks like an adapter PR landing with hand-typed fixtures
instead of recorded raw transport responses — which is, today, the actual
state of the collector's one adapter (`ntcip-asc`'s `healthyFixture` is a
hand-written `map[string]int64`, not a captured recording; see its own
doc comment on the alarm bitmap for why that is a real gap, not a
formality). There is no automated check for this today — it is enforced
by PR review, marked `Review (manual)` above rather than left blank. A
recording tool and conformance kit are tracked as successor A's work; once
it exists, this row's enforcer cell should name it instead of `Review
(manual)`.

## Every guard must be shown to fail

Decided by: no ADR names this rule; it was invoked by name ("the standing
rule") in a design spec that cited nothing, and promoted here rather than
left homeless when that spec was retired.

The rule: a validation or lint that has only ever been observed to pass is
indistinguishable from one that can never fail — a check that has never
been seen to catch anything is not known to be a check. Violating this
looks like adding a guard (a lint rule, a boot-time validation) and never
writing the test that deliberately trips it, so a future refactor could
silently defang the guard and nothing would notice. `scripts/lint-boundary.sh`
is built specifically to make this testable: `LINT_FORBIDDEN` and
`LINT_GOMOD` are override hooks that let a test point the same rule at a
fixture guaranteed to violate it. `make lint-boundary-selftest` points
Rule A at `gosnmp`, a dependency `sdk/` genuinely has, and fails the build
unless the lint flags it; `make lint-boundary-replace-selftest` points
Rule C at a throwaway `go.mod` fixture that does carry a `replace`
directive, and fails the build unless the lint flags that too. Both run as
part of `make check`, alongside the lint itself.
