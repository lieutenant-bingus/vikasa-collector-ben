---
name: review-adapter-contribution
description: Review an incoming vendor-adapter contribution to vikasa-collector — run the machine checks, then judge the things no check can see (fixture provenance, absence-of-evidence, facet decomposition, files a contributor should not have touched) and produce a PR-ready verdict. Use this whenever reviewing, triaging, or giving feedback on an adapter PR, a new vendor×device-kind integration, or a change under internal/vendors/ — including when the request is only "take a look at this PR" or "is this ready to merge" and the diff turns out to touch an adapter. Also use it when deciding whether an adapter contribution is safe to accept.
contract: v1
---

# Reviewing an adapter contribution

Adapters are this project's main contribution surface, and reviewing them at
volume is what makes accepting outside integrations sustainable. The cost to
guard against is a reviewer re-deriving the same checklist from scratch on
every PR, getting bored by PR five, and waving through the one violation that
matters.

Two things make adapter review specifically hard, and they are why this skill
exists rather than a checklist in a doc:

- **The most important property is invisible to CI.** Whether fixtures are
  *recorded from a device* or *hand-typed to make the test pass* is
  indistinguishable to every automated check in this repo. Only a human or an
  agent reading the diff can tell.
- **The one existing adapter fails that bar.** `ntcip-asc`'s fixtures are
  hand-typed. A contributor who copies it is copying a known gap, and
  "it matches the existing adapter" is the most plausible-sounding wrong
  argument you will hear.

## When this applies

Reviewing any change under `internal/vendors/`, a new vendor×device-kind pair,
or a PR adding an adapter. It applies even when the request is vague ("can you
look at this?") and the diff turns out to be an adapter.

It does **not** apply to changes that add a facet (`sdk/model`), a differ
(`internal/synth`), or a wire mapping (`internal/wire`). Those are different
contribution types with different bars — see `docs/reference/test-requirements.md`.
If an adapter PR *also* contains those, see "Scope creep" below.

## Invariants

Do not restate these rules; link to them. Each row explains what violating it
looks like in practice:

- [Adapters and `sdk/` never import openits-models](../../../docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models)
- [Absence of evidence is never a state change](../../../docs/reference/invariants.md#absence-of-evidence-is-never-a-state-change)
- [No fixtures, no merge](../../../docs/reference/invariants.md#no-fixtures-no-merge)
- [Config is the trust boundary](../../../docs/reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized)

## Procedure

### Phase 1 — run what can be proven

Prove what a machine can settle, so no finding is argued when it could be
demonstrated. Capture the real output; a review that says "tests appear to
pass" has not run them.

1. `make check` — vet, tests, the boundary lint, its two selftests, docs lint.
2. `go test ./... -race` — poll loops and the publisher are concurrent.
3. `gofmt -l .` — must print nothing.

A red boundary lint is the clearest possible finding: quote its output
verbatim. It names the offending package and import.

### Phase 2 — judge what checks cannot see

Work through the diff. For each item, decide *proven*, *violated*, or
*cannot tell from the diff* — and say which. "Cannot tell" is a legitimate
outcome that asks the contributor a question; a guess dressed as a finding is
not.

**Fixture provenance — the one that matters most.** Are the fixtures recorded
transport responses, or values typed until the test passed? Signals of
hand-typing: round numbers, values that exactly match the assertion, a
suspiciously minimal OID set, no capture metadata, comments explaining what
each value "should" mean. Signals of recording: unexplained extra fields,
values the test ignores, device quirks nobody would invent.
Ask directly if unclear — provenance is a question, not an accusation.

**Absence of evidence — trace it, do not judge it.** A failed or absent read
must produce a `model.FacetError` in `Snapshot.Errors` and leave that facet
out of `Snapshot.Facets`. A zero-valued facet is indistinguishable downstream
from a real reading of zero, so it manufactures events the device never
reported.

This is the easiest item here to get wrong, because it is the easiest to
*feel* checked. Reading the adapter and concluding "it looks like failures are
handled" is not checking it — and a review that says so wrongly is worse than
one that says nothing, because it tells the maintainer a thing was verified
when it was not.

So enumerate every read path that can fail — each transport call, each facet —
and for each, quoting the code, state what actually lands in `Snapshot.Facets`
and what lands in `Snapshot.Errors` when it fails. A path you cannot quote is
a path you have not checked; say that instead of assuming it is fine. Then
confirm facets fail independently: one facet's failure must not remove or
corrupt another's.

**Facet decomposition.** Facets are per-device-kind, never per-vendor. A new
facet kind invented for one vendor's convenience is a design problem, not a
detail — it means the domain model is being bent to fit a device.

**`Descriptor()` capability bits** match what the adapter actually implements.

**Connection parsing rejects malformed config at build time** rather than
dialing something broken and failing later. Config is the trust boundary.

**Scope creep.** An adapter contribution should touch:

- `internal/vendors/<vendor>/<kind>.go` and its test — new files
- one line in `RegisterAdapters` (`cmd/collector/main.go`)
- optionally `collector.yaml`'s example

Anything else deserves a question. Changes to `sdk/model`, `internal/synth`,
or `internal/wire` mean the contributor hit a real gap in the domain model —
which may be legitimate and valuable, but it is a *second* contribution with
its own bar, and it reviews far better as its own PR. Changes to
`internal/config`, `internal/subject` or `internal/publish` in an adapter PR
are almost always a sign something went wrong.

### Phase 3 — write the review

Lead with the verdict so a skim gets the answer. Order findings by severity,
give each a `file:line` and the invariants row it violates, and separate
blocking from non-blocking so the contributor knows what actually gates the
merge. Then say what you *verified clean* — an outside contributor who gets
back only a list of faults learns nothing about what they got right, and this
project wants them to come back.

```markdown
## Adapter review: <vendor>-<kind>

**Verdict:** Merge | Request changes — <n> blocking

### Blocking
- `path/file.go:NN` — what is wrong, and the rule it breaks (link the row).

### Non-blocking
- Observations worth fixing that do not gate the merge.

### Questions
- Things the diff cannot answer. Fixture provenance usually lives here.

### Verified clean
- What you checked and found correct, including the machine checks you ran.
```

## Verify

Before posting, confirm:

- Every machine check in Phase 1 was actually run and its real output quoted —
  not "should pass."
- Every finding names a `file:line` and links an invariants row rather than
  paraphrasing the rule.
- Fixture provenance was addressed explicitly, even if the answer is a question.
- The absence-of-evidence trace is present: every failing read path named, with
  the code quoted showing what it produces. "Failures are handled correctly"
  without that trace is not a finding, it is a hope.
- No finding rests on "the existing adapter does it this way." `ntcip-asc` does
  not meet the fixture bar; matching it is not a defence.
- The review says what was verified clean, not only what failed.

## Canonical doc

`docs/reference/test-requirements.md` — the "A new adapter" section is the bar
a contribution is measured against, and it names the reference tests for each
requirement. `docs/reference/invariants.md` holds the rules themselves.
