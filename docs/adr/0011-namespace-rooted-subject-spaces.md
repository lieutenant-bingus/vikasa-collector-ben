# ADR 0011: Namespace-rooted subject spaces, one stream per namespace

**Status:** Accepted (2026-08-08)
**Amends:** 0009 (its default-grammar and single-binding consequences). 0006
and 0007 stand as written.

## Context
The collector publishes two families of event onto one subject space: ITS
domain events from the openits catalog, and collector-owned health events
(ADR 0007). They shared a root because the pre-template scheme did —
`subject.decompose` parsed the ce-type's namespace and deliberately discarded
it — and at the time nothing distinguished them.

Three things now do, in ascending order of durability:

A Tier 2 conformance harness pointed at `openits.>` flags every health event,
because health carries `openits-collector.*` ce-types that the profile's
ce-type regex rejects. That is a false negative on an otherwise-conformant
deployment, and "ignore those errors" trains people to ignore harness output.

Retention differs. Health is high-churn operational state with a short useful
life; telemetry is archival. One stream forces one retention policy, wrong for
one of them.

Access control differs. With a shared root an operator cannot grant `openits.>`
to a data platform without also exposing collector internals.

The general shape is that **collector-internal traffic and ITS-domain traffic
are different concerns**, and health is merely the first thing to cross that
line rather than a special case.

## Decision
The ce-type's namespace is the subject root. `{namespace}` becomes a per-event
template token and the leftmost token of the default grammar:

```
{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}
```

Catalog events render on `openits.…`, health on `openits-collector.…`, from
one template that needs to know neither name.

`Binding()` becomes `Bindings()`, returning one static subject filter per
namespace the emitters can produce. A single filter is no longer possible: the
leftmost token varies per event, so one filter would be `>` and would capture
every subject on the server, including other tenants sharing the broker.

The publisher provisions one stream per binding. Stream names derive from the
binding — `openits.us-ga.metro.d01.>` becomes `OPENITS-US-GA-METRO-D01` — so a
stream and the filter it captures cannot disagree. The `subject.stream` config
key is removed: with a stream per namespace a single configured name is
meaningless, and a per-namespace block would be a second source of truth.

## Consequences
Two streams to provision, monitor, and back up rather than one. Subject
permissions become expressible per family. A consumer wanting both subscribes
to two roots, or to `>`.

The default grammar and both bindings changed, so any stream provisioned
against the old five- or seven-token single root must be reprovisioned. Free
only because there are no deployments — this is the same pre-1.0 window ADR
0009's reset used, and it does not come round again.

The no-static-prefix guard moved from `subject.New` to `Bindings`: whether the
leftmost token varies can only be answered once the namespace is substituted.
Both are boot-time, so a bad grammar is still a refusal to start. `config.Load`
keeps an early structural check (`ValidateBindable`) so a hopeless template is
still rejected at the trust boundary rather than surviving to provisioning.

**Scope limit, recorded deliberately.** "One stream per namespace" holds for
outbound event families and does NOT generalise to everything a namespace may
carry. A collector control channel would be inbound and carry write authority:
it wants tighter subject permissions than health, and probably a work-queue
stream — or no stream at all, if it is request/reply. Under this rule it would
share health's stream purely for sharing a namespace, which would be wrong.
Splitting bindings more finely, per (namespace, service class), is the likely
evolution and is additive. This is written down so the next reader sees a rule
with a known boundary rather than one they assume generalises.

Not decided here: ADR 0004 remains pull-only, and the subject engine is
publish-only — `Render` has no subscribe-pattern counterpart. An inbound
channel needs both, and neither is required to keep this decision open.

Retention is not yet differentiated: both streams get identical config today.
Separating the roots is what makes differing retention *possible*; choosing the
values is an operational decision with no current requirement.

## Alternatives considered
**Keep the shared root and scope the conformance claim** (rejected). Cheapest,
and it addresses only the harness symptom. Retention and subject permissions
stay inexpressible, which are the durable problems; the harness is the one that
merely embarrasses.

**Rename health ce-types into `openits.collector-health.*`** (rejected). It
would satisfy the profile regex, and that is precisely the objection: it claims
membership in an authority namespace whose registry has no such service, so a
consumer resolving `ce-dataschema` finds nothing. It converts a visible, honest
failure into an invisible false claim.

**Separate roots but one stream capturing both** (rejected). Fixes the harness
and the permissions story but not retention, which was the strongest driver.
Since reprovisioning is the expensive part and it is free exactly once, doing
half the change now would mean paying that cost twice.
