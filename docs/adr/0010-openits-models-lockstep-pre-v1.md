# ADR 0010: Track openits-models HEAD in lockstep until v1

**Status:** Accepted (2026-08-08)
**Amends:** 0002 (its dependency-pinning clause only; the boundary rule is
untouched). 0005 stands as written.

## Context
ADR 0002 requires openits-models be consumed as tagged semver releases,
never a `replace` on a moving checkout. That rule was written for the
world it anticipates: independent release cadences, external
implementers, deployments migrating at different speeds.

None of that is true yet. Both repositories are owned by the same team,
neither has a consumer outside it, and openits-models is pre-v1 — its own
vendor guide says main "carries no compatibility promise between commits,"
which is a statement about what a *tag* buys you there today.

Meanwhile the first wire emitter has to be written against some version,
and the tagged line (v0.2.2, 2026-07-22) already trails main by schema
corrections the emitter must map against. Requiring a tag per pin makes
every model fix discovered while writing the emitter a release round trip
— during exactly the phase when those fixes are most frequent, because
writing the emitter is what finds them.

## Decision
Until openits-models reaches v1.0.0, the collector pins it at a **main
HEAD pseudo-version** (`go get github.com/Vikasa2M/openits-models@main`)
and the two repositories move in lockstep.

Still never a `replace` directive. The pin remains a real, reproducible
module version recorded in `go.mod` and resolved by CI like any other
dependency — a pseudo-version names an immutable commit, so builds stay
deterministic. That is the property ADR 0002's rule actually protects,
and it is preserved.

The boundary rule is unaffected: only `internal/wire` may import
openits-models, and `scripts/lint-boundary.sh` still fails the build
otherwise. It matches on the repo name, so the pseudo-version changes
nothing about that check.

Two ADR 0002/0005 consequences are **deferred, not repealed**:

- The one-package-per-pinned-release layout (`internal/wire/<version>`)
  starts at the first tagged pin. During lockstep exactly one models
  version is ever compiled in, so the emitter package is
  `internal/wire/openits`, unsuffixed. A version suffix naming a
  pseudo-version would be ceremony that documents nothing.
- `model_version` in config (ADR 0005) keeps its boot validation and its
  refuse-to-start behavior, but selects from a single option.

**This ADR expires at openits-models v1.0.0**, or earlier the moment any
of the following becomes true — each is the tagged rule's original premise
arriving for real:

- a consumer outside this team pins openits-models;
- the collector needs two model versions compiled side by side (ADR 0002
  scenario S2 actually occurring);
- openits-models adopts a compatibility promise between releases.

At that point the collector returns to tagged pins and the versioned
emitter-package layout without a further ADR — this one already says so.

## Consequences
A models fix and its collector adoption land in one change instead of one
release cycle, which is worth real time while the emitter and the schema
are being reconciled against each other.

The costs are that a models commit can break the collector build with no
release note in between, and `go.mod` stops reading as a human-legible
version. Both are bounded by lockstep ownership: the same people write
both sides, and the collector's golden tests (ADR 0008) fail loudly on any
wire change rather than shipping it. Neither is bounded once someone
outside the team is pinning, which is why that is an expiry trigger and
not a "revisit sometime."

A pin bump becomes `go get -u github.com/Vikasa2M/openits-models@main`
plus whatever golden diffs it produces. Every golden diff is still a
decision — confirm each against the models change, never regenerate
blindly. That workflow is unchanged from ADR 0002's S1.

## Alternatives considered
**Cut a v0.3.0 tag and pin it** (rejected for now; this is the end state,
reached at v1). Nearly free and strictly more legible, but it turns every
model correction found while writing the emitter into a tag-and-bump round
trip, and there is currently nobody for whom the tag carries information
the commit does not. The tag starts paying the moment someone else pins
it — which is the first expiry trigger above.

**`replace` to a local checkout** (rejected, and still forbidden). Breaks
reproducibility outright: the build would depend on a working tree CI and
other contributors do not have. This is the case ADR 0002's rule exists
for and nothing here weakens it.

**Stay on v0.2.2** (rejected). The pinned line predates the schema
corrections the emitter maps against, so the first emitter would be
written against a contract already known to be superseded, then rewritten
immediately. The delta is small — one added ce-type we do not emit, and
nine of ten mapped proto messages byte-identical — but it is small in a
direction that argues for moving, not for staying.
