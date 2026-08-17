# Documentation hub

Routed by what you're trying to do, not by which tier a document happens to
live in. If your task isn't listed, the rest of this directory is small
enough to skim:

- `adr/` (why) — decisions, immutable once accepted; only an inaccurate
  status line is ever corrected.
- `reference/` (what) — living; the enforced rules and the config surface.
- `specs/` (not built yet) — a staging area for designs whose work has not
  shipped, **not** an archive. A spec is deleted once its design ships and
  its durable content has been promoted into the living tiers.
- `plans/` (in flight) — how a shipped-or-shipping design is being executed.
  Deleted when the work ships.
- `notes/` (evidence) — probe ledgers: what was verified, when, against what
  version. Immutable, never edited, never deleted. Evidence does not go
  stale, so a ledger is not a tracker — findings that need action are
  collected below.

| I want to... | Start here |
|---|---|
| See the repo for the first time | [`../README.md`](../README.md) — the one-diagram architecture and current Status |
| Add a vendor adapter | [`reference/starter-tasks.md`](reference/starter-tasks.md) for the highest-leverage first PR, then [`.claude/skills/add-vendor-adapter/`](../.claude/skills/add-vendor-adapter/SKILL.md) for the step-by-step workflow |
| Know what will fail my PR | [`reference/invariants.md`](reference/invariants.md) for the rules and what enforces them, [`reference/test-requirements.md`](reference/test-requirements.md) for the testing bar per contribution type |
| Understand why it's built this way | [`adr/README.md`](adr/README.md) — the accepted decision records, in order |
| Configure a deployment | [`reference/configuration.md`](reference/configuration.md) — every `collector.yaml` field: type, default, validation |
| Know what is already known-broken | [Known gaps and successor work](#known-gaps-and-successor-work) — every open finding the truth pass left behind, and what closing each one involves |

## Other task guides in `.claude/skills/`

- [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) — new vendor × device-kind integrations
- [`add-domain-facet`](../.claude/skills/add-domain-facet/SKILL.md) — new facets, differs, and domain events
- [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) — openits-models mappings and release pin bumps
- [`.claude/skills/README.md`](../.claude/skills/README.md) — the contract new skills are written against

A tutorial, four how-to guides, and an explanation tier promoted from the
designs still staged in `specs/` are tracked as follow-on work and will be
linked here as they land — this hub only points at documents that exist
today.

## Known gaps and successor work

This is the tracker for work the documentation truth pass found and did not
close. Where a document stated the intended bar and the code did not meet it,
the rule was **fix the code, never weaken the document** — so the document
stands as written and the gap is open here.

Each entry is written to stand on its own: what is wrong, where in the code,
and what closing it involves. The audit ledger
([`notes/2026-08-17-documentation-truth-audit.md`](notes/2026-08-17-documentation-truth-audit.md))
holds the original probes and their output as supporting evidence, but no
entry below depends on it — a ledger is immutable evidence of what was
verified on one date, not a tracker, and it may not live in this repository
forever.

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

**Two `.go` package comments cite a spec that Phase 2 deletes and have no
durable replacement home.** `internal/runner/runner.go` cites the 2026-07-12
greenfield architecture spec §7 for the poll loop's jitter, per-poll timeout
and panic isolation; `sdk/model/model.go` cites §4 for the "facets are
per-device-kind, never per-vendor" governance rail. Both now name the spec in
full rather than saying "spec §7", so git history remains findable, but
neither rule has a living home. *Closing it:* the explanation tier —
`architecture.md` for the poll loop, `adapter-to-model.md` for the governance
rail — then repoint both comments. Three sibling citations were repointed in
this pass: `internal/config/config.go` now cites ADR 0014,
`internal/app/app.go` and `internal/app/app_test.go` cite `README.md`'s
loud-drop statement, and `internal/cloudevents/envelope.go` was citing ADR 0006
for the CE-source URN — the wrong ADR entirely, since 0006 documents the old
`//<agency>/<site>/<device-id>` format — and now cites ADR 0015. Evidence:
ledger `:2470-2528`.

**The "rule of three" deferral has no home outside the specs being deleted.**
The greenfield spec defers any per-vendor overlay mechanism until roughly
three NTCIP-variant adapters exist, and two other specs scheduled for deletion
cite it by name as the reason no such mechanism exists. No ADR records it.
*Closing it:* fold one sentence into `pluggability.md` during harvest, or
accept losing the rationale — the absence of a mechanism is self-evident from
the code either way. Evidence: ledger `:2448-2466`.

**The three `SKILL.md` files were never probed.** The truth audit covered every
surviving and harvested document except the skills; its only skill coverage was
confirming the directories exist. This is not theoretical:
`add-vendor-adapter/SKILL.md` told contributors to wire adapter registration
"into `internal/app`", when registration has always lived in
`RegisterAdapters` in `cmd/collector/main.go` and nothing in `internal/app`
calls `RegisterTo`. That one was corrected by reading, not by a systematic
pass, and this hub routes the highest-traffic contributor task straight into
that file. *Closing it:* probe all three skills claim-by-claim — every path,
symbol, command and rule statement — as part of retargeting them. Evidence:
ledger `:284-297` is the entire skill coverage.

**Enforced rules are still paraphrased outside
[`reference/invariants.md`](reference/invariants.md).** `AGENTS.md` restates
six (the two layering rules, absence-of-evidence, subjects-versus-envelope,
the testing bar, and config-as-trust-boundary) with no link into
`docs/reference/` at all. `CONTRIBUTING.md` restates the openits-models import
rule and "no fixtures, no merge"; `README.md`'s contributing section and
`.github/pull_request_template.md`'s layering checkbox each restate one; all
three `SKILL.md` files restate at least one. A paraphrase is a copy nobody
remembers to update, which is how the 28-restatements-across-14-files problem
started. *Closing it:* convert each to a link to its canonical row.

### Unresolved because it could not be verified here

**ADR 0001's "gen-1 code is deleted (git history preserves it)" cannot be
confirmed from this repository.** No `gen2` branch exists locally or on the
remote, and history begins at a deliberately squashed public-baseline commit,
so whatever preserves the gen-1 code is not reachable from this clone. Recorded
as blocked rather than false. *Resolving it:* check whether an internal
predecessor repository holds the pre-launch history, and either confirm the
claim or correct the ADR's status line. Evidence: ledger `:831-840`.
