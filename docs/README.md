# Documentation hub

Routed by what you're trying to do, not by which tier a document happens to
live in. If your task isn't listed, the rest of this directory is small
enough to skim:

- `tutorial/` (learning) — living; one guided build for someone who has
  never seen the repo. Links rules, never restates them.
- `how-to/` (doing) — living; the canonical steps for each contribution
  shape. The `.claude/skills/` guides are terse pointers at these.
- `explanation/` (understanding) — living; how the pieces fit and why the
  seams fall where they do.
- `reference/` (what) — living; the enforced rules, the config surface, and
  the guarantees a consumer of the bus can rely on.
- `adr/` (why) — decisions, immutable once accepted; only an inaccurate
  status line is ever corrected.
- `specs/` (not built yet) — a staging area for designs whose work has not
  shipped, **not** an archive. A spec is deleted once its design ships and
  its durable content has been promoted into the living tiers.

Implementation plans and probe ledgers are **not** committed to this repo.
Both are working artifacts of building a change — not documentation of the
system that resulted — and are kept outside the tree; this repo's agent
tooling writes them under the git-ignored `.superpowers/`. `specs/` is the
one staging tier that does live here, because a spec targets a reader
deciding whether to build something, not the agent that built it, and it
empties out as that work ships.

| I want to... | Start here |
|---|---|
| See the repo for the first time | [`../README.md`](../README.md) — the one-diagram architecture and current Status |
| Learn the codebase by building something | [`tutorial/build-your-first-adapter.md`](tutorial/build-your-first-adapter.md) — clone to a real event on the bus, in one sitting |
| Add a vendor adapter | [`reference/starter-tasks.md`](reference/starter-tasks.md) for the highest-leverage first PR, [`how-to/add-a-vendor-adapter.md`](how-to/add-a-vendor-adapter.md) for the full guide, then [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) for the step-by-step checklist |
| Add a device-state concept nothing in `sdk/model` represents yet (a new facet) | [`how-to/add-a-domain-facet.md`](how-to/add-a-domain-facet.md), then [`add-domain-facet`](../.claude/skills/add-domain-facet/SKILL.md) |
| Add support for a device that doesn't speak SNMP | [`add-transport`](../.claude/skills/add-transport/SKILL.md) — builds the `sdk/transport/<name>` plumbing, not the adapter itself |
| Map a domain event to a wire ce-type, or adopt a new openits-models release | [`how-to/map-an-event-to-the-wire.md`](how-to/map-an-event-to-the-wire.md) and [`how-to/adopt-an-openits-models-release.md`](how-to/adopt-an-openits-models-release.md), then [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) |
| Record fixtures from a real device | [`how-to/record-fixtures-from-a-device.md`](how-to/record-fixtures-from-a-device.md) — honest stub; the tool doesn't exist yet (successor A) |
| Review an incoming adapter contribution | [`review-adapter-contribution`](../.claude/skills/review-adapter-contribution/SKILL.md) — the machine checks plus the things no check can see |
| Know what will fail my PR | [`reference/invariants.md`](reference/invariants.md) for the rules and what enforces them, [`reference/test-requirements.md`](reference/test-requirements.md) for the testing bar per contribution type |
| Understand why it's built this way | [`adr/README.md`](adr/README.md) — the accepted decision records, in order |
| Understand how the pieces fit together, end to end | [`explanation/architecture.md`](explanation/architecture.md) — the document a newcomer reads first in this tier; it links onward to the other four |
| Consume events the collector publishes | [`reference/consumer-contract.md`](reference/consumer-contract.md) — delivery, duplicates, ordering, and what restart does to the stream |
| Configure a deployment | [`reference/configuration.md`](reference/configuration.md) — every `collector.yaml` field and every command-line flag: type, default, validation |
| Deploy a collector to a fleet | [`how-to/deploy-a-collector.md`](how-to/deploy-a-collector.md) — honest stub; the deploy path isn't built yet (successor B) |
| Know what is already known-broken | [Known gaps and successor work](#known-gaps-and-successor-work) — every open finding the truth pass left behind, and what closing each one involves |

## Other task guides in `.claude/skills/`

- [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) — new vendor × device-kind integrations
- [`add-domain-facet`](../.claude/skills/add-domain-facet/SKILL.md) — new facets, differs, and domain events
- [`add-transport`](../.claude/skills/add-transport/SKILL.md) — a new `sdk/transport/<name>` package for a protocol nothing speaks yet
- [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) — openits-models mappings and release pin bumps
- [`review-adapter-contribution`](../.claude/skills/review-adapter-contribution/SKILL.md) — the maintainer-side checklist for an incoming adapter PR
- [`.claude/skills/README.md`](../.claude/skills/README.md) — the contract new skills are written against

## Known gaps and successor work

This is the tracker for work the documentation truth pass found and did not
close. Where a document stated the intended bar and the code did not meet it,
the rule was **fix the code, never weaken the document** — so the document
stands as written and the gap is open here.

Each entry is written to stand on its own: what is wrong, where in the code,
and what closing it involves. The audit ledger held the original probes and
their output as supporting evidence; no entry below depends on it, and it
has since left this repository — probe ledgers are working artifacts, not
documentation (see the taxonomy note above). Every commit that touched it
is still in history; retrieve it, and resolve any `Evidence: ledger` line
number below against it, with:

```
git show e6e2b1f:docs/notes/2026-08-17-documentation-truth-audit.md
```

### The code does not meet a bar the docs correctly state

**The `openits` emitter has no catalog-conformance check.**
`internal/wire/openits/golden_test.go`'s `TestGoldensCoverEveryCEType` reads
like a conformance test and is not one: it iterates `New("x").CETypes()`,
and `CETypes()` (`internal/wire/openits/emitter.go`) is derived from the
emitter's own local `ceTypeFor` routing table. It therefore proves the golden
set keeps up with the mapping, and nothing about whether the mapping keeps up
with the pinned openits-models catalog. A ce-type the pinned release declares
but the emitter never mapped is invisible to every check in this repo.
`internal/wire/health/conformance_test.go` is the shape to copy — it compares
the health emitter's `CETypes()` against an external document (`asyncapi.yaml`)
in both directions, failing both on an emittable type with no channel and on
a documented channel nothing can emit. *Closing it:* build the equivalent for
`internal/wire/openits`, reading the pinned release's own
`bindings/nats/asyncapi.yaml` from the module cache and asserting both
directions. Evidence: ledger `:1165-1180`, `:3210`.

**46 catalog ce-types are declared upstream and unmapped, 17 of them in
services the collector already serves.** [ADR 0016](adr/0016-collector-as-transitional-shim.md)
makes this a *collector* gap by definition: the collector adapts to the
catalog, so a ce-type the pinned release declares and `internal/wire/openits`
never mapped is work owed here, not evidence the catalog is wrong. Against
the v0.3.0 pin the emitter maps 26 of the catalog's 72 ce-types. Most of the
remainder belong to services the collector does not model at all — `ess`,
`ramp-metering`, `reversible-lane`, `rsu` — and are blocked on a domain
facet and an adapter, not on a mapping. The 17 that are not so blocked sit in
services already wired end to end:

- `openits.traffic-sensor.traffic-sensor-status-report.v1` and
  `openits.traffic-sensor.queue-state-changed.v1`
- three `openits.cctv.*` command/lockout events
  (`ptz-move-commanded`, `ptz-preset-recalled`, `lockout-denied`)
- twelve `openits.signal-control.*` events — phase, overlap, pedestrian,
  coordination, detector-transition, comm-health, TSP and TSAM

Those three groups are not one task. The CCTV ones describe *commands*, and
nothing in the collector commands anything (`sdk/adapter.Commander` has no
dispatch path, the same shape as the `EventReader` gap below). Most of the
signal-control ones are high-rate per-cycle events an SNMP poller structurally
cannot observe — they want a controller pushing, which is the end state
[ADR 0016](adr/0016-collector-as-transitional-shim.md) expects to make the
collector unnecessary. `traffic-sensor-status-report` is the one ADR 0016
names explicitly and the most tractable: it is a periodic state report for a
device kind already modelled, differed and published.

*Closing it:* per ce-type, not in bulk — each needs the domain event, the
`ceTypeFor` entry, the `Encode` case and a golden. Start with
`traffic-sensor-status-report`. The reason the list has to be recomputed by
hand rather than read off a check is the entry above: there is no
catalog-conformance test, so nothing tells you when this list changes.

**Four of the eight registered differs have no failed-read test.** The
absence-of-evidence rule (ADR 0013) is proven for the signal, fault, detector
and DMS differs (`TestFailedFacetSuspendsDiffing`,
`TestFailedFaultReadNeverClears`, `TestFailedDetectorReadEmitsNothing`,
`TestDMSFailedReadEmitsNothing`). The CCTV, traffic-interval, zone-incident
and zone-interval differs have none — `internal/synth/cctv_test.go`,
`internal/synth/trafficsensor_test.go` and `internal/synth/perception_test.go`
contain zero failed-read cases. `Engine.Apply` (`internal/synth/synth.go`)
implements the gate once in shared code, so the blast radius is likely small,
but an untested invariant is untested. *Closing it:* four tests in the shape
of `TestDMSFailedReadEmitsNothing` — a failed poll emits zero events, and the
recovery poll diffs against the pre-failure state rather than a zero value.
Live coverage is tracked in
[`reference/invariants.md`](reference/invariants.md#absence-of-evidence-is-never-a-state-change).
Evidence: ledger `:1442-1460`.

**Synth's previous state does not survive a restart.** `synth.Engine` keeps
`prev[deviceID][kind]` in an in-memory map (`internal/synth/synth.go`), so a
restarted collector diffs against nothing. `internal/synth/fault.go` re-emits
`FaultRaised` for every standing fault — a storm on every rollout restart —
and every other differ silently drops whatever transitioned while the process
was down. Consumers cannot dedupe the re-raise: the ce-id derives from the
snapshot's `SampledAt` (`internal/cloudevents/envelope.go`), so it is a
different id for a genuinely different observation.
[ADR 0017](adr/0017-durable-synth-state.md) decides the fix and nothing
implements it yet. The consumer-side half of ADR 0017 is done: the
idempotency requirement per `(device, fault-id)`, and today's restart
behaviour, are stated in
[`reference/consumer-contract.md`](reference/consumer-contract.md). *Closing
the rest:* write `prev` through to a JetStream KV bucket in the cabinet's own
NATS under a JetStream domain, and seed it at boot before the first poll. The
contractual requirement stays either way — no local store survives a reflash,
and after one the collector will correctly re-raise.

**`ntcip-asc`'s fixtures are hand-written, not recordings.** ADR 0008 requires
recorded raw transport responses; `internal/vendors/ntcip/asc_test.go`'s
`healthyFixture` is a hand-typed `map[string]int64`, and the adapter's own doc
comment states the alarm bitmap has never been validated against a physical
controller. The bar is correct and stays as written; the repo's single adapter
does not meet it. *Closing it:* the fixture recorder and adapter conformance
kit (successor A) — a tool that captures real SNMP responses into `testdata/`
with a sanctioned regeneration path, then re-recording `ntcip-asc` against a
real controller. Evidence: ledger `:194-210`, `:3428-3440`.

**`model_version` selects nothing.** `internal/config/config.go` validates it
non-empty and no other code reads it; `internal/app/app.go` builds
`[]wire.Emitter{openits.New(...), health.NewHealthEmitter()}` unconditionally,
so any non-empty value produces byte-identical behaviour. ADR 0005 and ADR
0010 both describe it as selecting the wire emitter at boot. *Closing it:*
either implement real selection keyed off the value, or validate it against
the wire package's declared version so an unrecognized value fails at boot —
which is what ADR 0014's trust-boundary rule already requires and the current
code does not do. Evidence: ledger `:995-1013`, `:1282-1290`.

**The signal differ has no axis-independence test.** It is the one differ fed
by the collector's only shipped adapter.
`internal/synth/signal_test.go`'s `TestTransitionsEmitChangeEvents` moves
mode, plan and preemption in a single poll and only asserts the expected event
is *present*, never that the others are *absent*. `AGENTS.md` and
[`reference/test-requirements.md`](reference/test-requirements.md) both state
the correct bar. *Closing it:* a `TestSignalAxesChangeIndependently` in the
shape of `TestDMSAxesChangeIndependently` (`internal/synth/dms_test.go`) —
move one axis, assert an exact event count, repeat per axis. Evidence: ledger
`:486-500`.

**`EventReader` has no dispatch path.** `sdk/adapter/adapter.go` declares the
interface and the `CapEvents` capability, but `runner.New`
(`internal/runner/runner.go`) is typed to `adapter.StateReader` specifically,
not a common adapter interface. A fully-implemented `EventReader` adapter
would have nothing in the poll path routing its `Fetch()` output to an
emitter. This is consistent with ADR 0004's "deferred to the first log-shaped
adapter" framing, but the branch the greenfield architecture spec §4 and §6
describe has zero code, and that spec is deleted in Phase 2 — so this is the
only remaining record of it. *Closing it:* widen the runner (or add a second
loop) to dispatch on capability, together with the first `EventReader`
adapter. Evidence: ledger `:2908-2925`.

**ADR 0012's readiness signal and expected-version drift reporting do not
exist.** Nothing in the tree implements a readiness endpoint or signal, and
nothing reports an expected-versus-running version. ADR 0012's Decision
section lists three numbered mechanisms in the present tense; only the first
(the collector never self-updates) is implemented. *Closing it:* fleet-plan
Phase 1 — the readiness signal, broker-absent tolerance, and expected-version
drift reporting, all prerequisites for health-gated rollback. Evidence: ledger
`:1376-1392`.

**ADR 0014's one sanctioned exception — tolerating an absent broker at
startup — is not implemented.** ADR 0014 carves out exactly one narrow
exception to "config is the trust boundary": a broker that is not yet
reachable at boot is a transient, not a config error, so it should retry
rather than fail hard. `publish.Connect` (`internal/publish/publish.go`)
still dials NATS and provisions every stream during boot, and
`internal/app/app.go` returns its error straight out of `Run`, so a broker
that is slow to come up makes the collector exit like any other boot-time
failure. In a cabinet where the collector and the broker start together
that is a race the collector can lose, and under ADR 0012's health-gated
rollback a lost race reads as a failed update. Both the ADR and
[`invariants.md`'s config row](reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized)
say so rather than implying the exception exists. *Closing it:* fleet-plan
Phase 1's broker-absent tolerance — connect in the background and let the
collector reach a running state without a broker, instead of treating the
dial as part of boot validation.

**Unclaimed wire-emitter events drop without a metric.** The bar is stated
on the `wire.Emitter` interface itself (`internal/wire/emitter.go`): an event
no emitter claims "is dropped LOUDLY (metric + log), never silently." It is
half met. `internal/app/app.go`'s `encodeAndPublish` calls `slog.Warn` on the
no-emitter-claimed path but emits no metric, and there is no metrics or
observability library anywhere in the tree (`internal/` and `sdk/` import
neither `prometheus`, `expvar`, nor `otel`).
[`explanation/wire-boundary.md`'s drop rule](explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate)
describes the shortfall accurately rather than restating the bar — it says
the drops are logged but not counted, and points here. The interface comment
is correct and stays as written. *Closing it:* the repo has no metrics
subsystem at all today, so this isn't a one-counter patch — it starts with a
decision about whether the collector should have one, then wiring a counter
into the drop path alongside the existing log line.

### Documents that are stale and were not corrected

**ADR 0003 says a contribution adds `internal/vendors/<vendor>/<kind>/`.** The
actual tree is `internal/vendors/<vendor>/<kind>.go` — a file named for the
device kind, not a subdirectory (`internal/vendors/ntcip/asc.go`). The same
defect was fixed in `README.md` and the adapter skill; the ADR was left alone
because ADRs are immutable and the audit could not tell whether the path was
always illustrative or genuinely wrong. *Deciding it:* either add a status-line
note in the style of ADR 0006's, or accept it as a historical illustration.
Evidence: ledger `:936-940`.

**ADR 0006 and ADR 0009 describe a default subject grammar that ADR 0011
replaced.** ADR 0006's body says "ADR 0009's default reproduces it exactly" and
ADR 0009's says "omitting the config reproduces ADR 0006's scheme
byte-for-byte." Both were true on 2026-07-16 and are false now: ADR 0011
replaced the five-token `openits.<agency>.<site>.<service>.<event>.v<n>`
grammar with the seven-token namespace-rooted `DefaultTemplate` in
`internal/subject/subject.go`. Both **Status** lines now say so explicitly;
both bodies stand as written under the immutability convention. Nothing
further is required unless the convention changes. Evidence: ledger
`:1041-1044`, `:1220-1224`.

### A guard that was deliberately not built

**No duplicate-prose lint exists.** Across the documentation effort, a
document reproducing prose that already exists elsewhere was caught seven
separate times in review — the "link, don't restate" discipline
([stated in `reference/invariants.md`'s preamble](reference/invariants.md#invariants))
held only because a reviewer caught each one by hand. A shingling check
across the docs tree would catch this class mechanically, and is the
natural fifth guard alongside the four `make check` already runs
([inventoried in `explanation/testing-strategy.md`](explanation/testing-strategy.md#doc-guards-structural-not-semantic)):
three `internal/docs` tests carrying five `Test` functions between them,
plus `scripts/lint-docs.sh`, which is one guard running two structural
checks (link/anchor resolution and skill structure). It was deliberately not built
during Phase 2: a version with an acceptable false-positive rate is real
design work in its own right — fenced code blocks legitimately repeat
identifiers verbatim, some
honesty items (the `ntcip-asc` fixture-provenance gap, for instance) are
*meant* to appear in the tutorial, the how-to, and this known-gaps list
because each is read by someone who won't have seen the other two, and
short common phrases (rule names, file paths) trip any naive n-gram
threshold that doesn't also weight by rarity. *Closing it:* design the
false-positive filter first — shingle length tuned above common-phrase
length, an allowlist for fenced code and the known-intentional repeats —
then add it as the fifth `make check` guard — `lint-docs.sh` is the natural
home for it, as a third structural check alongside the two it already runs.

### Unresolved because it could not be verified here

**ADR 0001's "gen-1 code is deleted (git history preserves it)" cannot be
confirmed from this repository.** No `gen2` branch exists locally or on the
remote, and history begins at a deliberately squashed public-baseline commit,
so whatever preserves the gen-1 code is not reachable from this clone. Recorded
as blocked rather than false. *Resolving it:* check whether an internal
predecessor repository holds the pre-launch history, and either confirm the
claim or correct the ADR's status line. Evidence: ledger `:831-840`.
