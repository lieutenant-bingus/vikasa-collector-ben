# Architecture Decision Records

Why the collector is built the way it is. Each record captures one decision
at the time it was made, in the same order every time: Status, Context,
Decision, Consequences, Alternatives considered.

**Records are immutable once accepted.** A reversal or refinement gets a new
ADR that *supersedes* (replaces) or *amends* (changes part of) the old one.
The old record's body is never rewritten. Only its **Status** block gains a
dated pointer to the newer record, or a dated note where the code has since
moved: something implemented, renamed, or still unbuilt. Read the Status block
before the body, because a body can be accurate history and still describe
something that is no longer true. ADRs are never deleted.

**Read first:** [0016](0016-collector-as-transitional-shim.md) (what the
collector is for), [0002](0002-domain-model-and-wire-emitter-boundary.md) (the
wire boundary), [0004](0004-pull-only-state-and-event-readers.md) with
[0021](0021-sdk-interfaces-ship-with-their-first-implementer.md) (the adapter
contract), [0013](0013-absence-of-evidence.md) (the synth rule), and
[0014](0014-config-is-the-trust-boundary.md) (boot validation).

| # | Decision | Status |
|---|---|---|
| [0001](0001-greenfield-rebuild.md) | Rebuild the collector greenfield | Accepted 2026-07-12 |
| [0002](0002-domain-model-and-wire-emitter-boundary.md) | Collector-owned domain model + versioned wire emitters | Accepted 2026-07-12. Pinning clause amended by 0010, then restored by 0018. Versioned emitter packages not started |
| [0003](0003-stable-sdk-in-tree-adapters.md) | Stable SDK, in-tree adapters (Telegraf model) | Accepted 2026-07-12 (note on the adapter path) |
| [0004](0004-pull-only-state-and-event-readers.md) | Pull-only; StateReader vs EventReader split by semantics | Accepted 2026-07-12. Amended by 0021 |
| [0005](0005-one-catalog-version-per-instance.md) | One catalog version per collector instance | Accepted 2026-07-12 |
| [0006](0006-tenant-scoped-subjects.md) | Tenant-scoped NATS subjects; CE type = catalog ce-type | Partially superseded: grammar by 0009 (default replaced again by 0011), CE source by 0015. CE type and content-addressed id stand |
| [0007](0007-collector-owned-health-schema.md) | Collector-owned health event schema | Accepted 2026-07-12. Amended by 0019 |
| [0008](0008-fixture-golden-testing-bar.md) | Fixture-golden testing bar for adapters | Accepted 2026-07-12 |
| [0009](0009-configurable-subject-templates.md) | Operator-configurable subject templates | Accepted 2026-07-16. Supersedes 0006's grammar. Amended by 0011; CE-source clause superseded by 0015 |
| [0010](0010-openits-models-lockstep-pre-v1.md) | Track openits-models HEAD in lockstep until v1 | Accepted 2026-08-08; amends 0002. Pinning clause superseded by 0018; no-`replace` clause stands |
| [0011](0011-namespace-rooted-subject-spaces.md) | Namespace-rooted subject spaces, one stream per namespace | Accepted 2026-08-08. Amends 0009 |
| [0012](0012-host-executed-updates.md) | Host-executed updates; the collector participates but never self-updates | Accepted 2026-08-09. Partly implemented: expected-version drift reporting not built |
| [0013](0013-absence-of-evidence.md) | Absence of evidence is never a state change | Accepted 2026-08-17 |
| [0014](0014-config-is-the-trust-boundary.md) | Config is the trust boundary; boot fails on the unrecognized | Accepted 2026-08-17. Broker-absent exception implemented by 0020 |
| [0015](0015-ce-source-urn-scheme.md) | The CE-source URN scheme | Accepted 2026-08-17. Supersedes the CE-source clauses of 0006 and 0009 |
| [0016](0016-collector-as-transitional-shim.md) | The collector is a transitional shim; synthesis stays here | Accepted 2026-08-18 |
| [0017](0017-durable-synth-state.md) | Synth's previous state is durable, in the cabinet's own JetStream KV | Accepted 2026-08-18; implemented 2026-10-13. Amended by 0020 |
| [0018](0018-tagged-model-pins.md) | The openits-models pin names a release tag | Accepted 2026-08-23. Supersedes 0010's pinning clause |
| [0019](0019-collector-self-health.md) | Collector self-health: a periodic heartbeat on a per-instance subject | Accepted 2026-10-12, amended 2026-10-14 (comm-health per device kind, with a fallback); implemented. Amends 0007 |
| [0020](0020-broker-absence-is-latency.md) | Broker absence is latency, not loss: background connect, held synthesis, bounded publish queue | Accepted 2026-10-12; implemented 2026-10-13. Implements 0014's exception; amends 0017 |
| [0021](0021-sdk-interfaces-ship-with-their-first-implementer.md) | SDK interfaces ship with their first implementer | Accepted 2026-10-13. Amends 0004 |
| [0022](0022-transport-security-and-credentials.md) | Transport security and credentials: creds file not URL, plaintext only on loopback, least-privilege broker user | Accepted 2026-10-15 |
| [0023](0023-ship-a-binary-and-a-runtime-contract.md) | Ship a binary and a runtime contract; packaging is the deployer's | Accepted 2026-10-20 |
| [0024](0024-openits-models-v0.8-domain-changes.md) | Domain changes for openits-models v0.8.0: cabinet output split from coordination mode, fault first-observed, comm-lost last-contact | Accepted 2026-10-20. Amends 0013's application to the signal facet and 0019's reachability fallback |
| [0025](0025-event-reader-with-checkpointing.md) | **Proposed** — EventReader, with a core-owned checkpoint: no backfill, publish then advance, adapter-owned resync. Would amend 0021 |
| [0026](0026-detector-samples-carry-their-collection-period.md) | **Proposed** — detector samples carry their collection period: per-period totals reported once, not delta'd |

Start at the [documentation hub](../README.md) for everything that is not a decision record.
