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
| Contribute without access to any hardware | [`reference/starter-tasks.md`](reference/starter-tasks.md#starter-tasks-that-need-no-hardware) — owed wire mappings in services already wired; `make check` is the whole feedback loop |
| Learn the codebase by building something | [`tutorial/build-your-first-adapter.md`](tutorial/build-your-first-adapter.md) — clone to a real event on the bus, in one sitting |
| Add a vendor adapter (needs access to the device) | [`reference/starter-tasks.md`](reference/starter-tasks.md) for the highest-leverage first PR, [`how-to/add-a-vendor-adapter.md`](how-to/add-a-vendor-adapter.md) for the full guide, then [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) for the step-by-step checklist |
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
| Deploy a collector | [`how-to/deploy-a-collector.md`](how-to/deploy-a-collector.md) — one cabinet: install, systemd, health checks, upgrade and rollback. Fleet-scale rollout is still a spec |
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

**48 catalog ce-types are declared upstream and unmapped, 3 of them owed
in services the collector already serves.** [ADR 0016](adr/0016-collector-as-transitional-shim.md)
makes this a *collector* gap by definition: the collector adapts to the
catalog, so a ce-type the pinned release declares and `internal/wire/openits`
never mapped is work owed here, not evidence the catalog is wrong. Against
the v0.5.0 pin the emitter maps 29 of the catalog's 77 ce-types.

The live list is not this paragraph: it is the `unmapped` allowlist in
`internal/wire/openits/catalog_test.go`, which the catalog-conformance
tests keep exact against the pinned release — a newly declared ce-type
fails CI until it is mapped or allowlisted, and an entry that is mapped or
no longer declared fails as stale. Each entry carries one of four reasons:

- **service not modelled** (31) — `ess`, `ramp-metering`,
  `reversible-lane`, `rsu`, `work-zone`: blocked on a domain facet and an
  adapter, not on a mapping.
- **describes a command** (3) — the `openits.cctv.*` PTZ and lockout
  events. Nothing in the collector commands anything
  (the SDK has no command path at all, by
  [ADR 0021](adr/0021-sdk-interfaces-ship-with-their-first-implementer.md)).
- **signal control, not yet assessed per event** (11) — most are high-rate
  per-cycle events an SNMP poller structurally cannot observe; they want a
  controller pushing, which is the end state ADR 0016 expects to make the
  collector unnecessary. Each needs an individual verdict.
- **owed** (3) — `traffic-sensor-status-report`, `queue-state-changed`
  and `zone-occupancy-changed`, in services already wired end to end.

*Closing it:* per ce-type, not in bulk — each needs the domain event, the
`ceTypeFor` entry, the `Encode` case, a golden, and deleting its
`unmapped` line. Start with `traffic-sensor-status-report`, the one ADR
0016 names explicitly.

**`ntcip-asc`'s fixtures are hand-written, not recordings.** ADR 0008 requires
recorded raw transport responses; `internal/vendors/ntcip/asc_test.go`'s
`healthyFixture` is a hand-typed `map[string]int64`, and the adapter's own doc
comment states the alarm bitmap has never been validated against a physical
controller. The bar is correct and stays as written; the repo's single adapter
does not meet it. *Closing it:* the fixture recorder and adapter conformance
kit (successor A) — a tool that captures real SNMP responses into `testdata/`
with a sanctioned regeneration path, then re-recording `ntcip-asc` against a
real controller. Evidence: ledger `:194-210`, `:3428-3440`.

**ADR 0012's expected-version drift reporting does not exist.** ADR 0012's
Decision section lists three numbered mechanisms in the present tense. Two
are implemented: the collector never self-updates, and it exposes a
readiness signal — `/readyz` on the loopback management surface
(`internal/manage`), latching on the first successful device poll and
independent of the broker, which is the condition health-gated rollback
needs. The third, comparing an expected version against the running one and
reporting drift, is not: `/status` reports the running `version` and
`config_revision`, but nothing knows what version was *expected*. *Closing
it:* an expected-version input from the deployment, and a drift report on
the bus. Evidence: ledger `:1376-1392`.

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
