# Documentation Architecture — Implementation Plan (Phase 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish the documentation tiers, give every enforced rule one
CI-verified home, and probe every surviving document for truth — before a
single new prose page is written.

**Architecture:** Three movements. First, close the gaps the spec surfaced: an
unenforced ADR clause and two load-bearing rules with no decision record.
Second, run the truth pass — probe each document's load-bearing claims against
the code, record verdicts in a `docs/notes/` ledger, fix what is wrong, delete
the sixteen shipped plans. Third, build the scaffold and the four checks that
make mechanical documentation drift impossible to ship.

**Tech Stack:** Go 1.26 (`testing`, `reflect`, `gopkg.in/yaml.v3`), bash + awk
for the lint scripts, GNU make.

**Spec:** `docs/specs/2026-08-17-documentation-architecture-design.md`

## Global Constraints

- Every task ends green on `make check` AND `go test ./... -race`.
- TDD where there is code: write the failing test, watch it fail for the right
  reason, then implement. A guard that has not been seen to fail is not known
  to be a guard.
- Conventional Commits. No AI or assistant attribution anywhere in commit
  messages (`AGENTS.md`).
- **Do not push.** Commit locally only. `main` is protected; work stays on
  branch `docs/documentation-architecture`.
- Adapters, `sdk/`, and everything outside `internal/wire` must not import
  openits-models (ADR 0002, enforced by `scripts/lint-boundary.sh`).
- Documentation tasks are not exempt from evidence: a claim written into a
  document must have been probed against the code in the same task.
- The six specs listed in spec §9 are **not** deleted in this plan. Phase 2
  harvests them first. Deleting them here would destroy content the explanation
  tier is built from.

## File Structure

| File | Responsibility |
|---|---|
| `scripts/lint-boundary.sh` | Gains Rule C: no `replace` directive for the pinned model module (ADR 0010) |
| `Makefile` | Wires the new selftest and the docs lint into `check` |
| `docs/adr/0013-absence-of-evidence.md` | New. The synth engine's iron rule, currently recorded nowhere |
| `docs/adr/0014-config-is-the-trust-boundary.md` | New. Cited by ADR 0011 and 0012 as established; never established |
| `docs/adr/README.md` | Index gains 0013, 0014; ADR 0006 status corrected |
| `docs/notes/2026-08-17-documentation-truth-audit.md` | The audit ledger: one row per document, probe recorded |
| `docs/README.md` | The documentation hub; routes by task |
| `docs/reference/invariants.md` | Sole restatement of enforced rules |
| `docs/reference/configuration.md` | Canonical config reference |
| `docs/reference/test-requirements.md` | The matrix a PR is measured against |
| `docs/reference/starter-tasks.md` | Facets awaiting an adapter; the reference implementation |
| `internal/docs/doc.go` | Package comment explaining why documentation has tests |
| `internal/docs/invariants_test.go` | Asserts every named enforcer exists |
| `internal/docs/configuration_test.go` | Asserts every config field is documented |
| `internal/docs/asyncapi_test.go` | Asserts AsyncAPI addresses match rendered subjects |
| `scripts/lint-docs.sh` | Relative-link resolution + `SKILL.md` structure |
| `asyncapi.yaml` | Addresses corrected to the ADR 0011 grammar |

`internal/docs` is a test-only package. It lives in `internal/` because it
imports `internal/config` and `internal/subject`; it ships no production code.

---

## Task 1: Enforce ADR 0010's never-`replace` clause

`scripts/lint-boundary.sh` greps imports. It does **not** look at `go.mod`, so
ADR 0010's "never a `replace` directive" clause is currently unenforced while
being widely believed enforced. The invariants table (Task 9) names this script
as the enforcer, so the claim must become true before the table asserts it.

**Files:**
- Modify: `scripts/lint-boundary.sh` (add Rule C after Rule B, before the
  `checked -eq 0` guard)
- Modify: `Makefile` (new `lint-boundary-replace-selftest` target, added to
  `check`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `LINT_GOMOD` environment override (default `go.mod`) so the rule is
  testable against a fixture; `make lint-boundary-replace-selftest`.

- [ ] **Step 1: Write the failing selftest**

Add to `Makefile`:

```make
# Prove Rule C can actually fail. Points the rule at a fixture go.mod that DOES
# replace the model module; if that does not trip, the rule is inert and its
# green result on the real go.mod means nothing.
lint-boundary-replace-selftest:
	@tmp=$$(mktemp -d); \
	printf 'module example.com/x\n\ngo 1.26\n\nreplace github.com/Vikasa2M/openits-models => ../openits-models\n' > $$tmp/go.mod; \
	if LINT_GOMOD=$$tmp/go.mod ./scripts/lint-boundary.sh >/dev/null 2>&1; then \
		rm -rf $$tmp; \
		echo "SELFTEST FAILED: lint-boundary did not flag a replace directive" >&2; \
		exit 1; \
	else \
		rm -rf $$tmp; \
		echo "lint-boundary replace-rule selftest: rule fires correctly"; \
	fi
```

Add `lint-boundary-replace-selftest` to the `.PHONY` list and to the `check`
target's dependency list.

- [ ] **Step 2: Run it to verify it fails**

Run: `make lint-boundary-replace-selftest`
Expected: FAIL with `SELFTEST FAILED: lint-boundary did not flag a replace
directive` — the script currently ignores `go.mod` entirely, so it exits 0.

- [ ] **Step 3: Implement Rule C**

In `scripts/lint-boundary.sh`, after the `forbidden=` assignment, add:

```bash
# Overridable so Rule C is testable against a fixture — see
# `make lint-boundary-replace-selftest`.
gomod="${LINT_GOMOD:-go.mod}"
```

After Rule B's loop and before the `checked -eq 0` guard, add:

```bash
# ---- Rule C: no replace directive for the model module (ADR 0010) ----------
# The pin is a main-HEAD pseudo-version while both repos move in lockstep. A
# `replace` would make every developer's build depend on a local checkout, so
# CI would be testing a tree nobody else has. Rules A and B cannot see this:
# a replaced module still imports cleanly.
#
# Parsed textually rather than via `go mod edit -json` so the check stays
# offline and works against a fixture path.
replaces=$(awk '
  /^replace[[:space:]]*\(/ { inblock=1; next }
  inblock && /^\)/         { inblock=0; next }
  inblock                  { print; next }
  /^replace[[:space:]]/    { print }
' "$gomod")

if grep -q -- "$forbidden" <<<"$replaces"; then
  echo "BOUNDARY VIOLATION (replace directive): $gomod replaces $forbidden (ADR 0010)" >&2
  grep -- "$forbidden" <<<"$replaces" | sed 's/^/  /' >&2
  fail=1
fi
```

Rule C inspects a file rather than packages, so it must not participate in the
`checked -eq 0` vacuity guard — that guard is about package inspection and Rule
C proves nothing about packages.

- [ ] **Step 4: Run the selftest to verify it passes**

Run: `make lint-boundary-replace-selftest`
Expected: PASS — `lint-boundary replace-rule selftest: rule fires correctly`

- [ ] **Step 5: Verify the real tree is still clean**

Run: `make check`
Expected: PASS, including `lint-boundary: clean (2 roots transitively, 13
packages for direct imports) against openits-models`

- [ ] **Step 6: Commit**

```bash
git add scripts/lint-boundary.sh Makefile
git commit -m "fix(ci): enforce ADR 0010's never-replace clause

The boundary lint greps imports and never looked at go.mod, so the
never-replace clause was unenforced while being believed enforced. A
replaced module still imports cleanly, so rules A and B cannot see it.

Rule C parses replace directives textually, which keeps it offline and
lets the selftest point it at a fixture that genuinely does replace the
model module."
```

---

## Task 2: ADR 0013 — absence of evidence is never a state change

The synth engine's iron rule. Its only sources are `AGENTS.md` and the
greenfield spec §4, and that spec is deleted in Phase 2. It is arguably the most
important correctness invariant in the collector and it has no decision record.

**Files:**
- Create: `docs/adr/0013-absence-of-evidence.md`
- Modify: `docs/adr/README.md` (index row)

**Interfaces:**
- Consumes: nothing.
- Produces: ADR 0013, cited by `docs/reference/invariants.md` in Task 9.

- [ ] **Step 1: Probe the rule against the code before writing it**

Run:

```bash
grep -n 'FacetFailed\|FacetError' sdk/model/model.go internal/synth/*.go | head -20
go test ./internal/synth/ -run 'Fail' -v 2>&1 | head -30
```

Record which differs actually implement the rule and which test names prove it.
The ADR's Consequences section cites these by name; do not write the citation
before reading the output.

- [ ] **Step 2: Write the ADR**

`docs/adr/0013-absence-of-evidence.md`, following the house format exactly —
Status, Context, Decision, Consequences, Alternatives considered:

- **Status:** Accepted (2026-08-17). Add a line: *Records a rule that predates
  this ADR; written when its only source, the greenfield architecture spec, was
  retired.*
- **Context:** Poll-based collection cannot distinguish "the device says X" from
  "the device did not answer." A differ that treats a missing read as a changed
  value manufactures events — a fault that never cleared reported as cleared, a
  mode that never changed reported as changed. Downstream these are
  indistinguishable from real transitions, and the collector is the only place
  with enough information to tell them apart.
- **Decision:** A failed or absent facet read emits nothing and leaves previous
  state untouched. Adapters record `model.FacetError` for a facet they tried and
  could not read; the synth engine suspends diffing for that facet. Absence is
  never evidence.
- **Consequences:** Every differ needs a failed-read test (cite the real test
  names from Step 1). A device that goes silent produces silence, not a storm
  of false clears — reachability is reported separately via
  `DeviceStatusChanged`, which is where "we cannot see it" belongs. Cost: a
  genuinely-cleared fault during an outage is not reported until the next
  successful read.
- **Alternatives considered:** *Treat absence as the zero value* — rejected: it
  is exactly the event-manufacturing failure above. *Emit an "unknown" state* —
  rejected: it puts collector-internal uncertainty into the ITS catalog's
  vocabulary, and the catalog has no such concept.

- [ ] **Step 3: Add the index row**

In `docs/adr/README.md`, append to the table:

```markdown
| [0013](0013-absence-of-evidence.md) | Absence of evidence is never a state change |
```

- [ ] **Step 4: Verify**

Run: `make check`
Expected: PASS. Then confirm every claim in the ADR traces to Step 1's output —
no test name appears in the ADR that the probe did not produce.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0013-absence-of-evidence.md docs/adr/README.md
git commit -m "docs: ADR 0013 records the absence-of-evidence rule

The synth engine's iron rule had no decision record. Its only sources
were AGENTS.md and the greenfield spec, and that spec is being retired,
which would have left the rule homeless."
```

---

## Task 3: ADR 0014 — config is the trust boundary

ADR 0011 and ADR 0012 both cite this as established ("a narrow exception to
'config is the trust boundary'"). Nothing established it. Its sources are
`AGENTS.md`, `collector.yaml`, and two specs scheduled for deletion.

**Files:**
- Create: `docs/adr/0014-config-is-the-trust-boundary.md`
- Modify: `docs/adr/README.md` (index row)

**Interfaces:**
- Consumes: nothing.
- Produces: ADR 0014, cited by `docs/reference/invariants.md` in Task 9.

- [ ] **Step 1: Probe the rule against the code**

Run:

```bash
grep -n 'return fmt.Errorf' internal/config/config.go
grep -rn 'ValidateBindable\|Tenant().Validate' internal/config/ internal/subject/ internal/cloudevents/
```

Every refusal-to-start the ADR claims must appear in that output.

- [ ] **Step 2: Write the ADR**

`docs/adr/0014-config-is-the-trust-boundary.md`, same house format, same
"records a pre-existing rule" status note as Task 2:

- **Context:** A cabinet collector runs unattended behind cellular NAT. A
  misconfiguration that is accepted at boot surfaces later as unroutable
  events, mislabelled provenance, or silently wrong data in the lake — all of
  which are discovered downstream, long after the cheap moment to catch them.
- **Decision:** Everything is validated at boot and the collector refuses to
  start on anything it does not understand: unknown vendor/device-kind, tenant
  tokens that would corrupt subject grammar or the source URN, a subject
  template that can never yield a static stream binding, a missing
  `collector_id`. Prefer boot-time failure over publish-time surprise.
- **Consequences:** Cite the real validations from Step 1. Note the tension ADR
  0012 already identified: strictness is correct for data integrity and
  hazardous unattended, because a config a new binary rejects bricks a cabinet
  that then needs a truck roll. That is what makes health-gated rollback
  non-optional — record the tension here rather than weakening the rule.
  Cross-reference the one sanctioned exception: an absent broker at startup is
  a transient, not a config error (fleet plan Phase 1).
- **Alternatives considered:** *Warn and continue* — rejected: the failure
  modes are all silent-and-downstream, so a warning in a cabinet log nobody
  reads is equivalent to no check. *Validate lazily at first publish* —
  rejected: moves the failure to 3am and to a process that is already carrying
  data.

- [ ] **Step 3: Add the index row**

```markdown
| [0014](0014-config-is-the-trust-boundary.md) | Config is the trust boundary; boot fails on the unrecognized |
```

- [ ] **Step 4: Verify**

Run: `make check`
Expected: PASS. Confirm each cited validation exists in Step 1's output.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0014-config-is-the-trust-boundary.md docs/adr/README.md
git commit -m "docs: ADR 0014 records config as the trust boundary

ADR 0011 and 0012 both cite this rule as established. Nothing
established it — it lived only in AGENTS.md, collector.yaml, and two
specs scheduled for deletion."
```

---

## Task 4: Truth-pass ledger — root documents

The audit produces evidence, not opinions. Format follows the Task 0 ledger
(`docs/notes/2026-08-08-task-0-openits-emitter.md`): each load-bearing claim
gets a probe, a verdict, and the command that produced it.

**Files:**
- Create: `docs/notes/2026-08-17-documentation-truth-audit.md`

**Interfaces:**
- Consumes: nothing.
- Produces: the ledger, extended by Tasks 5 and 6 and consumed by Task 7's
  fixes and Phase 2's harvest.

- [ ] **Step 1: Create the ledger with its header and verdict key**

```markdown
# Documentation Truth Audit

**Date:** 2026-08-17
**Method:** Probe, don't read. Every load-bearing claim is checked against the
code, with the command recorded so a reviewer can re-run it.
**Scope:** Every document that survives the documentation-architecture change,
plus every document being harvested. Package-level doc comments included;
inline comments excluded.

## Verdict key

| Verdict | Meaning | Action |
|---|---|---|
| TRUE | Claim holds | Leave |
| DOC WRONG | Document contradicts the code | Fix the document |
| DOC RIGHT, CODE WRONG | The document states the intended bar; the code does not meet it | Fix the code. **Never weaken the document.** Becomes a successor work item |
| UNVERIFIABLE | Nothing can confirm the claim | Delete the claim |
| HOMELESS RULE | The claim's only source is a document being deleted | Needs an ADR or promotion before that document goes |
```

- [ ] **Step 2: Probe `README.md`**

Run each and paste the output into the ledger:

```bash
find internal/vendors -type f | sort                 # vs the <vendor>/<kind>/ claim
sed -n '85,100p' internal/app/app.go                 # vs "signal-status synth"
grep -c 'openits\.' internal/wire/openits/emitter.go # vs "not yet wired"
grep -n 'healthyFixture' internal/vendors/ntcip/asc_test.go  # vs "fixtures"
```

Record four rows. Expected verdicts, to be confirmed rather than assumed:
`internal/vendors/<vendor>/<kind>/` → DOC WRONG; "signal-status synth" → DOC
WRONG; "Not yet wired: additional facets" → DOC WRONG; "fixtures + live SNMP" →
DOC RIGHT, CODE WRONG (ADR 0008's bar is correct; the repo does not meet it).

- [ ] **Step 3: Probe `CONTRIBUTING.md`, `AGENTS.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `collector.yaml`**

For each rule restatement and each path reference:

```bash
grep -n 'internal/\|sdk/\|docs/\|make ' CONTRIBUTING.md AGENTS.md
# then, for every path named above:
ls -d <path> 2>&1
```

Every named path must exist; every rule restatement must match the ADR it
paraphrases. `collector.yaml`: confirm the documented default poll interval
(`5s`) and the `collector_id` pattern against `internal/config/config.go`.

- [ ] **Step 4: Verify the ledger is non-vacuous**

Run: `grep -c '^|' docs/notes/2026-08-17-documentation-truth-audit.md`
Expected: at least 10 rows. A ledger with no findings on documents already known
to contain four defects is evidence the audit was not run.

- [ ] **Step 5: Commit**

```bash
git add docs/notes/2026-08-17-documentation-truth-audit.md
git commit -m "docs: truth audit — root documents probed"
```

---

## Task 5: Truth-pass ledger — ADRs

ADRs are never deleted, so their accuracy matters longest. The known defect:
ADR 0006's status names ADR 0009 but not ADR 0011, which also amends it.

**Files:**
- Modify: `docs/notes/2026-08-17-documentation-truth-audit.md`
- Modify: `docs/adr/0006-tenant-scoped-subjects.md` (status line only)
- Modify: `docs/adr/README.md` if any index row misstates a status

**Interfaces:**
- Consumes: the ledger from Task 4.
- Produces: ledger rows for ADRs 0001–0014.

- [ ] **Step 1: Probe supersession integrity in both directions**

```bash
for f in docs/adr/0*.md; do echo "--- $f"; grep -nE '^\*\*(Status|Supersedes|Amends)' "$f"; done
```

For every "X supersedes/amends Y," confirm Y's status names X. Record each
pair and whether it is bidirectional.

- [ ] **Step 2: Probe each ADR's Decision against the code**

For every ADR whose Decision makes a checkable claim, run a probe and record
it. At minimum:

```bash
grep -n 'openits-models' go.mod                      # ADR 0010's pin shape
ls internal/wire/                                     # ADR 0002's one-package-per-release
grep -rn 'CapCommand' --include='*.go' internal/ sdk/ # ADR 0004's dormant seam
grep -n 'DefaultTemplate' internal/subject/subject.go # ADR 0011's grammar
```

- [ ] **Step 3: Fix ADR 0006's status line**

Replace:

```markdown
**Status:** Partially superseded by [ADR 0009](0009-configurable-subject-templates.md) (2026-07-16)
```

with:

```markdown
**Status:** Partially superseded by [ADR 0009](0009-configurable-subject-templates.md) (2026-07-16)
and further amended by [ADR 0011](0011-namespace-rooted-subject-spaces.md) (2026-08-08)
```

This is a status correction, not a content edit — ADRs stay immutable in
substance.

- [ ] **Step 4: Verify**

Run:

```bash
grep -n '0011' docs/adr/0006-tenant-scoped-subjects.md
make check
```

Expected: the 0006 status now names 0011; `make check` passes.

- [ ] **Step 5: Commit**

```bash
git add docs/notes/2026-08-17-documentation-truth-audit.md docs/adr/
git commit -m "docs: truth audit — ADRs probed; ADR 0006 status names 0011"
```

---

## Task 6: Truth-pass ledger — specs, live plan, and package comments

Covers the six specs about to be harvested (their claims must be probed *before*
Phase 2 promotes them), the surviving fleet plan, the management-surface spec,
`asyncapi.yaml`, and package-level doc comments.

**Files:**
- Modify: `docs/notes/2026-08-17-documentation-truth-audit.md`

**Interfaces:**
- Consumes: the ledger from Tasks 4–5.
- Produces: the harvest whitelist Phase 2 reads — every claim marked TRUE is
  promotable; everything else must be corrected or dropped during promotion.

- [ ] **Step 1: Probe the six harvest specs**

For each of the six specs in spec §9, probe every claim the explanation tier
will inherit. Known defect to confirm:

```bash
sed -n '255,270p' docs/specs/2026-07-12-greenfield-collector-architecture-design.md
grep -n 'DefaultTemplate' internal/subject/subject.go
```

Expected: DOC WRONG — the spec's `openits.<agency>.<site>.<service>.<event>.v<n>`
against the actual seven-token namespace-rooted grammar.

Also probe the repository-layout section (`:74`, `:103`) against
`find internal/vendors -type f`, and §5's adapter SDK description against
`sdk/adapter/adapter.go`.

- [ ] **Step 2: Mark every homeless rule**

```bash
grep -rn 'absence of evidence\|trust boundary' docs/specs/ AGENTS.md
```

Every rule whose only remaining source is a spec being deleted gets a HOMELESS
RULE row. Tasks 2 and 3 resolved the two known ones; this step exists to catch
any third. If one is found, it needs an ADR before Phase 2 deletes its source —
add it to the ledger as a blocking item.

- [ ] **Step 3: Probe `asyncapi.yaml`**

```bash
grep -n 'address:' asyncapi.yaml
grep -n 'DefaultTemplate' internal/subject/subject.go
```

Expected: DOC WRONG on both addresses. Record the correct rendered form for
Task 11 to implement.

- [ ] **Step 4: Probe package doc comments**

```bash
for d in $(go list ./...); do echo "--- $d"; go doc "$d" 2>/dev/null | head -8; done
```

Check each package comment's factual claims — named files, named rules, cited
ADRs. Precedent for staleness exists: the Task 0 ledger found a false module
path assertion in `lint-boundary.sh`'s header comment.

- [ ] **Step 5: Verify the audit is complete**

Run:

```bash
# Every live markdown document must appear in the ledger.
for f in README.md CONTRIBUTING.md AGENTS.md SECURITY.md CODE_OF_CONDUCT.md \
         asyncapi.yaml collector.yaml docs/adr/*.md docs/specs/*.md \
         docs/plans/2026-08-09-fleet-deployment.md; do
  grep -q "$(basename $f)" docs/notes/2026-08-17-documentation-truth-audit.md \
    || echo "NOT AUDITED: $f"
done
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add docs/notes/2026-08-17-documentation-truth-audit.md
git commit -m "docs: truth audit — specs, asyncapi, and package comments probed"
```

---

## Task 7: Apply the DOC WRONG fixes

Fixes only what the ledger marked DOC WRONG. DOC RIGHT/CODE WRONG findings are
recorded as successor work items and the documents are left alone — weakening a
correct bar to match the code is the failure mode this rule exists to prevent.

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`, `AGENTS.md` (only where the ledger found defects)
- Modify: `docs/notes/2026-08-17-documentation-truth-audit.md` (successor items)

**Interfaces:**
- Consumes: the ledger's verdicts.
- Produces: a README whose every claim was probed in Task 4.

- [ ] **Step 1: Fix the adapter layout claim**

In `README.md`, replace:

```markdown
- **Adapters** (`internal/vendors/<vendor>/<kind>/`) own transport
```

with:

```markdown
- **Adapters** (`internal/vendors/<vendor>/`) own transport
```

One package per vendor, one file per device kind — matching
`internal/vendors/ntcip/asc.go` and what `add-vendor-adapter/SKILL.md:43`
already says.

- [ ] **Step 2: Rewrite the Status section against probed reality**

Replace the first paragraph of `## Status` with text asserting only what Task 4
probed:

```markdown
Gen-2 rebuild in progress. The domain model, the synth engine and the full
publish path are complete across six device domains: eight facet kinds, eight
differs, and 26 catalog ce-types plus two collector-health ce-types, each
pinned by a byte-exact golden. Events reach JetStream as CloudEvents in the
NATS reference profile's Tier 2 shape (binary mode, deterministic ULID
`ce-id`, seven-token namespace-rooted subjects).

What is missing is **adapters**. One exists — `ntcip-asc` — producing three of
the eight facet kinds. The other five are modeled, diffed and wired with no
device on the other end, which makes them the best first contribution in the
repo: see [starter tasks](docs/reference/starter-tasks.md).
```

Note the inversion: the old text said facets were unwired and adapters existed.
The truth is the reverse.

- [ ] **Step 3: Correct the fixture claim**

Replace `` `ntcip-asc` adapter (fixtures + live SNMP) `` wherever it appears
with `` `ntcip-asc` adapter (hand-written OID maps + live SNMP) ``, and add a
ledger row marking the gap as successor A's work. ADR 0008 is not edited.

- [ ] **Step 4: Verify no unprobed claim remains**

Run:

```bash
find internal/vendors -type f | sort
grep -n 'internal/vendors' README.md
grep -c 'openits\.' internal/wire/openits/emitter.go   # expect 26
```

Expected: README's layout claim matches the tree; the ce-type count in README
matches the emitter.

- [ ] **Step 5: Commit**

```bash
git add README.md CONTRIBUTING.md AGENTS.md docs/notes/
git commit -m "docs: correct README claims the audit found false

The adapter layout claim named a directory shape the tree does not use,
and the status section had the state of the project backwards: it
reported facets as unwired and adapters as done, when every facet is
wired and adapters are what is missing."
```

---

## Task 8: Delete the sixteen shipped plans

Safe now: nothing harvests from plans. The six specs stay until Phase 2.

**Files:**
- Delete: sixteen files under `docs/plans/`

**Interfaces:**
- Consumes: nothing.
- Produces: a `docs/plans/` containing exactly two files —
  `2026-08-09-fleet-deployment.md` (not built) and this plan.

- [ ] **Step 1: Confirm the survivors before deleting**

Run:

```bash
ls docs/plans/
grep -l 'Status.*not.*implement\|Phase 1' docs/plans/2026-08-09-fleet-deployment.md
```

Expected: seventeen pre-existing plans plus this one; the fleet plan present.

- [ ] **Step 2: Delete**

```bash
git rm docs/plans/2026-07-10-*.md \
       docs/plans/2026-07-12-gen2-plan1-contract-and-spine.md \
       docs/plans/2026-07-16-asc-facets.md \
       docs/plans/2026-07-16-configurable-subjects.md \
       docs/plans/2026-07-16-dms-domain.md \
       docs/plans/2026-08-08-health-subject-separation.md
```

- [ ] **Step 3: Verify exactly two plans remain**

Run: `ls docs/plans/`
Expected: `2026-08-09-fleet-deployment.md` and
`2026-08-17-documentation-architecture.md`, nothing else.

- [ ] **Step 4: Verify nothing live still links to a deleted plan**

Run:

```bash
grep -rn 'docs/plans/2026-07' --include='*.md' --include='*.go' . | grep -v '^./.git'
```

Expected: no output outside `docs/specs/` (which is itself scheduled for
deletion) — any hit in a surviving document must be fixed before committing.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs: delete the sixteen shipped plans

A plan's life ends when it ships. Eleven of these describe a gen-1
codebase that no longer exists — p0-hardening specifies an
internal/runtimex package and Prometheus metrics, neither of which is in
the tree. Git history holds them; a reader browsing docs/ should not
have to work out which plans describe code that still exists."
```

---

## Task 9: `invariants.md` and its enforcement test

The table becomes the sole restatement of enforced rules. The test makes it
impossible for the table to name an enforcer that does not exist.

**Files:**
- Create: `docs/reference/invariants.md`
- Create: `internal/docs/doc.go`
- Create: `internal/docs/invariants_test.go`

**Interfaces:**
- Consumes: ADRs 0013 and 0014 (Tasks 2–3); Rule C (Task 1).
- Produces: the `internal/docs` package and its `repoRoot` constant, which
  Tasks 10 and 11 both use. `tableRows`, `backtickRe` and `manualEscape` are
  local to this task's test.

- [ ] **Step 1: Write the failing test**

`internal/docs/doc.go`:

```go
// Package docs holds tests that keep documentation honest.
//
// Only STRUCTURAL claims are checked — a named enforcer that vanished, a
// config field that appeared, a subject address that drifted. Prose accuracy
// is a review problem and is deliberately not attempted here: a check that
// cannot fail on real decay is worse than no check, because it reads as
// coverage.
//
// It lives under internal/ because it imports internal/config and
// internal/subject. It ships no production code.
package docs
```

`internal/docs/invariants_test.go`:

```go
package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the module root relative to this package's directory.
const repoRoot = "../.."

var backtickRe = regexp.MustCompile("`([^`]+)`")

// manualEscape marks a rule that no automated check enforces. It is spelled
// out so an unenforced rule is visible in the table rather than inferred from
// an empty cell.
const manualEscape = "Review (manual)"

// tableRows returns the body rows of the first markdown table in src, each as
// its trimmed cells. Header and separator rows are skipped.
func tableRows(t *testing.T, src string) [][]string {
	t.Helper()
	var rows [][]string
	seenHeader := false
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !seenHeader {
			seenHeader = true
			continue
		}
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

func TestInvariantsTableNamesRealEnforcers(t *testing.T) {
	path := filepath.Join(repoRoot, "docs", "reference", "invariants.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invariants table: %v", err)
	}

	rows := tableRows(t, string(raw))
	if len(rows) < 5 {
		t.Fatalf("invariants table has %d rows; a near-empty table makes this "+
			"check vacuous", len(rows))
	}

	for _, row := range rows {
		if len(row) < 3 {
			t.Errorf("row %q: want 3 columns (rule, decided by, enforced by)", row)
			continue
		}
		rule, enforcedBy := row[0], row[2]

		names := backtickRe.FindAllStringSubmatch(enforcedBy, -1)
		if len(names) == 0 && !strings.Contains(enforcedBy, manualEscape) {
			t.Errorf("rule %q names no enforcer and is not marked %q", rule, manualEscape)
			continue
		}
		for _, m := range names {
			assertEnforcerExists(t, rule, m[1])
		}
	}
}

// assertEnforcerExists resolves one backticked enforcer. A token containing a
// slash is a path (file or directory); a token starting with "Test" is a Go
// test function that must exist somewhere in the tree.
func assertEnforcerExists(t *testing.T, rule, name string) {
	t.Helper()
	switch {
	case strings.Contains(name, "/"):
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err != nil {
			t.Errorf("rule %q names enforcer %q, which does not exist: %v", rule, name, err)
		}
	case strings.HasPrefix(name, "Test"):
		if !testFuncExists(t, name) {
			t.Errorf("rule %q names test %q, which is not defined anywhere", rule, name)
		}
	default:
		t.Errorf("rule %q names enforcer %q, which is neither a path nor a Test function", rule, name)
	}
}

func testFuncExists(t *testing.T, fn string) bool {
	t.Helper()
	needle := "func " + fn + "("
	found := false
	err := filepath.Walk(repoRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// .git holds no test files and is large enough to dominate the walk.
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if found || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return found
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/docs/ -run TestInvariantsTableNamesRealEnforcers -v`
Expected: FAIL with `read invariants table: ... no such file or directory` —
the document does not exist yet.

- [ ] **Step 3: Write `docs/reference/invariants.md`**

Header paragraph must state the rule that gives the document its purpose: *this
is the only document permitted to restate an enforced rule; everywhere else
links to a row here.* Then:

```markdown
| Rule | Decided by | Enforced by |
|---|---|---|
| Adapters and `sdk/` never import openits-models | ADR 0002 | `scripts/lint-boundary.sh` |
| openits-models is pinned at main HEAD; never a `replace` directive | ADR 0010 | `scripts/lint-boundary.sh` |
| Absence of evidence is never a state change | ADR 0013 | `internal/synth` |
| Config is the trust boundary; boot fails on the unrecognized | ADR 0014 | `internal/config` |
| Subjects are operator-configurable; the CloudEvents envelope is canonical | ADR 0009, ADR 0011 | `internal/subject` |
| Every mapped ce-type has a byte-exact golden | ADR 0008 | `internal/wire/openits/golden_test.go` |
| No fixtures, no merge | ADR 0008 | Review (manual) — automated by the conformance kit in successor A |
```

Each row gets a short paragraph below the table explaining what violating it
looks like in practice, and linking to the deciding ADR. No rule text is
duplicated anywhere else in the repository.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/docs/ -v`
Expected: PASS. If `internal/synth` or any other token fails, the enforcer name
is wrong — fix the table, not the test.

- [ ] **Step 5: Prove the guard can fail**

Temporarily add a row naming `scripts/does-not-exist.sh`, run the test, confirm
it fails with `names enforcer "scripts/does-not-exist.sh", which does not
exist`, then remove the row. A guard that has not been seen to fail is not
known to be a guard.

- [ ] **Step 6: Commit**

```bash
git add docs/reference/invariants.md internal/docs/
git commit -m "docs: invariants table with a test that it names real enforcers

Four rules were restated 28 times across 14 files. This is now the only
document allowed to restate one, and a test asserts every enforcer it
names exists — so the table cannot decay into a list of aspirations."
```

---

## Task 10: `configuration.md` and its coverage test

**Files:**
- Create: `docs/reference/configuration.md`
- Create: `internal/docs/configuration_test.go`
- Modify: `collector.yaml` (link to the reference; keep the rationale)

**Interfaces:**
- Consumes: `repoRoot` from Task 9.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Write the failing test**

`internal/docs/configuration_test.go`:

```go
package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
)

// yamlFields returns every yaml field name reachable from t, dotted for
// nesting. Slices of structs contribute their element type's fields, because
// a device's `id` needs documenting exactly once, not once per device.
func yamlFields(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		name := prefix + tag
		out = append(out, name)

		ft := f.Type
		if ft.Kind() == reflect.Slice || ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out = append(out, yamlFields(ft, name+".")...)
		}
	}
	return out
}

func TestConfigReferenceDocumentsEveryField(t *testing.T) {
	path := filepath.Join(repoRoot, "docs", "reference", "configuration.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read configuration reference: %v", err)
	}
	doc := string(raw)

	fields := yamlFields(reflect.TypeOf(config.Config{}), "")
	if len(fields) == 0 {
		t.Fatal("reflected zero config fields; the check would be vacuous")
	}

	for _, f := range fields {
		if !strings.Contains(doc, "`"+f+"`") {
			t.Errorf("config field %q is not documented in configuration.md "+
				"(expected it to appear as `%s`)", f, f)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/docs/ -run TestConfigReferenceDocumentsEveryField -v`
Expected: FAIL — `read configuration reference: ... no such file or directory`.

- [ ] **Step 3: Confirm the field list before writing the document**

Run:

```bash
grep -n 'yaml:"' internal/config/config.go
```

Expected fifteen names: `region`, `agency`, `agency_unit`, `site`,
`collector_id`, `model_version`, `subject`, `subject.template`, `subject.vars`,
`devices`, `devices.id`, `devices.vendor`, `devices.device_kind`,
`devices.poll_interval`, `devices.connection`. Write the document from this
output, not from `collector.yaml`.

- [ ] **Step 4: Write `docs/reference/configuration.md`**

A table with columns Field, Type, Required, Default, Validation — each field
name in backticks so the test finds it. Validation values come from
`internal/config/config.go`, not from prose: the tenant-token pattern
`^[a-z0-9][a-z0-9-]*$`, the `collector_id` pattern `^[a-zA-Z0-9_-]+$`, the
`poll_interval` default `5s` and its must-be-positive rule, `model_version`
required-non-empty, at-least-one-device, and device-id uniqueness.

Below the table, a short section per non-obvious field pointing at the ADR that
explains it — `collector_id` to ADR 0007 and the observed-by rationale,
`subject` to ADR 0009 and 0011.

- [ ] **Step 5: Trim `collector.yaml` and link out**

Keep every paragraph that explains *why* — the observed-by rationale, why
stream names are derived, why service-first grammars are rejected. Remove the
reference-shaped detail now duplicated in the table (regexes, defaults, the
placeholder inventory) and replace it with a single pointer near the top:

```yaml
# Field-by-field reference — types, defaults, validation:
#   docs/reference/configuration.md
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/docs/ -v`
Expected: PASS.

- [ ] **Step 7: Prove the guard can fail**

Temporarily add a field `Foo string \`yaml:"foo"\`` to `config.Config`, run the
test, confirm it fails with `config field "foo" is not documented`, then remove
it.

- [ ] **Step 8: Commit**

```bash
git add docs/reference/configuration.md internal/docs/configuration_test.go collector.yaml
git commit -m "docs: config reference with a coverage test

Config is the trust boundary (ADR 0014) and its only reference was 130
lines of comments inside the example file. The reference is now
canonical and reflection asserts every field appears in it, so a new
knob without documentation fails the build."
```

---

## Task 11: Fix `asyncapi.yaml` and assert it against the real renderer

The known defect: addresses still use the pre-ADR-0009 grammar
`openits.{agency}.{site}.health.*`, unchanged since the public baseline.

**Files:**
- Modify: `asyncapi.yaml`
- Create: `internal/docs/asyncapi_test.go`

**Interfaces:**
- Consumes: `repoRoot` from Task 9; `internal/subject`.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Determine the correct addresses from the code**

Run:

```bash
cat > /tmp/subj_test.go <<'EOF'
package subject

import "testing"

func TestPrintHealthSubjects(t *testing.T) {
	tmpl, err := New(Config{}, Identity{
		Region: "us-ga", Agency: "metro-atlanta", AgencyUnit: "d01", Site: "cabinet-042",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ ceType, dev string }{
		{"openits-collector.health.collector-started.v1", ""},
		{"openits-collector.health.device-status-changed.v1", "asc-1"},
	} {
		s, err := tmpl.Render(c.ceType, c.dev)
		t.Logf("%s -> %s (err=%v)", c.ceType, s, err)
	}
}
EOF
cp /tmp/subj_test.go internal/subject/zz_print_test.go
go test ./internal/subject/ -run TestPrintHealthSubjects -v
rm internal/subject/zz_print_test.go
```

Record the two rendered subjects in the audit ledger. Write the AsyncAPI
addresses from this output — parameterizing `us-ga`, `metro-atlanta`, `d01` and
the device id back into `{region}`, `{agency}`, `{agency_unit}`, `{device_id}`.

- [ ] **Step 2: Write the failing test**

`internal/docs/asyncapi_test.go`:

```go
package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

// asyncAPIDoc is the sliver of the AsyncAPI document this test needs.
type asyncAPIDoc struct {
	Channels map[string]struct {
		Address string `yaml:"address"`
	} `yaml:"channels"`
}

// The identity the addresses are rendered against. Any values work; they only
// have to be the same on both sides of the comparison.
var testIdentity = subject.Identity{
	Region: "us-ga", Agency: "metro-atlanta", AgencyUnit: "d01", Site: "cabinet-042",
}

// deviceIDFor mirrors what the publisher passes: health events about the
// collector itself carry no device.
func deviceIDFor(ceType string) string {
	if strings.Contains(ceType, "collector-started") {
		return ""
	}
	return "asc-1"
}

func TestAsyncAPIAddressesMatchRenderedSubjects(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "asyncapi.yaml"))
	if err != nil {
		t.Fatalf("read asyncapi.yaml: %v", err)
	}
	var doc asyncAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse asyncapi.yaml: %v", err)
	}
	if len(doc.Channels) == 0 {
		t.Fatal("asyncapi.yaml declares no channels; the check would be vacuous")
	}

	tmpl, err := subject.New(subject.Config{}, testIdentity)
	if err != nil {
		t.Fatalf("build default template: %v", err)
	}

	for ceType, ch := range doc.Channels {
		dev := deviceIDFor(ceType)
		want, err := tmpl.Render(ceType, dev)
		if err != nil {
			t.Errorf("channel %q: renderer rejected it: %v", ceType, err)
			continue
		}
		got := strings.NewReplacer(
			"{region}", testIdentity.Region,
			"{agency}", testIdentity.Agency,
			"{agency_unit}", testIdentity.AgencyUnit,
			"{site}", testIdentity.Site,
			"{device_id}", dev,
		).Replace(ch.Address)
		if got != want {
			t.Errorf("channel %q address drifted:\n  asyncapi: %s\n  rendered: %s",
				ceType, got, want)
		}
	}
}
```

For `collector-started`, `dev` is empty and the renderer substitutes the
device-less literal, so the AsyncAPI address must spell that literal out rather
than use `{device_id}`.

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/docs/ -run TestAsyncAPIAddressesMatchRenderedSubjects -v`
Expected: FAIL with `channel ... address drifted`, showing the stale
`openits.metro-atlanta.cabinet-042.health...` against the rendered
`openits-collector.us-ga.metro-atlanta.d01.health...`.

- [ ] **Step 4: Fix the addresses**

Update both `address:` values in `asyncapi.yaml` to the Step 1 output,
parameterized. Update the surrounding description text where it describes the
old grammar, and add a comment noting that the addresses are asserted against
`internal/subject` by `internal/docs/asyncapi_test.go`.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/docs/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add asyncapi.yaml internal/docs/asyncapi_test.go
git commit -m "fix(docs): asyncapi addresses match the ADR 0011 grammar

The channel addresses had used the pre-ADR-0009 grammar since the public
baseline, so the only machine-readable contract for collector health
events described a subject shape the code stopped emitting a month ago.
A test now renders each address through the real subject template."
```

---

## Task 12: `test-requirements.md` and `starter-tasks.md`

**Files:**
- Create: `docs/reference/test-requirements.md`
- Create: `docs/reference/starter-tasks.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `starter-tasks.md`, linked from `README.md` in Task 7 and from the
  tutorial in Phase 2.

- [ ] **Step 1: Probe the current differ test coverage**

```bash
grep -rn 'func Test' internal/synth/*_test.go | sed 's/(t \*testing.T).*//'
```

The matrix must describe what the existing tests actually do, so a contributor
copying an existing differ test finds the document agrees with the code.

- [ ] **Step 2: Write `docs/reference/test-requirements.md`**

One section per contribution type, each a checklist:

- **A new differ:** first poll (no previous state), no change between polls,
  each axis changing independently, failed read emits nothing (ADR 0013),
  DeviceKind stamped onto every event.
- **A new adapter:** a golden read test per facet, a facet-read-failure test
  producing `model.FacetError` rather than a zero value, connection-parse
  rejection for malformed config, `Descriptor()` capability bits correct.
- **A new ce-type mapping:** a byte-exact golden, and a test asserting no
  mapped ce-type lacks one (the existing pattern in
  `internal/wire/openits/golden_test.go`).
- **Any change:** `make check` and `go test ./... -race`.

- [ ] **Step 3: Probe the starter-task facts**

```bash
grep -rn 'Kind[A-Za-z]* Kind = ' sdk/model/*.go
sed -n '85,100p' internal/app/app.go
grep -oE '"openits\.[a-z-]+\.' internal/wire/openits/emitter.go | sort -u
find internal/vendors -type f
```

Every claim in `starter-tasks.md` must come from this output.

- [ ] **Step 4: Write `docs/reference/starter-tasks.md`**

Open with the leverage argument: five of eight facet kinds are modeled, diffed,
golden-tested and mapped to real ce-types with no adapter producing them, so a
first adapter for one of them touches `internal/vendors/<vendor>/` alone — no
`sdk/model`, no `internal/synth`, no `internal/wire`, and therefore no exposure
to the layering rules that are hardest to learn.

Then a table: facet kind, the differ that consumes it, the ce-types it lights
up, and what a device would have to provide. Cover `dms-status`, `cctv-status`,
`traffic-intervals`, `zone-incidents`, `zone-intervals`.

Close by designating `internal/vendors/ntcip/asc.go` the reference
implementation, naming the three things it demonstrates: per-facet failure
isolation, the absence-of-evidence rule in practice, and the synthesized-index
table read that avoids ~510 round trips.

- [ ] **Step 5: Verify every claim traces to a probe**

Run the Step 3 commands again and check each number in the documents against
the output. Specifically confirm the facet-kind count, the differ names, and
that no listed facet has an adapter.

- [ ] **Step 6: Commit**

```bash
git add docs/reference/test-requirements.md docs/reference/starter-tasks.md
git commit -m "docs: test requirements and starter tasks

Five of eight facet kinds are wired end to end with no adapter producing
them. That makes them the safest first contribution in the repo, and
nothing said so anywhere."
```

---

## Task 13: The hub, the docs lint, and the `make check` wiring

**Files:**
- Create: `docs/README.md`
- Create: `scripts/lint-docs.sh`
- Create: `.claude/skills/README.md`
- Modify: `Makefile`
- Modify: `README.md` (route to the hub)

**Interfaces:**
- Consumes: every document created in Tasks 9–12.
- Produces: `make lint-docs`, part of `make check`.

- [ ] **Step 1: Write the failing lint**

`scripts/lint-docs.sh`:

```bash
#!/usr/bin/env bash
# Two structural checks on documentation.
#
#   A. Every relative markdown link under docs/ resolves to a real file. The
#      documentation tiers link heavily by design — rules live in exactly one
#      place and everything else points at it — so a broken link is not a
#      cosmetic defect, it is a rule becoming unreachable.
#
#   B. Every SKILL.md carries the sections the skill contract requires. Skills
#      are read by agents that will not notice a missing section; they will
#      just proceed without the guardrail.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
checked=0

# ---- A: relative links resolve ---------------------------------------------
# Fenced code blocks are skipped. Documents that TEACH markdown — this repo's
# plans and skills show example link syntax — would otherwise be flagged for
# links that were never meant to resolve from the containing file's directory.
strip_fences() {
  awk '/^[[:space:]]*```/ { infence = !infence; next } !infence { print }' "$1"
}

while IFS= read -r md; do
  dir=$(dirname "$md")
  # [text](target) where target is neither absolute nor a URL nor an anchor.
  strip_fences "$md" | grep -oE '\]\([^)#][^)]*\)' | sed 's/^](//; s/)$//' | while read -r link; do
    case "$link" in
      http*://* | mailto:*) continue ;;
    esac
    target="${link%%#*}"
    [ -z "$target" ] && continue
    if [ ! -e "$dir/$target" ]; then
      echo "BROKEN LINK: $md -> $link" >&2
      exit 1
    fi
  done || fail=1
  checked=$((checked + 1))
done < <({ find docs -name '*.md' -not -path 'docs/specs/*'; \
           ls README.md CONTRIBUTING.md AGENTS.md CODE_OF_CONDUCT.md SECURITY.md; })

# ---- B: skill structure -----------------------------------------------------
required=("## When this applies" "## Invariants" "## Procedure" "## Verify" "## Canonical doc")
skills=0
for skill in .claude/skills/*/SKILL.md; do
  [ -e "$skill" ] || continue
  skills=$((skills + 1))
  for section in "${required[@]}"; do
    if ! grep -qF "$section" "$skill"; then
      echo "SKILL MISSING SECTION: $skill lacks '$section'" >&2
      fail=1
    fi
  done
done

if [ "$checked" -eq 0 ] || [ "$skills" -eq 0 ]; then
  echo "lint-docs: inspected $checked docs and $skills skills — proved nothing" >&2
  exit 2
fi

if [ "$fail" -ne 0 ]; then
  echo "lint-docs: FAILED" >&2
  exit 1
fi
echo "lint-docs: clean ($checked docs, $skills skills)"
```

`chmod +x scripts/lint-docs.sh`.

Note: `docs/specs/` is excluded from the link check. Specs are point-in-time
records and may reference files that have since been deleted; holding history
to a live-link standard would force edits to documents that are supposed to be
frozen.

The root-level documents ARE included. `README.md` is the most-read file in the
repository, and a check that skipped it would miss the links most likely to be
followed.

- [ ] **Step 2: Run it to verify it fails**

Run: `./scripts/lint-docs.sh`
Expected: FAIL — the three existing skills predate the contract and lack every
required section.

- [ ] **Step 3: Write the skill contract**

`.claude/skills/README.md` documents the six-part structure from spec §6.1 —
frontmatter with explicit triggers, When this applies, Invariants (links into
`invariants.md`, never restated), Procedure, Verify (literal commands),
Canonical doc — and states that `scripts/lint-docs.sh` enforces sections 2–6.

Retargeting the three existing skills to this shape is **Phase 2, Task 5**. To
keep this task's deliverable green without doing that work here, the lint's
section check runs only against skills that declare the contract. Add to the
loop, before the section checks:

```bash
  # Skills opt in by declaring the contract version. Retargeting the
  # pre-contract skills is Phase 2; until then they are listed as
  # non-conforming rather than silently skipped.
  if ! grep -qF 'contract: v1' "$skill"; then
    echo "lint-docs: $skill predates the skill contract (Phase 2 retargets it)"
    continue
  fi
```

- [ ] **Step 4: Write `docs/README.md`**

Routes by task, not by tier: "I have never seen this repo" → tutorial; "I want
to add an adapter" → how-to plus starter tasks; "what will fail my PR" →
invariants plus test requirements; "why is it like this" → explanation plus
ADRs; "I need to configure it" → configuration reference. Each entry one line.
Links to documents that do not exist yet are omitted, not stubbed — Phase 2
adds them as it creates them.

- [ ] **Step 5: Route `README.md` to the hub**

Replace the "Why it's built this way: see `docs/adr/`. Full design: …" line —
which currently points at a spec scheduled for deletion — with a pointer to
`docs/README.md`.

- [ ] **Step 6: Wire into `make check`**

```make
lint-docs:
	./scripts/lint-docs.sh
```

Add `lint-docs` to `.PHONY` and to `check`.

- [ ] **Step 7: Run the full gate**

Run:

```bash
make check
go test ./... -race
```

Expected: both PASS, with `lint-docs: clean (N docs, 3 skills)`.

- [ ] **Step 8: Prove the link guard can fail**

Add a link to `docs/README.md` pointing at `does-not-exist.md`, run
`./scripts/lint-docs.sh`, confirm `BROKEN LINK`, then remove it.

- [ ] **Step 9: Commit**

```bash
git add docs/README.md scripts/lint-docs.sh .claude/skills/README.md Makefile README.md
git commit -m "docs: hub, docs lint, and the skill contract

README stops pointing at a design spec that is scheduled for deletion
and routes to the documentation hub instead. The lint asserts relative
links resolve and that contract-declaring skills carry their required
sections."
```

---

## Definition of done

- [ ] `make check` and `go test ./... -race` both green.
- [ ] `docs/plans/` contains exactly two files.
- [ ] `docs/specs/` still contains all seven specs — Phase 2 deletes six after
      harvesting them.
- [ ] Every enforced rule appears in `docs/reference/invariants.md` and nowhere
      else outside the ADR that decided it.
- [ ] The audit ledger has a probed row for every surviving and harvested
      document, and every DOC RIGHT/CODE WRONG finding is recorded as a
      successor work item.
- [ ] Each of the four checks has been seen to fail on a deliberate defect.

## What Phase 2 covers

Written after this plan executes, because its harvest list is the audit's
output: the explanation tier (five documents promoted from probed spec content,
then the six specs deleted), the tutorial, four how-to guides, two honest stubs,
and the skills — three retargeted to the contract, plus `add-transport` and
`review-adapter-contribution`.

### Rule-restatement conversions (added after Phase 1 review)

`docs/reference/invariants.md` states the enforced rules canonically, but the
paraphrases it was meant to replace are still live. Each of these restates at
least one enforced rule with no link into `docs/reference/`, and each must be
converted to a link (or deleted) in Phase 2:

- `AGENTS.md` — six restatements: the two layering rules (`:20-22`, `:23-26`),
  absence-of-evidence (`:27-29`), subjects-vs-envelope (`:30-34`), the testing
  bar (`:47-51`), and config-as-trust-boundary (`:60-62`). Untouched by Phase 1.
- `CONTRIBUTING.md` — the openits-models import rule (`:26`) and
  "no fixtures, no merge" (`:28`).
- `README.md` — the contributing section's adapter-contract bullets.
- `.github/pull_request_template.md` — the layering checkbox (`:9`).
- `.claude/skills/add-vendor-adapter/SKILL.md`,
  `.claude/skills/add-domain-facet/SKILL.md`,
  `.claude/skills/wire-emitter/SKILL.md` — all three, as part of the retarget
  already listed above.

### Probe the three `SKILL.md` files with the ledger method

The truth audit never probed the skills. Its only skill coverage is an
`ls -d` of the directory (ledger `:284-297`) — a check that they exist, not
that anything in them is true. Phase 1 review found a concrete consequence:
`add-vendor-adapter/SKILL.md` sent contributors to wire adapter registration
"into `internal/app`", when registration has always lived in
`cmd/collector/main.go`'s `RegisterAdapters`. That was corrected in place, but
it was found by reading, not by a systematic pass, and `docs/README.md` routes
the single highest-traffic contributor task straight into that file.

Phase 2 must probe all three skills claim-by-claim in the audit's ledger
format — every path, symbol, command, and rule statement — before or as part
of retargeting them, and record the verdicts in a `docs/notes/` ledger like
every other probed surface.

### Citations with no durable home

Phase 2 deletes the six harvested specs. These live `.go` comments cite
"spec §N" and will dangle. Phase 1 repointed the ones with an existing target;
these two have none yet and need one created or the citation dropped:

- `internal/runner/runner.go:3` — cites the greenfield spec §7 for the poll
  loop's jitter / per-poll timeout / panic isolation. No ADR or reference doc
  covers it; the natural home is the explanation tier's `architecture.md`.
- `sdk/model/model.go:9` — cites the architecture spec §4 for the
  "facets are per-device-kind, never per-vendor" governance rail. The manifest
  harvests §4 into `adapter-to-model.md`; repoint once that exists.

Also: the greenfield spec's "rule of three" deferral (no per-vendor overlay
mechanism until ~3 variant adapters exist) is cited by name from two other
harvest specs and has no home outside them — fold one sentence into
`pluggability.md` during harvest or accept losing the rationale
(ledger `:2448-2466`).
