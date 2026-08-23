# ADR 0002: Collector-owned domain model + versioned wire emitters

**Status:** Accepted (2026-07-12)
Amended by [ADR 0010](0010-openits-models-lockstep-pre-v1.md) (2026-08-08) — its
dependency-pinning clause only; the boundary rule is untouched. That amendment
is itself superseded by [ADR 0018](0018-tagged-model-pins.md) (2026-08-23),
which restores this record's original position: openits-models is consumed as
a tagged semver release. The versioned-emitter-package clause below is
unaffected and has not started — see ADR 0018's Decision for why tagged pins
and versioned packages were decoupled.

## Context
The collector must survive three model-change scenarios: (S1) codegen
rename/renumber churn in openits-models, (S2) different deployments pinned
to different model releases at once, (S3) wholesale replacement of
openits-models someday. With 10+ contributed vendor adapters planned, any
design where adapters touch wire types makes each scenario an
every-adapter problem.

## Decision
Adapters produce only collector-owned `sdk/model` types (facets + events).
Exactly one layer, `internal/wire/<version>`, imports openits-models and
maps domain events to `(proto payload, ce-type)`. The rule is mechanical:
CI fails if `sdk/` or `internal/vendors/` transitively import
openits-models. openits-models is consumed as tagged semver releases —
never a `replace` on a moving checkout.

## Consequences
S1 = edit one emitter package. S2 = compile two emitter packages, config
picks one. S3 = write a new emitter family. Cost: a permanent mapping layer
(every event exists in domain + mapping form) and a second schema to govern
— accepted because the mapping is dumb, golden-tested, and cheaper than
coordinating breaking changes across contributed adapters. The domain model
may be richer than any wire version; gaps become explicit emitter
map-or-drop decisions instead of collection blockers.

## Alternatives considered
Pin openits-models harder and shim on majors (rejected: fails S2/S3
structurally; makes proto types the contributor API). Declarative mapping
engine (rejected as foundation: mapping DSLs grow into bad programming
languages; kept as a possible future library inside adapter families).
