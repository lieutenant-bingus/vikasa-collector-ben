# Configurable Subject Templates — Design

**Date:** 2026-07-16
**Status:** Approved design, pending implementation plan
**Supersedes (in part):** ADR 0006's fixed subject grammar. The identity half of
ADR 0006 (CE `type` = catalog ce-type verbatim; CE `source` = `//<agency>/<site>/<device-id>`)
is **retained unchanged**. A new ADR (0009) records the reversal.

## 1. Problem

The NATS subject scheme is hardcoded in three places:

- `internal/cloudevents/subject.go` — the literal `openits.` prefix and the
  splice rule (tenant always goes after the first dot-token).
- `internal/publish/publish.go` — the stream binding `openits.<agency>.<site>.>`
  and the stream name `OPENITS-<AGENCY>-<SITE>`.

Agencies need to fit the collector into subject namespaces they already own.
They want different token *names*, *fewer* tokens, and *more* tokens. Today any
of those requires a code change, and `openits` as a root is imposed on every
deployment.

ADR 0006 fixed this grammar deliberately, and its reasoning about routing and
cardinality remains sound — but it decided the grammar on the fleet's behalf.
This design moves grammar ownership to the operator while keeping the parts of
0006 that carry semantics rather than routing.

## 2. Decisions

| Decision | Choice |
|---|---|
| Grammar ownership | Operator, via a config template |
| Template form | Literal `{name}` substitution — no logic, no `text/template`, nothing executable |
| CE `type` | **Unchanged.** Catalog ce-type verbatim — schema identity, and what Plan 2's catalog-conformance test asserts against |
| CE `source` | **Unchanged.** `//<agency>/<site>/<device-id>`; `agency`/`site` stay required identity |
| Default behavior | Omitting `subject:` reproduces today's subjects **byte-for-byte** |
| `{device_id}` | **Not supported in v1** (§4) — reserved, but no template may use it |
| Bare `>` binding | Rejected at boot |

Identity and routing are different concerns. Operators own routing; the envelope
stays canonical so a fleet remains interpretable regardless of local subject
choices.

## 3. Config

```yaml
agency: metro-atlanta          # required: identity, feeds CE source
site: cabinet-042              # required: identity, feeds CE source
model_version: openits/v1

subject:                       # OPTIONAL — omit for today's exact scheme
  template: "{prefix}.{agency}.{site}.{service}.{event}.{version}"
  stream: OPENITS-METRO-ATLANTA-CABINET-042
  vars:
    prefix: openits
```

`subject.stream` is the JetStream stream name. It defaults to the current
`OPENITS-<AGENCY>-<SITE>` (tokens uppercased) so existing deployments are
unaffected; operators running several collectors against one NATS server can set
it explicitly to avoid collisions.

Every field of `subject:` is independently optional and defaults to today's
behavior: a block setting only `stream` keeps the default template, and one
setting only `template` keeps the default stream name. `vars` may be omitted
entirely if the template references only built-ins.

## 4. Template variables

Two kinds, and the distinction is load-bearing (§5):

**Instance-constant** — fixed for the process lifetime:
- Everything under `vars` (operator-defined; arbitrary names)
- `agency` and `site`, exposed automatically from the top-level fields

**Per-event** — vary per published event:
- `{service}`, `{event}`, `{version}` — decomposed from the ce-type

(`{device_id}` would belong here, but is not supported in v1 — see below.)

`{version}` carries the literal `v` (`v1`, not `1`) — it is the ce-type's last
token verbatim, which is what makes the default template byte-compatible.

The built-in names — `agency`, `site`, `service`, `event`, `version`, `device_id`
— are **reserved**. A `vars` entry using one is a boot error rather than a
silent shadow: an operator who redefines `service` would get subjects that
disagree with their own ce-types, and the failure would surface as unroutable
events rather than a config mistake.

### ce-type decomposition

Drop the first token; split the remainder into service / event / version:

```
openits.signal-control.fault-raised.v1
  → service=signal-control  event=fault-raised  version=v1

openits-collector.health.device-status-changed.v1
  → service=health  event=device-status-changed  version=v1
```

Both families decompose identically, which is why one template serves catalog
and health events alike. The first token (`openits` vs `openits-collector`) is
the schema namespace and is **not** a subject token — this preserves today's
behavior, where health and catalog events share a subject root.

A ce-type that is not exactly four dot-tokens is a **boot error**. Every ce-type
the collector emits today and every catalog ce-type in the pinned models release
is four tokens; anything else means an emitter is producing something the subject
layer cannot faithfully route, and guessing would be worse than refusing.

### `{device_id}`

**Not supported in v1.** The rule below ("rejected unless every emittable event
has a device") turns out to reject it unconditionally: `model.CollectorStarted`
is always emittable and is deliberately device-less, since its CE source is the
cabinet rather than a device. Shipping a knob that can never be switched on is
worse than omitting it. The name stays reserved so a later version can add it —
which would require emitters to declare which ce-types are device-scoped —
without colliding with an operator's var. Cardinality (devices × events) remains
the reason ADR 0006 kept device out of the subject.

## 5. Stream binding derivation

The collector provisions a per-cabinet stream, and a stream requires a static
subject filter. Derivation:

1. Substitute instance-constants into the template.
2. Truncate at the first per-event placeholder.
3. Append `>`.

```
{prefix}.{agency}.{site}.{service}.{event}.{version}
  → openits.metro-atlanta.cabinet-042.>
```

Two rules fall out, both enforced at boot:

- **A template whose leftmost token is per-event has no static prefix.** Its
  binding would degrade to `>`, and the stream would swallow every subject on the
  server — including other components'. Rejected.
- **Per-event placeholders must occupy whole tokens.** `pre{service}post` leaves
  no token boundary to truncate on. Rejected.

## 6. Choosing a layout (operator guidance)

This section is written for the config documentation, not just this spec.

The rule of thumb: **coarsest, most-stable tokens leftmost.** NATS makes prefix
matching cheap — stream binding, subject permissions, and upstream aggregation
all key off the left of the subject.

| Layout | Good for | Costs |
|---|---|---|
| **Tenant-first** (default)<br>`{prefix}.{agency}.{site}.{service}.{event}.{version}` | Clean per-cabinet stream binding; prefix-based aggregation upstream; NATS permissions are subject-prefix based, so per-site auth falls out naturally | Cross-fleet subscription by event type needs mid-pattern wildcards (`openits.*.*.signal-control.fault-raised.v1`), hardcoding tenancy depth |
| **Service-first**<br>`{prefix}.{service}.{event}.{version}.{agency}.{site}` | Cross-fleet subscription by event type is a clean prefix | **Rejected at boot** — a cabinet publishes across every service, so no static prefix exists and the binding degrades to `>`. Also forfeits per-site auth |
| **Flat / no tenancy**<br>`{prefix}.{service}.{event}.{version}` | Simplest; matches the catalog's own AsyncAPI addresses | Same `>`-binding rejection. Once events aggregate upstream, cabinets are indistinguishable by subject — consumers must read CE `source` |
| **Deep tenancy**<br>`{prefix}.{region}.{agency}.{site}.…` | Region-level aggregation and auth for large fleets | Longer subjects; every subscriber pattern encodes the depth |
| **Device in subject**<br>`…{site}.{device_id}.{service}.…` | *(Not available in v1 — §4.)* Would suit subscribing to a single intersection, or per-device auth | Cardinality = devices × events; device is already in CE `source` |

Two warnings worth stating in the docs:

- **Adding a token later is a breaking change for subscribers.** Wildcard
  patterns encode depth, so `openits.*.*.signal-control.>` silently stops
  matching when a `region` token appears. Choose depth up front.
- **The local stream binding is the real constraint.** Layouts that read nicely
  for fleet-wide consumers often cannot bind a per-cabinet stream at the edge.
  The collector's job is local-device → local-JetStream; subject layouts that
  serve only the upstream aggregate are the wrong trade here.

## 7. Boot validation

Config is the trust boundary; all of this is fail-fast (spec §6):

- Template parses; braces balanced; no empty placeholder `{}`.
- Every placeholder is built-in or defined in `vars`.
- No `vars` entry shadows a reserved built-in name (§4).
- Every `vars` value is a legal NATS token: non-empty, no `.`, `*`, `>`, or
  whitespace. Operator-defined tokens get this looser rule rather than
  `Tenant.Validate`'s `^[a-z0-9][a-z0-9-]*$`, because that regex exists to keep
  `agency`/`site` safe inside CE `source` (a URI) — a constraint that does not
  apply to a token that only ever appears in a subject. `agency` and `site`
  keep the stricter rule precisely because they do appear in `source`.
- **Every ce-type the collector can emit** renders to a legal subject that falls
  inside the derived binding.
- Binding is not bare `>`.
- No per-event placeholder splits a token.

The "every ce-type" check requires knowing what the emitter chain can produce, so
`wire.Emitter` gains:

```go
// CETypes returns every ce-type this emitter can produce, sorted.
CETypes() []string
```

This is a small breaking change to an internal interface (one implementation
today). It pays for itself twice: boot validation becomes exhaustive rather than
sampled, and the AsyncAPI conformance test can drop its hand-maintained `samples`
list and drive from the real set — closing the gap where a new health event added
without a sample would go undetected.

## 8. Error handling

- **Boot:** every failure above is fatal with a message naming the template, the
  offending placeholder or token, and why. No partial starts.
- **Runtime:** rendering cannot fail after boot validation passes — per-event
  values come from ce-types already proven to decompose, and instance-constants
  are fixed. A render that somehow produces an illegal subject is a programmer
  error: log loudly and drop the event rather than publish to a corrupt subject.
  It must never panic a runner.

## 9. Testing

- **Back-compat is the headline guard:** the existing byte-literal subject
  goldens stay *unchanged*. With no `subject:` block the default template must
  reproduce them exactly. If those goldens need editing, the change is wrong.
- **Table tests per layout in §6**, including the ones that must be *rejected*
  (service-first, flat, split-token, bare `>`).
- **Binding derivation** table tests, including the truncation point.
- **Boot-validation rejection tests:** unknown placeholder, illegal token value
  (`.`/`*`/`>`/empty/whitespace), any use of `{device_id}` (unsupported in v1),
  malformed ce-type.
- **e2e** with a non-default template: assert events land on the custom subject
  and the provisioned stream captures them.
- **Guards must be shown to fail.** Per the boundary-lint lesson this session — a
  check that has never failed is indistinguishable from one that cannot fail —
  each new validation gets a test proving it rejects, not merely that it accepts.

## 10. Migration

No forced migration. `subject:` is optional and its default is today's scheme,
so existing configs produce identical bytes on the same stream. Adopting a custom
template is a config change plus a restart; because the stream binding is derived,
a changed template generally implies a **new stream** — operators changing the
grammar of a running cabinet must expect to provision one, and the docs say so.

## 11. Out of scope

- **Per-event or per-device template overrides.** One template per instance.
- **Templating CE `type` or `source`** (§2) — forfeits catalog conformance and a
  canonical fleet identity, respectively.
- **Subject-based auth configuration.** The collector publishes; NATS
  permissions are the operator's to grant.
- **Rewriting `asyncapi.yaml` addresses per template.** Its documented addresses
  become the *default* rendering; a templated deployment's actual subjects are
  derived from its own config. Noted in that file rather than generated.
