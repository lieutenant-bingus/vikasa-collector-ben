# ADR 0015: The CE-source URN scheme

**Status:** Accepted (2026-08-17). Records a scheme that predates this ADR;
written when its only source, `docs/specs/2026-07-21-openits-models-emitter-design.md`,
was retired. Supersedes the CE-source clauses of [ADR 0006](0006-tenant-scoped-subjects.md)
and [ADR 0009](0009-configurable-subject-templates.md) only — their subject-grammar
and CE-type decisions stand as written.

## Context
ADR 0006 declared CE `source` = `//<agency>/<site>/<device-id>` and ADR 0009
restated it as "retained unchanged." Neither is what the collector emits.
`SourceFor` (`internal/cloudevents/subject.go`) builds:

```
urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<device-id>
```

Until now the only accurate description of this format lived in
`docs/specs/2026-07-21-openits-models-emitter-design.md`, one of the specs the
documentation-architecture pass deletes after harvest. That would have left
the actual scheme with no durable home while two ADRs permanently assert a
format the code has never produced. This matters beyond tidiness: `EventID`
(`internal/cloudevents/eventid.go`) hashes the literal bytes of `source` as
the first field of the `ce-id` digest, so every downstream consumer computing
or verifying an id depends on this string's exact shape. A drift between what
this document says and what `SourceFor` emits is not cosmetic — it is a
silent id mismatch.

## Decision
CE `source` is the profile URN
`urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>`, built by
`SourceFor(tenant, deviceKind, deviceID)`.

**Entity-kind is not the collector's `DeviceKind`.** `entityKindFor` maps the
collector's internal vocabulary to the profile's:

| Collector `deviceKind` | Profile entity-kind |
|---|---|
| `asc` | `controller` |
| `dms` | `sign` |
| `cctv` | `cctv` |
| `traffic-sensor` | `traffic-sensor` |
| `perception` | `perception` |
| `""` (no device) | `collector` |

A camera doing analytics is deliberately allowed to appear under two entity
kinds (`cctv` and `perception`) for the same physical device: entity kind
follows the event family, not the chassis.

**An unmapped device kind emits nothing, never a guess.** If `entityKindFor`
has no mapping for a kind (for example `rsu`, which the collector does not
yet model), `SourceFor` returns `""` and the caller drops the event with a
warning (`internal/app/app.go`: "event dropped: no ce-source entity kind for
device kind") rather than falling back to the raw kind string. A URN
asserting an entity type upstream does not define would be inherited by
every consumer that parses `ce-source`; a dropped event is recoverable, an
invented vocabulary term in a canonical identity field is not.

**The identity triple is region/agency/agency_unit, not the full tenant.**
`Tenant` carries a fourth field, `Site`, but `Site` is never a URN segment
for a device-scoped event — it is used elsewhere (stream naming, health
context), and the profile's notion of "where" is the three-token triple.

**Device-less events use the tenant's site as the id.** When `deviceID` is
empty — the boot event is the only current case — `SourceFor` substitutes
`t.Site`, making the collector the subject of its own event. This is the one
place `Site` reaches the URN, and only as a fallback identifier, not as an
identity segment alongside region/agency/agency_unit.

**This format is a hard contract, not an implementation detail.** Because
`EventID` hashes `source` verbatim, changing the token order, the entity-kind
vocabulary, or the fallback rule changes every event id a fleet has ever
produced. Any future change to `SourceFor` is an id-breaking change and needs
its own ADR, not a patch to this one.

## Consequences
The URN now has exactly one canonical description, in a document that is not
scheduled for deletion. ADR 0006 and ADR 0009 keep their subject-grammar and
CE-type decisions — those are unaffected and remain correct — but their
CE-source prose is superseded by this ADR and must not be read as current;
their status lines are updated to point here, and their bodies are left
otherwise untouched, per this repository's ADR-immutability convention.

Downstream consumers that parse `ce-source` to recover entity kind, region,
agency, or agency_unit now have one URN grammar to code against instead of
two stale ones and one deleted spec. A future entity-kind addition (a new
device kind) is a one-line change to `entityKindFor`'s table, not a format
change — it does not touch token order and does not break existing ids.

Cost: this ADR is a correction, not a new decision — nothing about the
collector's behavior changes. Its only effect is moving the URN's ground
truth from a document about to disappear into one that will not.

## Alternatives considered
**Fall back to the raw `deviceKind` string when unmapped** (rejected, per
`entityKindFor`'s own reasoning): would silently mint a URN vocabulary term
upstream never defined, and every consumer parsing `ce-source` would inherit
the invention. Dropping the event with a warning is the safer failure mode —
recoverable once a mapping is added, versus an entity-kind string baked into
already-published ids that nothing agreed to.

**Include `site` in the identity segment**, making it a four-token tuple
alongside region/agency/agency_unit (rejected): the profile's identity is the
region/agency/agency_unit triple; `site` is a collector-local concern used for
stream naming and health context, not part of what the URN asserts about a
device's tenancy. Folding it in would conflate two different scopes in one
field.

**Keep ADR 0006's original `//<agency>/<site>/<device-id>` scheme**
(superseded by the scheme this ADR records): it carries no region or
agency_unit token and no entity-kind segment, so a consumer cannot recover
entity type or the operator's full tenant hierarchy from `source` alone
without also parsing the subject. The `urn:openits:` scheme carries both
directly in the identity field, at the cost of being a different shape than
what ADR 0006 and ADR 0009 originally promised — which is precisely the drift
this ADR exists to correct.
