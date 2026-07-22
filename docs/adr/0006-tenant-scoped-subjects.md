# ADR 0006: Tenant-scoped NATS subjects; CE type = catalog ce-type

**Status:** Partially superseded by [ADR 0009](0009-configurable-subject-templates.md) (2026-07-16)

The subject *grammar* below is now the operator's, configured per deployment;
ADR 0009's default reproduces it exactly. The identity decisions — CE `type` =
catalog ce-type verbatim, CE `source` = `//<agency>/<site>/<device-id>`,
content-addressed CE `id` — are **retained unchanged**.

## Context
The generated AsyncAPI in openits-models defines channel addresses as bare
ce-types (`openits.signal-control.fault-raised.v1`) with no tenancy. Events
from many cabinets eventually aggregate upstream; consumers need to route
by agency/site/service without parsing payloads.

## Decision
Two distinct concepts. CE `type` = the catalog ce-type verbatim (schema
identity, matches AsyncAPI exactly). NATS subject = the ce-type with tenant
spliced after the first token: `openits.<agency>.<site>.<service>.<event>.v<n>`.
Device identity lives in CE `source` (`//<agency>/<site>/<device-id>`), not
the subject. CE `id` is content-addressed (deterministic hash) so JetStream
dedup survives restarts. Tenant tokens are validated (`^[a-z0-9][a-z0-9-]*$`)
so they can never corrupt subject grammar.

## Consequences
One stream binding per cabinet (`openits.<agency>.<site>.>`), prefix-based
upstream aggregation, wildcard subscription by service or event across
sites. AsyncAPI documents the address with parameters. Subject-per-device
cardinality explosion is avoided.

## Alternatives considered
Subject = ce-type verbatim (rejected: routing/aggregation too weak once
events leave the cabinet); version-first hierarchy `openits.v1.<agency>...`
(rejected: diverges most from catalog strings for little gain).
