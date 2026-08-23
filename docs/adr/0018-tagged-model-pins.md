# ADR 0018: The openits-models pin names a release tag

**Status:** Accepted (2026-08-23). Supersedes
[ADR 0010](0010-openits-models-lockstep-pre-v1.md)'s pinning clause; ADR
0010's versioned-emitter-package clause survives unchanged, see the Decision.

## Context
[ADR 0010](0010-openits-models-lockstep-pre-v1.md) pinned openits-models at
`main` HEAD as a pseudo-version, on the reasoning that both repositories are
owned by the same team, move together pre-v1, and would otherwise turn every
models fix found while writing an emitter into a release round trip. It paired
that with a rule stated in
[`invariants.md`](../reference/invariants.md): the pin has to actually track
`main` HEAD rather than quietly go stale. That rule was marked `Review
(manual)` — nothing in CI checked it.

It did not hold. On 2026-08-23 the pin was
`v0.2.3-0.20260807005833-235e8780f44c`, whose commit is *a GitHub Actions
dependency bump in openits-models' own CI* — not a models change at all.
`main` had moved two commits past it, through the v0.3.0 release, one of which
was a breaking change that relocated the `object-class` identity hierarchy and
split the perception zone-interval report in two. The collector was still
emitting `openits-perception-types:object-truck`, an identity that no longer
exists. Nothing failed. It was found by a documentation audit that happened to
compare the pin against the models checkout.

The failure is structural rather than a lapse of attention. "Keep tracking a
moving branch" has no moment at which it is observably violated: there is no
artefact whose shape is wrong, no diff in which the staleness appears, and
`go.mod` is perfectly valid the entire time it is out of date. A reviewer
would have to decode a pseudo-version's embedded timestamp and compare it
against another repository's branch to notice — which is exactly the check
nobody performs on a PR that did not touch `go.mod`.

Meanwhile the premise moved. When ADR 0010 was written openits-models had no
release cadence worth pinning to. It now runs release-please and has cut
v0.2.1, v0.2.2 and v0.3.0, each with a changelog describing what changed and
why. The thing that made tag-pinning expensive — no tags, and a release being
a manual ceremony — is no longer true.

## Decision
**The pin names a semver release tag.** `go get
github.com/Vikasa2M/openits-models@vX.Y.Z`, never `@main`.

**The shape is CI-enforced.** `scripts/lint-boundary.sh`'s Rule D fails the
build when `go.mod` pins the model module at a pseudo-version — the
`<timestamp>-<commit>` form `go get @main` produces. `make
lint-boundary-tag-selftest` points the rule at a fixture carrying the exact
pin this repository used to have and fails unless it trips, per the "every
guard must be shown to fail" rule.

**Freshness stays a review decision, deliberately.** Rule D checks that the
pin names a tag, not that it names the *newest* tag. A lint cannot answer "is
v0.3.0 still current" without reaching the network, and one that did could not
answer it reproducibly — an offline or air-gapped build would fail for reasons
having nothing to do with the tree. What has changed is not that freshness
became machine-checkable but that falling behind is now legible: a release is
a dated artefact with a changelog, and `.github/dependabot.yml` already treats
openits-models as an ordinary gomod dependency whose bumps arrive as pull
requests. Under a pseudo-version pin there was nothing for that flow to
propose.

**The emitter package stays unsuffixed.** ADR 0010 coupled tagged pins to a
versioned package layout — `internal/wire/openits_v1`, `openits_v2`, config
selecting one at boot. That coupling is dissolved here: the two were always
answers to different questions. Tagged pins are about *how the collector
adopts a models release*; versioned packages are about *compiling two models
releases into one binary*, which nothing needs while `model_version` selects
nothing and every deployment runs one catalog version
([ADR 0005](0005-one-catalog-version-per-instance.md)). The split starts when
a fleet genuinely runs mixed versions, which remains ADR 0010's second expiry
trigger and is untouched by this record.

**Lockstep development does not end.** Both repositories are still pre-v1 and
still move together; a models change the collector needs is still made
upstream first. The difference is only that the collector adopts it at a tag
boundary instead of continuously.

## Consequences
A models fix found while writing an emitter now needs a release before the
collector can use it. That is precisely the round trip ADR 0010 refused to
pay, and it is a real cost — but a smaller one than it was, because cutting a
release upstream is now a merge to `main` and release-please does the rest.

Adopting a breaking models change becomes a deliberate, dated act with a
changelog to read, instead of something that arrives under a routine `go get
-u`. The v0.3.0 adoption was the first to work this way and it surfaced two
changes no compiler could have caught — a relocated identityref and a moved
schema-registry revision — which is the argument for the boundary being
visible.

**Rule D can only see the shape of the version string.** A pin can name
v0.3.0 for a year and pass every build. The check converts one failure mode —
an untagged pin nobody notices — into a smaller one: a tagged pin that is
merely old. That residue is stated here rather than papered over, and it is
why the freshness half of the old invariants row survives as a review item
rather than being deleted along with the policy that created it.

The [pin-bump how-to](../how-to/adopt-an-openits-models-release.md) and the
[`wire-emitter` skill](../../.claude/skills/wire-emitter/SKILL.md) both change
their first step: `go get @vX.Y.Z` against a chosen release, and read that
release's changelog, rather than resolving whatever `main` happens to be.

## Alternatives considered
**Keep main-HEAD pins and rely harder on review** (rejected). This is the
option that just failed, and it failed in the way unenforced rules fail —
silently, for sixteen days, past a breaking change. A rule whose enforcement
mechanism is "a reviewer decodes a pseudo-version's timestamp and compares it
to another repository" was never going to hold, and saying so now is cheaper
than discovering it again.

**A freshness check that queries the module proxy** (rejected). It would make
the build non-reproducible and network-dependent, and it inverts who decides
when the collector adopts a release: an upstream merge would start failing
this repository's CI before anyone here had chosen to move. Adoption is a
decision, and decisions do not belong to a lint.

**Pin to a tag but keep the emitter package unsuffixed only until v1**
(rejected as a distinct option; it is what this record already does, without
the deadline). Tying the package split to a version number rather than to the
condition that motivates it — two model releases needed in one binary — is
how ADR 0010 ended up coupling two unrelated things in the first place.
