# Architecture Decision Records

Why the collector is built the way it is. Format per record: Status,
Context, Decision, Consequences, Alternatives considered. Records are
immutable once accepted; reversals get a new ADR that supersedes the old.

| # | Decision |
|---|---|
| [0001](0001-greenfield-rebuild.md) | Rebuild the collector greenfield |
| [0002](0002-domain-model-and-wire-emitter-boundary.md) | Collector-owned domain model + versioned wire emitters |
| [0003](0003-stable-sdk-in-tree-adapters.md) | Stable SDK, in-tree adapters (Telegraf model) |
| [0004](0004-pull-only-state-and-event-readers.md) | Pull-only; StateReader vs EventReader split by semantics |
| [0005](0005-one-catalog-version-per-instance.md) | One catalog version per collector instance |
| [0006](0006-tenant-scoped-subjects.md) | Tenant-scoped NATS subjects; CE type = catalog ce-type |
| [0007](0007-collector-owned-health-schema.md) | Collector-owned health event schema |
| [0008](0008-fixture-golden-testing-bar.md) | Fixture-golden testing bar for adapters |
| [0009](0009-configurable-subject-templates.md) | Operator-configurable subject templates (supersedes 0006's grammar) |

Companion spec: `../specs/2026-07-12-greenfield-collector-architecture-design.md`
