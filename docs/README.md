# Documentation hub

This page routes you to the right document for what you're trying to do.
New to the project? Read the [top-level README](../README.md) first, then
[`explanation/architecture.md`](explanation/architecture.md).

## How the docs are organized

| Directory | What it holds |
|---|---|
| `tutorial/` | One guided build for someone who has never seen the repo. It links to rules rather than restating them. |
| `how-to/` | The steps for each kind of contribution, plus deployment. The `.claude/skills/` guides are short checklists that point at these. |
| `explanation/` | How the pieces fit together, and why the boundaries sit where they do. |
| `reference/` | The enforced rules, the configuration surface, the testing bar, and what a consumer of the bus can rely on. |
| `adr/` | Architecture decision records. Immutable once accepted; only a status line or dated note is ever added. |
| `specs/` | Designs for work that has not shipped yet. A spec is deleted once its design ships and the lasting parts move into the tiers above. |

Implementation plans and working notes are not committed here. They
describe how a change was built, not the system that resulted; agent
tooling keeps them in the git-ignored `.superpowers/` directory.

## Find the right document

| I want to... | Start here |
|---|---|
| See the repo for the first time | [`../README.md`](../README.md) — what the collector is, the pipeline in one diagram, and current status |
| Learn the codebase by building something | [`tutorial/build-your-first-adapter.md`](tutorial/build-your-first-adapter.md) — from a fresh clone to a real event on the bus, in one sitting |
| Contribute without access to any hardware | [`reference/starter-tasks.md`](reference/starter-tasks.md#starter-tasks-that-need-no-hardware) — open gaps where `make check` is the whole feedback loop |
| Add a vendor adapter (needs access to the device) | [`reference/starter-tasks.md`](reference/starter-tasks.md) for the best first PR, [`how-to/add-a-vendor-adapter.md`](how-to/add-a-vendor-adapter.md) for the full guide, then the [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) checklist |
| Model device state nothing in `sdk/model` represents yet (a new facet) | [`how-to/add-a-domain-facet.md`](how-to/add-a-domain-facet.md), then [`add-domain-facet`](../.claude/skills/add-domain-facet/SKILL.md) |
| Support a device that doesn't speak SNMP | [`add-transport`](../.claude/skills/add-transport/SKILL.md) — builds the `sdk/transport/<name>` package, not the adapter itself |
| Map a domain event to a wire ce-type | [`how-to/map-an-event-to-the-wire.md`](how-to/map-an-event-to-the-wire.md), then [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) |
| Adopt a new openits-models release | [`how-to/adopt-an-openits-models-release.md`](how-to/adopt-an-openits-models-release.md), then [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) |
| Record fixtures from a real device | [`how-to/record-fixtures-from-a-device.md`](how-to/record-fixtures-from-a-device.md) — `collector record-fixture`, scrubbing, and regenerating a fixture |
| Review an incoming adapter contribution | [`review-adapter-contribution`](../.claude/skills/review-adapter-contribution/SKILL.md) — the machine checks, plus what no check can see |
| Know what will fail my PR | [`reference/invariants.md`](reference/invariants.md) for the rules and what enforces each, [`reference/test-requirements.md`](reference/test-requirements.md) for the testing bar per contribution type |
| Understand how the pieces fit together | [`explanation/architecture.md`](explanation/architecture.md) — the pipeline end to end; read this first |
| Understand facets, snapshots and the synth engine | [`explanation/adapter-to-model.md`](explanation/adapter-to-model.md) |
| Understand how adapters plug in | [`explanation/pluggability.md`](explanation/pluggability.md) — the adapter contract, the opaque `connection` block, why transport stays out of the core |
| Understand why the domain model isn't the wire model | [`explanation/wire-boundary.md`](explanation/wire-boundary.md) |
| Understand what the tests prove, and what they don't | [`explanation/testing-strategy.md`](explanation/testing-strategy.md) |
| Understand why it's built this way | [`adr/README.md`](adr/README.md) — the decision records, in order |
| Consume events the collector publishes | [`reference/consumer-contract.md`](reference/consumer-contract.md) — delivery, duplicates, ordering, and what a restart does to the stream |
| Package or supervise the collector (container, unit, package, fleet agent) | [`reference/runtime-contract.md`](reference/runtime-contract.md) — what the binary needs and promises: inputs, files, network, health, signals, exit codes |
| Configure a deployment | [`reference/configuration.md`](reference/configuration.md) — every `collector.yaml` field and command-line flag: type, default, validation |
| Deploy a collector | [`how-to/deploy-a-collector.md`](how-to/deploy-a-collector.md) — one cabinet: install, systemd, health checks, upgrade and rollback |
| See designs not yet built | [`specs/2026-08-09-fleet-deployment.md`](specs/2026-08-09-fleet-deployment.md) (fleet-scale rollout) and [`specs/2026-08-09-management-surface-design.md`](specs/2026-08-09-management-surface-design.md) (the management surface's design record; all but `sd_notify` is built) |
| Know what is already known to be broken | [Known gaps and successor work](#known-gaps-and-successor-work), below |

## Task guides in `.claude/skills/`

Short checklists, written for AI agents but readable by anyone:

- [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) — new vendor × device-kind integrations
- [`add-domain-facet`](../.claude/skills/add-domain-facet/SKILL.md) — new facets, differs, and domain events
- [`add-transport`](../.claude/skills/add-transport/SKILL.md) — a new `sdk/transport/<name>` package for a protocol nothing speaks yet
- [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) — openits-models mappings and release pin bumps
- [`review-adapter-contribution`](../.claude/skills/review-adapter-contribution/SKILL.md) — the maintainer-side checklist for an incoming adapter PR
- [`.claude/skills/README.md`](../.claude/skills/README.md) — the structure every skill follows

## Known gaps and successor work

Open gaps the documentation audit found and did not close. Where a document
stated the intended bar and the code fell short, the rule was **fix the
code, never weaken the document**: the document stands, and the gap is
tracked here.

Each entry says what is wrong, where, and what closing it involves. Some
cite an `Evidence: ledger` line: that is the audit's probe ledger, which no
longer lives in the tree. Retrieve it from history with:

```
git show e6e2b1f:docs/notes/2026-08-17-documentation-truth-audit.md
```

### The code does not meet a bar the docs correctly state

**49 catalog ce-types are declared upstream and unmapped; 3 of them are owed
in services the collector already serves.** Against the v0.8.0 pin the
emitter maps 30 of the catalog's 79 ce-types. Under
[ADR 0016](adr/0016-collector-as-transitional-shim.md) the collector adapts
to the catalog, so an unmapped ce-type is work owed here, not a catalog
problem.

The live list is the `unmapped` allowlist in
`internal/wire/openits/catalog_test.go`. Its tests keep it exact against the
pinned release: a newly declared ce-type fails CI until it is mapped or
allowlisted, and an entry that is mapped or no longer declared fails as
stale. Each entry carries one of four reasons:

- **service not modelled** (32) — `ess`, `ramp-metering`,
  `reversible-lane`, `rsu`, `work-zone`. These need a domain facet and an
  adapter first, not just a mapping.
- **describes a command** (3) — the `openits.cctv.*` PTZ and lockout
  events. The collector commands nothing; the SDK has no command path at
  all ([ADR 0021](adr/0021-sdk-interfaces-ship-with-their-first-implementer.md)).
- **signal control, not yet assessed per event** (11) — mostly high-rate
  per-cycle events an SNMP poller cannot observe. They want a controller
  that pushes, which is the end state ADR 0016 expects to make the
  collector unnecessary. Each needs its own verdict.
- **owed** (3) — `traffic-sensor-status-report`, `queue-state-changed`
  and `zone-occupancy-changed`, in services already wired end to end.

*Closing it:* one ce-type at a time. Each needs the domain event, the
`ceTypeFor` entry, the encoder case, a golden, and its `unmapped` line
deleted. Start with `traffic-sensor-status-report`, the one ADR 0016 names.

**`ntcip-asc`'s fixtures are hand-written, not recordings.** ADR 0008
requires recorded raw transport responses. `internal/vendors/ntcip/asc_test.go`'s
`healthyFixture` is a hand-typed `map[string]int64`, and the adapter's own
doc comment says its alarm bitmap has never been validated against a
physical controller. (`ntcip-dms`'s fixtures, by contrast, are transcribed
from a live controller and say so.) *Closing it:* re-record `ntcip-asc`
against a real controller with `collector record-fixture`
([how](how-to/record-fixtures-from-a-device.md)), which needs the
hardware and nothing else. Evidence: ledger `:194-210`,
`:3428-3440`.

**ADR 0012's expected-version drift reporting does not exist.** Two of ADR
0012's three mechanisms are built: the collector never updates itself, and
`/readyz` on the loopback management surface (`internal/manage`) gives the
readiness signal health-gated rollback needs. It latches on the first
successful device poll and does not depend on the broker. The third,
comparing the expected version with the running one and reporting drift, is
not built. The collector reports what it runs — `version` and
`config_revision` on `/status`, the `started` event and every heartbeat —
and [ADR 0019](adr/0019-collector-self-health.md) leaves the comparison to
the fleet control plane. *Closing it:* fleet-plan work
([`specs/2026-08-09-fleet-deployment.md`](specs/2026-08-09-fleet-deployment.md)),
largely outside this repository. Evidence: ledger `:1376-1392`.

### A guard that was deliberately not built

**No duplicate-prose lint exists.** The docs follow a "link, don't restate"
rule ([stated in `reference/invariants.md`'s preamble](reference/invariants.md#invariants)),
and copies of existing prose have so far been caught only by reviewers. A
shingling check across the docs tree would catch them mechanically, as a
third check in `scripts/lint-docs.sh` alongside link/anchor resolution and
skill structure (the existing doc guards are listed in
[`explanation/testing-strategy.md`](explanation/testing-strategy.md#doc-guards-structural-not-semantic)).
It was not built because a version with an acceptable false-positive rate
is real design work: fenced code legitimately repeats identifiers, some
items (the `ntcip-asc` fixture gap, for one) are *meant* to appear in the
tutorial, the how-to and this list, and short common phrases trip any
naive n-gram threshold. *Closing it:* design the false-positive filter
first — shingle length above common-phrase length, plus an allowlist for
fenced code and intentional repeats — then add the check.

### Unresolved because it could not be verified here

**ADR 0001's "gen-1 code is deleted (git history preserves it)" cannot be
confirmed from this repository.** Public history begins at a deliberately
squashed baseline commit, so whatever preserves the gen-1 code is not
reachable from a clone. Recorded as unverified rather than false.
*Resolving it:* confirm where the pre-launch history is kept, then either
confirm the claim or correct the ADR's status line. Evidence: ledger
`:831-840`.
