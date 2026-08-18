# Documentation Architecture — Design

**Date:** 2026-08-17
**Status:** Phase 1 and Phase 2 shipped (§10, §11.1). Successors A and B are
open — see §11.
**Scope:** This repo's documentation, skills, and the checks that keep both
true. Contributor tooling (fixture recorder, conformance kit) and the deploy
path are successor efforts — see §11.

## 1. Purpose

**Success criterion: a junior engineer picks up this repo cold and ships a
working vendor adapter.**

That is a documentation problem before it is a tooling problem. The code is
green, the layering is enforced, and five of the eight facet kinds are modeled,
diffed, and wired to real ce-types with no adapter producing them — meaning a
newcomer's first adapter can touch `internal/vendors/<vendor>/` and nothing
else. None of that is written down anywhere a newcomer would look, and several
of the documents they *would* look at are wrong.

Two audiences, one system: humans reading prose, and agents following skills.
Neither is served by making the other's document do double duty.

### Non-goals

- A published documentation site. Plain markdown, GitHub-rendered, until
  someone asks otherwise.
- Prose-quality automation — spell check, readability scores, coverage
  percentages. They punish writing and catch nothing that matters.
- Documenting the deploy path beyond a stub. It depends on code that does not
  exist yet (fleet plan Phase 1).

## 2. The problem, with evidence

### 2.1 Documents that are not true

A ten-minute spot check across roughly a fifth of the documentation:

| Claim | Location | Verdict |
|---|---|---|
| Adapters live at `internal/vendors/<vendor>/<kind>/` | `README.md:14`, greenfield spec `:74`, `:103` | **False.** Actual layout is `internal/vendors/ntcip/asc.go`, flat. `add-vendor-adapter/SKILL.md:43` says `<vendor>/` and is correct — the docs contradict each other as well as the code |
| Subject is `openits.<agency>.<site>.<service>.<event>.v<n>` | greenfield spec `:259` | **False.** Superseded by ADR 0009 and ADR 0011. This is the document `README.md` calls "Full design" |
| "Working today: … signal-status synth" | `README.md` Status | **Stale.** Eight differs are registered at `internal/app/app.go:89` |
| "Not yet wired: additional facets and vendors" | `README.md` Status | **False, and inverted.** Every facet is wired; adapters are what is missing |
| "`ntcip-asc` adapter (fixtures + live SNMP)" | `README.md` Status | **Overstated.** Hand-written OID maps, not recordings |
| Health address `openits.{agency}.{site}.health.*` | `asyncapi.yaml:48`, `:60` | **False.** Pre-ADR-0009/0011 grammar; unchanged since the public baseline |
| ADR 0006 superseded by ADR 0009 | `docs/adr/0006-…:3` | **Incomplete.** ADR 0011 also amends 0006; 0006's status never says so |
| Fixtures are "recorded raw transport responses" | `docs/adr/0008-…:11` | **Doc right, code wrong.** The bar is correct; nothing in the repo meets it |

### 2.2 The root cause is a tier error

The repo has been using dated design specs as its explanation layer. `README.md`
points a newcomer at a July 12 spec for the "full design," and that spec's
subject-grammar section is three ADRs out of date.

Nobody was wrong to write those specs. A spec records what was decided on a
date; it is point-in-time by nature, exactly like a plan. Explanation has to be
living. One document cannot be both, and when it tries, the reader cannot tell
which parts still hold.

### 2.3 Rules are restated until they drift

Four rules — the wire boundary, "no fixtures no merge," absence-of-evidence,
and the `internal/wire`-only clause — appear **28 times across 14 files**.
Frozen plans account for many, harmlessly. The live surfaces still number six:
`README.md`, `CONTRIBUTING.md`, `AGENTS.md`, the PR template, and two skills.
Adding how-to guides would make it seven-plus.

The ADRs are not the problem; they hold the reasoning well and are immutable
once accepted. The problem is that every other document paraphrases them, and a
paraphrase is a copy that no one remembers to update.

### 2.4 There is no learning-oriented document at all

`CONTRIBUTING.md` gives the adapter path six bullets. The configuration
reference exists only as comments inside `collector.yaml`. Nothing documents the
local development loop — that `snmptest.Static` fakes a device and that
`nats-server` is an embedded test-only dependency. A newcomer's first question,
"how do I run this and see it work," has no answer in the repository.

## 3. Taxonomy

> **Superseded in part, later in this same effort.** `docs/notes/` and
> `docs/plans/` as described below are no longer committed to this
> repository at all — not "empties as work ships" but gone entirely; both
> are now working artifacts kept outside the tree. `docs/README.md` is the
> live, authoritative description of the current layout. What follows is
> the taxonomy as originally designed and as Phase 1 built it, left as
> written per §3.1's own rule against rewriting settled sections.

Five tiers, each with one job and an explicit lifecycle.

| Tier | Answers | Lifetime | May restate a rule? |
|---|---|---|---|
| `docs/tutorial/` | "I have never seen this repo" | Living | No — links only |
| `docs/how-to/` | "I know the system; what are the steps" | Living | No — links only |
| `docs/explanation/` | "why is it shaped like this" | Living | No — links only |
| `docs/reference/` | "what is the valid value for X" | Living | **`invariants.md` only** |
| `docs/adr/` | "what was decided, and why" | Immutable | Canonical reasoning |

Two supporting directories, neither a documentation tier:

- **`docs/specs/`** — designs for work that is **not yet built**. A staging
  area, not an archive. It empties as work ships.
- **`docs/notes/`** — probe ledgers. Evidence of what was verified, when, and
  against what version. Evidence does not go stale; these are never edited.

### 3.1 Lifecycle rule

**A spec's life ends when its design ships and its durable content has been
promoted into the living tiers. A plan's life ends when it ships.** Both are
then deleted. Git history holds them; a reader browsing `docs/` does not have to
work out which of seventeen plans describes code that still exists.

ADRs are the deliberate exception. They are the decision trail, their format
announces that they are historical, and their own README already declares them
immutable. They are never deleted; only inaccurate status lines are corrected.

Archiving was considered and rejected: an archive directory still appears in
search results and still misleads, while costing the same effort as deletion.

## 4. Single source of truth for rules

`docs/reference/invariants.md` becomes the **only** document permitted to
restate an enforced rule. Every other surface links to a row in it.

| Rule | Decided by | Enforced by |
|---|---|---|
| Adapters and `sdk/` never import openits-models | ADR 0002 | `scripts/lint-boundary.sh` |
| openits-models pinned at main HEAD, never `replace` | ADR 0010 | `scripts/lint-boundary.sh` **(check to be added — see §4.1)** |
| Absence of evidence is never a state change | **No ADR — see §4.1** | synth differ tests |
| No fixtures, no merge | ADR 0008 | review; conformance kit (successor A) |
| Subjects are configurable; the CE envelope is canonical | ADR 0009, 0011 | `internal/subject` tests |
| Config is the trust boundary; boot fails on the unrecognized | **No ADR — see §4.1** | `internal/config` tests |

The "Enforced by" column is machine-checked (§7.1), so the table cannot decay
into a list of aspirations.

### 4.1 Rules with no decision record

Drafting this table surfaced three defects before a line of it was committed —
which is the strongest available argument for having it.

**Two load-bearing rules have no ADR.** Both live only in `AGENTS.md` and in
specs scheduled for deletion:

- *Absence of evidence is never a state change.* The synth engine's iron rule,
  and arguably the single most important correctness invariant in the
  collector. Its only sources are the greenfield spec §4 and `AGENTS.md`.
- *Config is the trust boundary; the collector refuses to start on anything it
  does not understand.* ADR 0011 and ADR 0012 both cite this as established
  ("a narrow exception to 'config is the trust boundary'"), but nothing
  established it.

Deleting those specs would leave both rules homeless, cited by ADRs that assume
a record which does not exist. Each therefore gets a retroactive ADR — 0013 and
0014 — written from the reasoning in the specs being harvested. Writing an ADR
for an already-settled rule is legitimate: the record documents the decision,
and the decision predates the record.

**One enforcement claim is false.** `scripts/lint-boundary.sh` greps for
openits-models imports; it does **not** check for `replace` directives, so ADR
0010's never-`replace` clause is currently unenforced. The fix is a two-line
check in the existing script, which is cheaper than weakening the row to
"review only."

The truth pass (§8) must treat this as a general category: **any rule whose only
source is a document being deleted needs a home before that document goes** —
a new ADR when it is a genuine decision, promotion into explanation when it is
merely a consequence.

## 5. Document manifest

### 5.1 Explanation (living)

| File | Job | Source |
|---|---|---|
| `architecture.md` | The pipeline end to end, with one worked trace: an SNMP OID → facet → differ → domain event → payload → ce-type → subject → JetStream | Harvest greenfield spec §2, §6 |
| `adapter-to-model.md` | Snapshot anatomy; facet vs `FacetError`; why absence of evidence is never a state change; how differs consume facets | Harvest §4; new worked example |
| `pluggability.md` | `Descriptor`, `Capability`, `Factory`, `Registry`; why in-tree rather than plugins (ADR 0003); why pull-only (ADR 0004); why config is the trust boundary | Harvest §5, §6 |
| `wire-boundary.md` | Why the domain model is not the wire model; versioned emitters; `ce-type`, `ce-id`, `ce-dataschema`; why the emitter drops rather than approximates | New; ADR 0002 + emitter spec |
| `testing-strategy.md` | What a golden proves and what it does not; why fixtures must be recorded; why `-race` matters here | Harvest §8 |

### 5.2 Tutorial (living)

`build-your-first-adapter.md` — the exemplar. One hand-held path, no choices:
run the suite, read `internal/vendors/ntcip/asc.go`, write a trivial adapter
against `snmptest.Static`, register it, watch its events reach the embedded
JetStream, write the differ tests.

It targets a fake device deliberately and permanently. `snmptest.Static` is the
correct teaching substrate and is not affected by successor work; only the
closing "now record from your real device" step links out to a page that does
not exist yet.

### 5.3 How-to (living)

`add-a-vendor-adapter.md` · `add-a-domain-facet.md` ·
`map-an-event-to-the-wire.md` · `adopt-an-openits-models-release.md` ·
`record-fixtures-from-a-device.md` *(stub; successor A)* ·
`deploy-a-collector.md` *(stub; successor B)*

Stubs state what they will contain, who owns them, and where to look meanwhile.
An honest stub is better than an absent page and far better than a fictional
one.

### 5.4 Reference (living)

- `invariants.md` — §4.
- `configuration.md` — every config field: type, default, validation, required.
  Canonical. `collector.yaml` keeps its rationale commentary and sheds the
  reference-shaped detail, linking here.
- `test-requirements.md` — the matrix a PR is measured against, per
  contribution type. For a differ: first poll, no change, each axis
  independently, failed read, DeviceKind stamping.
- `starter-tasks.md` — the highest-leverage page for the success criterion.
  `dms-status`, `cctv-status`, `traffic-intervals`, `zone-incidents`, and
  `zone-intervals` are modeled, diffed, golden-tested, and mapped to real
  ce-types with no adapter producing them. Landing one touches
  `internal/vendors/<vendor>/` alone — no exposure to the layering rules that
  are hardest to learn. Also designates `internal/vendors/ntcip/asc.go` as the
  reference implementation to read first.

### 5.5 Hub

`docs/README.md` routes by task. `README.md` points here and stops growing.

## 6. Skills

### 6.1 Contract

Every `SKILL.md` shares one structure, so the seventh matches the first:

1. **Frontmatter** — `name`, and a `description` naming explicit trigger
   phrases.
2. **When this applies** — and when it does not.
3. **Invariants** — links into `invariants.md` rows. Never restated.
4. **Procedure** — an ordered checklist.
5. **Verify** — literal commands, with the expected result.
6. **Canonical doc** — a link to the how-to holding the detail.

Skills stay terse: trigger, guardrails, procedure. Narrative lives in the
how-to. `.claude/skills/README.md` holds the template and the contract.

### 6.2 Inventory

| Skill | Audience | State |
|---|---|---|
| `add-vendor-adapter` | Contributor | Retarget to pointer shape |
| `add-domain-facet` | Contributor | Retarget |
| `wire-emitter` | Contributor | Retarget |
| `add-transport` | Contributor | **New.** Only SNMP exists; an HTTP or serial device has no map |
| `review-adapter-contribution` | Maintainer | **New.** Runs the invariant checklist, detects hand-written fixtures presented as recordings, flags core files a contributor should not have touched |
| `deploy-collector` | Operator | **Deferred to successor B** |

`review-adapter-contribution` is what makes accepting contributions at volume
survivable: without it, every incoming adapter costs a full manual audit.

## 7. Keeping it true

### 7.1 Checks, all in `make check`

1. **Invariants enforcement test.** Parses `invariants.md` and asserts each
   named enforcer exists — script present on disk, or test function present in
   the tree. Deleting `lint-boundary.sh` fails the docs check.
2. **Config coverage test.** Reflects over `config.Config`; every field must
   appear in `configuration.md`. A new field with no documentation is a failing
   build.
3. **AsyncAPI subject test.** Renders each `asyncapi.yaml` channel address
   through the real `internal/subject` template and asserts equality. This is
   the check that would have caught §2.1's health-address defect a month ago.
4. **Link check and skill structure lint.** Relative links under `docs/`
   resolve; every `SKILL.md` carries the §6.1 sections. One shell script.

Each fails only on a **structural** claim that has become false — a named
enforcer that vanished, a field that appeared, an address that drifted. That is
precisely the class of decay that actually occurred here, three separate times.

### 7.2 What these do not catch

An explanation document that becomes subtly wrong about *behavior*. Nothing
short of review catches that. The checks make mechanical drift impossible to
ship; they do not make prose self-correcting, and claiming otherwise would be
its own untrue documentation.

## 8. The truth pass

Runs **first**, before any new document is written. The explanation tier
harvests content from documents being deleted; harvesting an unverified claim
launders it into the document a newcomer trusts most.

### 8.1 Method

Follow the house ritual already in use: *probe, don't read* (`wire-emitter`
skill), with verdicts recorded in a ledger (`docs/notes/`, Task 0 format).
Output is `docs/notes/2026-08-17-documentation-truth-audit.md` — one row per
document, each load-bearing claim probed against the code, and the probe itself
recorded so a reviewer can re-run it.

*(`docs/notes/` was later removed from this repository entirely — see the
§3 note. The ledger this step produced is retrievable from git history;
§11.2 has the command.)*

### 8.2 Verdicts

- **True** — leave it.
- **Doc wrong** — fix the document.
- **Doc right, code wrong** — the document states the intended bar and the code
  does not meet it. **Fix the code; never weaken the document.** ADR 0008 is
  this case: the answer is successor A's fixture recorder, not softening the
  ADR to bless hand-written maps.
- **Unverifiable** — a claim nothing can confirm. Delete the claim.

### 8.3 Scope

Every document that survives, plus every document being harvested. Package-level
doc comments included — there is precedent for those going stale (Task 0 found a
false assertion in `lint-boundary.sh`'s comment). Inline comments excluded:
unbounded, low yield.

## 9. Demolition

**Specs — 6 deleted after harvest (§10 step 3, not step 1), 1 kept:**

Deleted: `2026-07-10-vendor-adapter-architecture-design.md` (gen-1),
`2026-07-12-greenfield-collector-architecture-design.md`,
`2026-07-16-asc-facets-design.md`, `2026-07-16-configurable-subjects-design.md`,
`2026-07-16-dms-domain-design.md`,
`2026-07-21-openits-models-emitter-design.md`.

Kept: `2026-08-09-management-surface-design.md` — describes work that does not
exist yet and is the direct input to successor B.

**Plans — 16 deleted, 1 kept:**

All eleven `2026-07-10-*` (gen-1; `p0-hardening.md` describes an
`internal/runtimex` package and Prometheus metrics, neither of which exists),
plus `2026-07-12-gen2-plan1-contract-and-spine.md`, `2026-07-16-asc-facets.md`,
`2026-07-16-configurable-subjects.md`, `2026-07-16-dms-domain.md`, and
`2026-08-08-health-subject-separation.md` — all shipped.

Kept: `2026-08-09-fleet-deployment.md` — not built.

**Citation cost:** four live citations point into `docs/specs/` —
`README.md:27`, `AGENTS.md:7`, `docs/adr/README.md:22`, and two skills. Every
one of those files is being rewritten regardless. Frozen plans cite specs
fourteen times; a dangling path inside a frozen historical document is a
historical reference, not a broken link.

**End state:** roughly 20 live documents in place of 54, each one probed.

## 10. Sequencing

1. **Truth pass and demolition.** Audit ledger; write ADR 0013 and ADR 0014 for
   the homeless rules (§4.1); add the `replace`-directive check to
   `lint-boundary.sh`; delete the 16 shipped plans; fix confirmed defects in
   place. No new prose documents written.

   The six specs are **not** deleted here — step 3 harvests them first. Plans
   are safe to delete now because nothing harvests from them.
2. **Scaffold and verification.** `docs/README.md`; the four checks;
   `invariants.md`, `configuration.md`, `test-requirements.md`,
   `starter-tasks.md`.
3. **Explanation tier.** Harvest from probed content, then delete the six
   harvested specs.
4. **Tutorial and how-to guides.** Exemplar first; then the three unblocked
   how-tos; two honest stubs.
5. **Skills.** Retarget three; add `add-transport` and
   `review-adapter-contribution`.

Steps 1–2 carry the leverage. Steps 3–4 carry the writing.

## 11. Successors

Phase 2 (§11.1) has shipped: the explanation tier, the tutorial, all six
how-to guides (two of them honest stubs), and the skill retargeting this
spec's manifest (§5, §6) describes — three skills retargeted to the §6.1
contract plus `add-transport` and `review-adapter-contribution` added, all
five now at `contract: v1`. The rule-restatement conversions landed too:
`AGENTS.md`, `CONTRIBUTING.md`, `README.md`, `.github/pull_request_template.md`,
and all three retargeted skills link into `invariants.md` instead of
restating a rule. Two successors remain, both out of scope for this design
from the start:

- **A — contributor tooling.** Fixture recorder, migration of inline fixtures
  and hex goldens into `testdata/` with a sanctioned regeneration path, and an
  adapter conformance kit any contributor's test can call. Unblocks
  `record-fixtures-from-a-device.md` and resolves ADR 0008's doc-right/code-wrong
  verdict.
- **B — deploy path.** Fleet plan Phases 1–2: readiness signal, broker-absent
  tolerance, expected-version drift, container image, systemd unit. Unblocks
  `deploy-a-collector.md` and the `deploy-collector` skill.

One more item surfaced during Phase 2 itself rather than being anticipated
here: a duplicate-prose lint, the natural fifth `lint-docs` guard, alongside
the four §7.1 lists. It wasn't built — a version with an acceptable
false-positive rate is its own design problem — and it isn't numbered
alongside A and B because it guards the documentation tree itself rather
than unblocking a stub; it's tracked in
[`docs/README.md`'s known-gaps list](../README.md#a-guard-that-was-deliberately-not-built)
instead.

### 11.1 Phase 2 scope

Phase 2's harvest list is Phase 1's truth-pass output (§8, §11.2): the
explanation tier (five documents promoted from probed spec content, then the
six harvested specs deleted — §9), the tutorial, four how-to guides, two
honest stubs, and the skills — three retargeted to the §6.1 contract, plus
`add-transport` and `review-adapter-contribution`.

**Rule-restatement conversions (added after Phase 1 review).**
`docs/reference/invariants.md` states the enforced rules canonically, but the
paraphrases it was meant to replace are still live. Each of these restates at
least one enforced rule with no link into `docs/reference/`, and each must be
converted to a link (or deleted) in Phase 2:

- `AGENTS.md` — six restatements: the two layering rules (`:20-22`, `:23-26`),
  absence-of-evidence (`:27-29`), subjects-vs-envelope (`:30-34`), the testing
  bar (`:47-51`), and config-as-trust-boundary (`:60-62`). Untouched by
  Phase 1.
- `CONTRIBUTING.md` — the openits-models import rule (`:26`) and
  "no fixtures, no merge" (`:28`).
- `README.md` — the contributing section's adapter-contract bullets.
- `.github/pull_request_template.md` — the layering checkbox (`:9`).
- `.claude/skills/add-vendor-adapter/SKILL.md`,
  `.claude/skills/add-domain-facet/SKILL.md`,
  `.claude/skills/wire-emitter/SKILL.md` — all three, as part of the retarget
  already listed above.

**The three `SKILL.md` files were probed with the ledger method — ahead of
schedule.** The truth audit never probed the skills. Its only skill coverage
was an `ls -d` of the directory (ledger `:284-297`) — a check that they
exist, not that anything in them is true. Phase 1 review had already found
one concrete consequence by reading, not by a systematic pass:
`add-vendor-adapter/SKILL.md` sent contributors to wire adapter registration
"into `internal/app`", when registration has always lived in
`cmd/collector/main.go`'s `RegisterAdapters`.

The systematic pass this section called for ran before Phase 2's own
sequencing reached it: 58 claims checked — every path, symbol, command, and
rule statement across the three original skills — with four defects found
and fixed in commit `28456bd`, including the `add-vendor-adapter` skill
attributing the absence-of-evidence rule to ADR 0008 instead of ADR 0013,
and `wire-emitter` describing openits-models as consumed via tagged semver
releases when ADR 0010 pins it at a main-HEAD pseudo-version instead.
Recorded here as done, not as an open item.

**Citations with no durable home.** Phase 2 deletes the six harvested specs
(§9). These live `.go` comments cite "spec §N" and will dangle. Phase 1
repointed the ones with an existing target; these two have none yet and need
one created or the citation dropped:

- `internal/runner/runner.go:3` — cites the greenfield spec §7 for the poll
  loop's jitter / per-poll timeout / panic isolation. No ADR or reference doc
  covers it; the natural home is the explanation tier's `architecture.md`.
- `sdk/model/model.go:9` — cites the architecture spec §4 for the
  "facets are per-device-kind, never per-vendor" governance rail. The
  manifest harvests §4 into `adapter-to-model.md`; repoint once that exists.

Also: the greenfield spec's "rule of three" deferral (no per-vendor overlay
mechanism until ~3 variant adapters exist) is cited by name from two other
harvest specs and has no home outside them — fold one sentence into
`pluggability.md` during harvest or accept losing the rationale (ledger
`:2448-2466`).

### 11.2 Harvest dependency: the audit ledger (historical)

Phase 2's harvest (§11.1, §9) used the truth audit's per-document verdicts as
its whitelist: which claims in the six specs being harvested were probed
**True** and safe to promote into the explanation tier as written, and which
were **Doc wrong** and needed fixing during harvest rather than copying
verbatim (§8.2). Harvesting without it would have meant re-verifying every
claim from scratch, or worse, laundering an unverified one into the document
a newcomer trusts most (§8, first paragraph).

That ledger — `docs/notes/2026-08-17-documentation-truth-audit.md`, one row
per document with the probe that produced each verdict — no longer lives in
this repository: `docs/notes/` is a working-artifact tier kept outside the
tree once its content has served its purpose (this repo's agent tooling
writes such ledgers under the git-ignored `.superpowers/`; see
`docs/README.md`'s taxonomy note). Every commit that touched it is still in
history. It was retrieved for the harvest with:

```
git show e6e2b1f:docs/notes/2026-08-17-documentation-truth-audit.md
```

Left here for whoever next needs it — auditing the harvest, or re-deriving a
verdict — since the same command still resolves; nothing about it depended
on Phase 2 being in flight.

## 12. Risks

**The truth pass finds more than expected.** A fifth of the docs yielded eight
defects. If the rate holds, expect thirty-plus. Mitigation: the pass is
scoped to surviving and harvested documents only, and "doc right, code wrong"
findings become successor work items rather than blocking this effort.

**Explanation docs drift anyway.** §7.2 is honest that nothing prevents this.
Mitigation is structural: keeping rules out of explanation docs means the
highest-consequence claims live in the one place CI verifies.

**Deletion loses something needed later.** Git history holds every deleted file,
and durable content is harvested before deletion rather than after.

## 13. Decisions locked

1. Two artifacts; how-to canonical, skills terse pointers.
2. `invariants.md` is the sole restatement of enforced rules, CI-verified.
3. Five tiers; specs stage unbuilt designs; notes are immutable evidence.
   *(Later refined — see the §3 note: notes left the repository entirely.)*
4. Specs and plans are deleted when their work ships, after harvest.
   *(Plans were refined further still — see the §3 note: no longer
   committed here at all, not even in flight.)*
5. ADRs are never deleted; only inaccurate status lines are corrected.
6. `configuration.md` is canonical; `collector.yaml` keeps rationale only.
7. Truth pass runs first, using probe-and-ledger, with four verdicts.
8. Skills cover three audiences: contributor, maintainer, operator.
9. A rule whose only source is a deleted document gets a home first — a new ADR
   if it is a decision, promotion into explanation if it is a consequence.
