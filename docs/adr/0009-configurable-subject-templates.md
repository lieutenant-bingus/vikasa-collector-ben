# ADR 0009: Operator-configurable subject templates

**Status:** Accepted (2026-07-16)
**Supersedes:** the subject-grammar half of [ADR 0006](0006-tenant-scoped-subjects.md)

## Context
ADR 0006 fixed the subject grammar at
`openits.<agency>.<site>.<service>.<event>.v<n>`. Its reasoning about routing
and cardinality holds, but it decided the grammar on every deployment's behalf.
Agencies need to fit the collector into namespaces they already own: different
token names, fewer tokens, more tokens. All three required a code change, and
`openits` as a root was imposed on everyone.

## Decision
Subject grammar is the operator's, via a config template of literal `{name}`
placeholders (no logic, nothing executable). Variables are instance-constants
(operator-defined `vars`, plus `agency`/`site`) or per-event values decomposed
from the ce-type (`service`, `event`, `version`). Omitting the config
reproduces ADR 0006's scheme byte-for-byte.

**ADR 0006's identity decisions are retained unchanged:** CE `type` is the
catalog ce-type verbatim, and CE `source` is `//<agency>/<site>/<device-id>`.
Identity and routing are different concerns — operators own routing; the
envelope stays canonical so a fleet remains interpretable regardless of local
subject choices. This is also what keeps Plan 2's catalog-conformance test
meaningful.

The JetStream stream binding is derived from the template (constants
substituted, truncated at the first per-event token). A template whose leftmost
token varies per event has no static prefix, so its binding would be `>` — a
stream capturing every subject on the server. That is rejected at boot, not
provisioned.

## Consequences
Agencies self-serve their namespace. Boot validation renders every emittable
ce-type, so grammar mistakes fail at startup rather than when a rare event
fires; this required `wire.Emitter` to declare `CETypes()`. Layouts that read
well for fleet-wide consumers (service-first, flat) cannot bind a per-cabinet
stream and are therefore rejected — an honest consequence of the collector
being an edge component. Changing a running cabinet's grammar generally implies
provisioning a new stream.

`{device_id}` is reserved but unsupported: collector-level events
(`collector-started`) have no device, so any template using it could never
render a legal subject for them. Supporting it would require emitters to
declare which ce-types are device-scoped; nothing has asked for that.

## Alternatives considered
Configurable prefix only (rejected: covers "not openits" but not the actual ask
— fewer, more, and renamed tokens). Go `text/template` (rejected: templates
become programs; validation gets much harder and nobody asked for logic in a
subject). Templating `type`/`source` as well (rejected: forfeits catalog
conformance and a canonical fleet identity).
