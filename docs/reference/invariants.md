# Invariants

The rules CI enforces, stated once. **This file is the canonical wording**:
each row below is the version that is kept true and checked. Anywhere else
that needs a rule links its section here instead of restating it, so
copies cannot drift from the code.

`docs/tutorial/` is the one deliberate exception. A tutorial that only
linked out at the moment a rule matters would fail its job, so it states
the rule it is teaching in its own words and links here for the canonical
wording.

Each row names an **enforcer**: something that actually fails when the
rule is broken, not just a place the rule is written down.
`TestInvariantsTableNamesRealEnforcers` (`internal/docs`) parses this table
on every `go test ./...` and fails if a named enforcer does not exist — a
path that has gone missing, a `Test...` function nobody defines, or a
`make` target that is not a prerequisite of `make check`. It cannot tell
whether an enforcer actually *enforces* the rule; that is a review
question. A rule with no automated enforcer is marked `Review (manual)`,
so the gap is visible rather than implied.

| Rule | Decided by | Enforced by |
|---|---|---|
| Adapters and `sdk/` never import openits-models | ADR 0002 | `scripts/lint-boundary.sh` |
| The openits-models pin carries no `replace` directive | ADR 0010 | `scripts/lint-boundary.sh` |
| The openits-models pin names a release tag | ADR 0018 | `scripts/lint-boundary.sh` |
| The pinned release is a current one, not an old tag | ADR 0018 | Review (manual) |
| Absence of evidence is never a state change | ADR 0013 | `internal/synth/synth.go`, `TestFailedFacetSuspendsDiffing`, `TestFailedFaultReadNeverClears`, `TestFailedDetectorReadEmitsNothing`, `TestDMSFailedReadEmitsNothing`, `TestDMSEnvFailedReadEmitsNothing`, `TestCCTVFailedReadEmitsNothingAndRecoveryDiffsAgainstPreFailure`, `TestZoneIncidentFailedReadNeitherClearsNorRedetects`, `TestTrafficIntervalFailedReadDoesNotDuplicateTheReport`, `TestZoneIntervalFailedReadDoesNotDuplicateTheReport`, `TestGateFailedReadEmitsNothingAndRecoveryDiffsAgainstPreFailure` |
| Config is the trust boundary; boot fails on the unrecognized | ADR 0014 | `internal/config`, `TestRunExitCodes` |
| The broker credential comes from a file, never the URL | ADR 0022 | `internal/publish/security.go`, `TestValidateSecurity`, `TestCredentialsInTheURLAreRefusedUnlogged`, `TestStartErrorNeverContainsCredentials` |
| Plaintext NATS only on loopback; TLS is always verified | ADR 0022 | `TestValidateSecurity`, `TestTLSBrokerIsVerifiedAgainstTheNamedCA` |
| The documented least-privilege broker grant is sufficient | ADR 0022 | `TestLeastPrivilegeGrantIsSufficient`, `TestDeployGuideGrantMatchesTheTestedGrant` |
| The management surface answers only on loopback | ADR 0022 | `TestValidateAddrIsLoopbackOnly`, `TestOnlyLoopbackHostHeadersAreServed`, `TestRunRejectsNonLoopbackStatusAddr` |
| Subjects are operator-configurable; the CloudEvents envelope stays canonical | ADR 0009, ADR 0011, ADR 0015 | `scripts/lint-boundary.sh` (Rule E), `internal/config` |
| openits-models is not reshaped to suit the collector | ADR 0016 | Review (manual) |
| Every mapped ce-type has a byte-exact golden | ADR 0008 | `internal/wire/openits/golden_test.go` |
| The catalog stream passes the openits-models Tier 2 harness | openits-models NATS profile; ADR 0011 | `TestCollectorStreamPassesTheTier2Harness`, `TestEveryHarnessCheckIsRunOrRuledOut` |
| `asyncapi.yaml` documents exactly the collector-owned ce-types | ADR 0007 | `TestAsyncAPICoversEveryEmittedType`, `TestAsyncAPIDocumentsNothingUnemittable`, `TestAsyncAPIAddressesMatchRenderedSubjects` |
| No fixtures, no merge | ADR 0008 | Review (manual) — recordings come from `cmd/collector/record.go`, and a recording without provenance fails to load (`TestReadRejects`) |
| Every guard must be shown to fail | Practice (no ADR; promoted here) | `make lint-boundary-selftest`, `make lint-boundary-replace-selftest`, `make lint-boundary-tag-selftest`, `make lint-boundary-independent-selftest` |

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

## The openits-models pin carries no `replace` directive

Decided by [ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md).

Violating this looks like a `replace github.com/Vikasa2M/openits-models =>
...` line in `go.mod` — the shortcut every dependency-pinning problem
tempts you toward when the two repos are moving in lockstep pre-v1.
`scripts/lint-boundary.sh`'s Rule C checks `go.mod` for exactly that and
fails the build if it finds one.

## The openits-models pin names a release tag

Decided by [ADR 0018](../adr/0018-tagged-model-pins.md), which supersedes
[ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md)'s pinning clause.

Violating this looks like `go.mod` carrying a pseudo-version — the
`v0.2.3-0.20260807005833-235e8780f44c` shape that `go get @main` produces —
instead of a release tag. `scripts/lint-boundary.sh`'s Rule D matches that
shape and fails the build, and `make check` runs it on every CI build.

This is a separate rule from "no `replace` directive," not a restatement of
it: Rule C checks that the dependency is not redirected at a local checkout,
Rule D checks that it names a release rather than a branch commit. A pin can
violate either without violating the other.

The rule this replaces was "the pin is main HEAD, not a stale tag," and it
was marked `Review (manual)`. It did not hold — the pin sat two releases
behind, past a breaking change, at a commit that was a CI dependency bump in
the models repo rather than a models change at all. ADR 0018's Context has
the account. The relevant part here is *why* review could not catch it: a
stale branch pin has no observable moment of violation, so there was nothing
for a reviewer to see on any PR that did not touch `go.mod`. Rule D exists
because the shape of a version string is something a machine can check and a
reviewer reliably will not.

## The pinned release is a current one, not an old tag

Decided by [ADR 0018](../adr/0018-tagged-model-pins.md).

This is the residue Rule D cannot cover, kept as its own row rather than
folded into the one above, because the enforcement status genuinely differs.
Violating this looks like `go.mod` naming a real, valid tag that is several
releases old, while the catalog the rest of the fleet assumes has moved on.
Rule D passes cleanly on exactly that — it checks the version string's
*shape*, never its recency.

Nothing in CI catches it, and deliberately so: answering "is this the newest
release" requires the network, which would make the build irreproducible and
would hand the decision of when to adopt a release to whoever merged it
upstream. Marked `Review (manual)` rather than left implied by the row above.
What makes it tractable now, where the branch-tracking version was not, is
that a release is a dated artefact with a changelog — and
`.github/dependabot.yml` already treats openits-models as an ordinary gomod
dependency, so a new release surfaces as a pull request after its cooldown
rather than depending on someone remembering to look.

## Absence of evidence is never a state change

Decided by [ADR 0013](../adr/0013-absence-of-evidence.md).

Violating this looks like a differ that treats a failed or absent facet
read as if the device reported a new value — clearing a fault that never
cleared, or reporting a mode change that never happened, because the poll
came back empty instead of wrong. The mechanism lives in
`synth.Engine.Apply` (`internal/synth/synth.go`): a facet that failed to
read is never present in `Snapshot.Facets`, so its differ is never invoked
and the engine's remembered previous state survives untouched. `Apply`
does not rely on adapters getting that right: a facet the snapshot also
lists in `Snapshot.Errors` is skipped (`TestApplyIgnoresAFacetItsSnapshotReportsFailed`),
and so is a facet whose Go type is not its differ's
(`TestApplyRejectsAWrongTypedFacet`) — neither is evidence.

**Every registered differ has a failed-read test** — the ten named in the
table row above. The four in `internal/synth/failed_read_test.go`, and
`TestGateFailedReadEmitsNothingAndRecoveryDiffsAgainstPreFailure`, go one
step further than "the failed poll emitted nothing": each recovery poll is
chosen so that an engine which forgot the pre-failure state would emit
something different (a transition with the wrong `From`/`PreviousPosition`,
a re-detected incident, a duplicate interval report). That distinction
matters because silence on the failed poll alone cannot tell a correct
engine from one that wiped its memory — and a mutation that makes `Apply`
forget state on a failed read fails those recovery-shaped tests.

## Config is the trust boundary; boot fails on the unrecognized

Decided by [ADR 0014](../adr/0014-config-is-the-trust-boundary.md).

Violating this looks like a bad `collector.yaml` value that is accepted at
boot and surfaces later as an unroutable event, mislabelled provenance, or
silently wrong data downstream — instead of a boot failure at the moment
the mistake was made.

`config.Load` decodes strictly: an unknown key anywhere outside a device's
`connection` block, or a second YAML document, fails boot (a misspelled
`poll_interval` would otherwise poll at the default). `Config.validate`
then rejects tenant tokens that would corrupt subject grammar or the
`ce-source` URN, a `heartbeat_interval` under 10s, an unknown
`log_ship_level`, a `model_version` outside `wire.SupportedModelVersions`,
a malformed `collector_id`, a subject template that can never yield a
static stream binding, zero devices, a missing or duplicate device id, an
unregistered vendor/device-kind pair, and a negative `poll_interval`. At
the start of `app.Run`, every ce-type is rendered for every device through
the subject template and must land inside its stream's binding. The
`connection` block is opaque to the core, so each adapter applies the same
rule to its own keys: `ntcip`'s `parseSNMPBlock`
(`internal/vendors/ntcip/register.go`) and `cameleon`'s `parseACSBlock`
(`internal/vendors/cameleon/register.go`) reject unknown keys and known
keys of the wrong type. [The configuration
reference](configuration.md#fields) lists every check.

The command line is held to the same rule: an unknown flag or an
unrecognized flag value is a usage error, exit code 2, never a silent
default (`TestRunExitCodes`, `cmd/collector/run_test.go`).

ADR 0014 names one sanctioned exception — an absent broker at startup is
transient, not a config error — and
[ADR 0020](../adr/0020-broker-absence-is-latency.md) implements it:
`publish.Start` (`internal/publish/publish.go`) connects in the background
and never fails on an absent broker, so the collector boots, polls, and
queues until NATS appears
(`TestRunBootsWithoutABrokerAndPublishesWhenOneArrives`). Bad config
still fails hard; only the missing peer is waited for.

## The broker credential comes from a file, never the URL

Decided by [ADR 0022](../adr/0022-transport-security-and-credentials.md),
decision 1.

Violating this looks like a `-nats` URL carrying `user:pass@` or a bare
`token@` — a secret then visible in the process table, shell history and
unit files, and quoted back in parser errors. `ValidateSecurity`
(`internal/publish/security.go`) refuses any URL with userinfo at boot
(exit code 2); the only way to pass a credential is `-nats-creds`, a NATS
`.creds` file that must grant nothing to other users. `TestValidateSecurity`
covers each case and checks no error message repeats the secret;
`TestCredentialsInTheURLAreRefusedUnlogged` and
`TestStartErrorNeverContainsCredentials` check the same for logs and
`publish.Start`'s errors.

## Plaintext NATS only on loopback; TLS is always verified

Decided by [ADR 0022](../adr/0022-transport-security-and-credentials.md),
decision 2.

Violating this looks like the collector publishing in plaintext to a broker
on another host, or accepting a TLS broker without verifying its
certificate. `ValidateSecurity` refuses a non-loopback `-nats` that is not
`tls://` at boot (`TestValidateSecurity`), and no flag skips verification:
the broker is checked against `-nats-ca` or, without it, the system roots
(`TestTLSBrokerIsVerifiedAgainstTheNamedCA`).

## The documented least-privilege broker grant is sufficient

Decided by [ADR 0022](../adr/0022-transport-security-and-credentials.md),
decision 3.

Violating this looks like a collector change that needs a broker permission
the [deploy guide](../how-to/deploy-a-collector.md)'s grant does not
include — a new subject, stream or bucket — so a correctly configured
cabinet starts failing with permission errors.
`TestLeastPrivilegeGrantIsSufficient` (`internal/app/permissions_test.go`)
runs the collector against a broker that grants only that set, and fails
on any permission violation while it provisions and writes both streams
and creates, loads and writes the `VIKASA_SYNTH_STATE` bucket.

The test holds its own copy of the grant, so
`TestDeployGuideGrantMatchesTheTestedGrant` (`internal/app/grant_doc_test.go`)
holds the deploy guide's two copies — the grant table and the `nsc`
command — to exactly that set, in both directions. A subject the collector
needs that the guide leaves out fails it, and so does one the guide grants
that the tested set does not.

## The management surface answers only on loopback

Decided by [ADR 0022](../adr/0022-transport-security-and-credentials.md),
decision 6.

The surface (`/livez`, `/readyz`, `/status`) is unauthenticated by design.
Violating this looks like it being reachable from the cabinet network, or
from a web page through DNS rebinding. `manage.ValidateAddr` refuses a
non-loopback `-status-addr` at boot (`TestValidateAddrIsLoopbackOnly`,
`TestRunRejectsNonLoopbackStatusAddr`), and the handler answers `421` to
any request whose `Host` is not `localhost` or a loopback IP
(`TestOnlyLoopbackHostHeadersAreServed`).

## Subjects are operator-configurable; the CloudEvents envelope stays canonical

Decided by [ADR 0009](../adr/0009-configurable-subject-templates.md),
[ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md), and
[ADR 0015](../adr/0015-ce-source-urn-scheme.md).

Violating this looks like deriving `ce-type`, `ce-source` or `ce-id` from
the operator's subject template, or deriving the subject from the
envelope. Either direction collapses two things that are deliberately
independent. Subject grammar belongs to the operator, so agencies can fit
the collector into namespaces they already own (`internal/subject`,
validated at boot). The envelope stays canonical whatever the local
routing: `ce-type` is catalog-verbatim, `ce-source` is the fixed profile
URN `urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>` built
by `SourceFor`, and `ce-id` is deterministic (`internal/cloudevents`). The
`ce-id` derivation is openits-models' contract, defined in its
`ce-id-spec.md`; deterministic does not mean "a bare content hash."

**Rule E of `scripts/lint-boundary.sh` keeps the two packages apart:**
neither `internal/subject` nor `internal/cloudevents` may reach the other
by any import path, test imports included, so neither can compute its
output from the other's. They meet only in packages that use both, such as
`internal/publish` and `internal/app`, and what those do with the two is
still a review question: the lint sees imports, not data flow.

`internal/config` covers one narrower point. `subject.New`
enforces only the permissive NATS-token rule (no `.`, `*`, `>` or
whitespace) and relies on `config.Load` having already run
`cloudevents.Tenant.Validate`'s stricter `^[a-z0-9][a-z0-9-]*$`. A
`*config.Config` built without `config.Load` could therefore reach
`subject.New` with a value that is a legal subject token but breaks the
URN. `internal/config` is cited because it is the gate that closes that
gap — not as an enforcer of the broader "never derive one from the other"
rule.

## openits-models is not reshaped to suit the collector

Decided by [ADR 0016](../adr/0016-collector-as-transitional-shim.md).

The collector is a transitional shim: it exists because field devices do not
speak openits-models yet, and it is built to be deleted once they do. The
catalog outlives it. So the accommodation runs one way — the collector adapts
to the catalog, never the reverse.

Violating this looks like proposing a ce-type, a field, or an enum member
upstream whose only justification is that a *poller* finds it convenient. The
tell is the argument's shape: "the collector has nowhere to put this" is not
a reason for the catalog to grow. "Every DMS in the field has this state and
the catalog cannot express it" is. The first adds a permanent artefact shaped
by a temporary component; the second is a real domain gap that would exist
whether or not this repository did.

The corollary is the half that generates work here. A ce-type the catalog
*does* declare and the emitter never mapped is a **collector** gap, and
closing it is collector work — not evidence the catalog is wrong. The
[gap list](../README.md#known-gaps-and-successor-work) tracks the ones known
today.

No automated check can see this: it is a rule about the *reasoning* behind a
change, and it mostly applies to a pull request opened against a different
repository. Marked `Review (manual)`, and it is the question to ask whenever
a mapping has nowhere to go — the `wire-emitter` skill's drop-versus-degrade
step is where it usually surfaces.

## Every mapped ce-type has a byte-exact golden

Decided by [ADR 0008](../adr/0008-fixture-golden-testing-bar.md).

Violating this looks like a domain event that encodes to different bytes
than a prior release did — a silent wire-format drift no reviewer would
catch by reading the diff. `internal/wire/openits/golden_test.go`'s
`TestGoldens` encodes one fixture per mapped ce-type at a fixed timestamp
and compares against recorded exact bytes; `TestGoldensCoverEveryCEType`
separately checks that every ce-type **the emitter maps** has a fixture, so
a newly-mapped ce-type can't quietly ship without one.

**What that second test does not do.** It iterates `New("x").CETypes()`,
and `CETypes()` (`internal/wire/openits/emitter.go`) returns the values of
the emitter's own local `ceTypeFor` routing table. The pinned openits-models
release is never consulted, so the check is self-referential with respect to
the catalog: it proves the goldens keep up with the mapping, and nothing
about whether the mapping keeps up with the catalog. A ce-type the pinned
release declares but the emitter never mapped is invisible to it.

The catalog itself is checked separately:
`internal/wire/openits/catalog_test.go` compares the emitter's `CETypes()`
against the pinned release's own `bindings/nats/asyncapi.yaml` in both
directions, with an `unmapped` allowlist (each entry carrying a reason) for
catalog ce-types the emitter consciously does not produce. See
[`testing-strategy.md`'s catalog-conformance section](../explanation/testing-strategy.md#the-catalog-conformance-check).

## The catalog stream passes the openits-models Tier 2 harness

Decided by openits-models' NATS reference profile, whose Tier 2 is the
envelope-and-subject conformance level; the collector targets it
([ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md) is why
collector-owned events sit on their own root, outside what the harness
judges).

Violating this looks like catalog events that encode correctly but break
the profile's envelope or subject rules — a subject outside the seven-token
`openits.` shape, a malformed ce-type or `ce-source` URN, a missing
`ce-id`, or a per-service event shape the harness checks.
`TestCollectorStreamPassesTheTier2Harness`
(`internal/wire/tier2`) runs the real collector against an embedded
`nats-server`, with scripted signal-controller, sign, camera,
traffic-sensor and perception devices, and runs
openits-models' own Tier 2 checks (`tools/conformance/tests`, imported
from the pinned release rather than transcribed) over everything published
on `openits.>`. It runs on every `go test ./...` and needs no network or
external broker. It judges headers and subjects, not payload bytes — those
are locked by the goldens above. The test names each harness check it runs,
and fails if the pinned release no longer has one.
`TestEveryHarnessCheckIsRunOrRuledOut` covers the other direction: every
check the pinned release defines for those kinds is either run or ruled out,
either as judging the device's state tree or as needing an event the
collector does not publish. So a check a new release adds fails until
someone decides which it is.

## `asyncapi.yaml` documents exactly the collector-owned ce-types

Decided by [ADR 0007](../adr/0007-collector-owned-health-schema.md).

The collector-owned `openits-collector.*` events are defined here, not in
openits-models, so this repo's [`asyncapi.yaml`](../../asyncapi.yaml) is
their only contract. Violating this looks like the file and the emitter
disagreeing: a ce-type emitted but undocumented, a documented one nothing
emits, a payload key that differs from the bytes on the wire, or an
address that is not what the default template renders.
`TestAsyncAPICoversEveryEmittedType` and
`TestAsyncAPIDocumentsNothingUnemittable`
(`internal/wire/health/conformance_test.go`) check the first three in both
directions; `TestAsyncAPIAddressesMatchRenderedSubjects`
(`internal/docs/asyncapi_test.go`) checks the addresses.

## No fixtures, no merge

Decided by [ADR 0008](../adr/0008-fixture-golden-testing-bar.md).

An adapter's read-path tests replay recorded raw transport responses from a
real device. Violating this looks like an adapter PR whose fixtures were
typed by hand from a specification rather than captured from a device, so
the tests prove the adapter agrees with its author, not with the hardware.

The ntcip adapters show both sides. `ntcip-dms`'s fixtures are transcribed
from SNMP GETs against a real sign controller, and its test file says which
one and when. `ntcip-asc`'s `healthyFixture`
(`internal/vendors/ntcip/asc_test.go`) is a hand-typed
`map[string]int64`, and the adapter's own doc comment says its alarm-bitmap
table has never been validated against a physical controller — a tracked
[known gap](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state),
not a precedent.

`collector record-fixture` records a device session into a fixture file
whose provenance (make, model, firmware, date) is required for it to load
at all — see
[`record-fixtures-from-a-device.md`](../how-to/record-fixtures-from-a-device.md).
That makes a recording checkable, but nothing can stop a PR from replaying
a hand-typed map instead, or prove a file came from the device its
provenance names. So the rule itself stays enforced in PR review, and
marked `Review (manual)`: the reviewer's question becomes "where is the
recording?", which the recorder makes cheap to answer.

## Every guard must be shown to fail

Decided by practice, not an ADR: a rule this project follows by name, kept
here so it has a canonical home.

A check that has only ever been seen to pass is indistinguishable from one
that can never fail. Violating this looks like adding a guard — a lint
rule, a boot-time validation, a conformance test — without a test that
deliberately trips it, so a later refactor can silently defang the guard.

For a validation or a Go test, the tripping test lives beside it: each
boot check has a case that feeds it a bad value, and the failed-read tests
under [absence of evidence](#absence-of-evidence-is-never-a-state-change)
pick recovery polls that a forgetful engine would get wrong. For the
boundary lint, the guards-of-the-guard are four Makefile targets, each a
prerequisite of `make check`:

- `make lint-boundary-selftest` points Rule A at `gosnmp`, a dependency
  `sdk/` genuinely has, and fails unless the lint flags it;
- `make lint-boundary-replace-selftest` points Rule C at a throwaway
  `go.mod` that carries a `replace` directive;
- `make lint-boundary-tag-selftest` points Rule D at a `go.mod` pinning
  the exact pseudo-version this repo once carried;
- `make lint-boundary-independent-selftest` points Rule E at
  `internal/publish` and `internal/subject`, a pair that genuinely
  depends.

None accepts a bare nonzero exit as proof: `lint-boundary.sh` also exits
non-zero when `go list` fails or `LINT_GOMOD` is unreadable, so each target
greps for its own rule's violation message and fails if the lint failed for
any other reason. All four drive the script through its
`LINT_FORBIDDEN`/`LINT_GOMOD`/`LINT_INDEPENDENT` override hooks; the script is what they
test, so the row names the targets, not the script.
`TestInvariantsTableNamesRealEnforcers` checks that each named target is
still wired into `check:`, because a guard CI never runs is as unenforced
as one that does not exist.
