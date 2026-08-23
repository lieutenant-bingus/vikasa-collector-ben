# The wire boundary: why the domain model isn't the wire model

[`architecture.md`](architecture.md) names the wire emitter as stage 3 of
the pipeline and says what it does: look up a domain event, return a
ce-type and a payload, or decline. This document stays at that boundary and
goes deep on two things architecture.md only names in passing — why the
boundary is drawn exactly where it is, between `sdk/model`
([`adapter-to-model.md`](adapter-to-model.md) covers that side in full) and
`internal/wire` — and what actually rides in the envelope once an emitter
has produced a payload. If you are about to write or review a mapping in
`internal/wire/openits/`, or you are trying to understand why a `ce-id`
looks the way it does, this is the page.

## Why the domain model is not the wire model

The collector's domain model (`sdk/model`) is a vocabulary the project
owns outright. openits-models is a vocabulary a different project owns,
released as its own versioned catalog. [ADR 0002](../adr/0002-domain-model-and-wire-emitter-boundary.md)
keeps those two vocabularies from merging into one, for a reason that only
shows up once you imagine the alternative: if every vendor adapter
constructed openits-models types directly, then anything that changes
about that catalog — a renamed field, a renumbered proto tag, an entirely
different catalog someday — becomes a change every adapter author has to
make, on every deployment, at the same time. With contributed adapters
from parties who do not control openits-models' release cadence, that is
not a maintenance cost the project can absorb.

So the domain model sits upstream of the catalog, not downstream of it.
Adapters return `sdk/model` facets and events; **exactly one layer,
`internal/wire`, ever imports openits-models and turns a domain event into
a `(ce-type, payload)` pair.** That import boundary is a load-bearing rule
with its own canonical statement and its own enforcement, not restated
here; see the
["Adapters and `sdk/` never import openits-models" row](../reference/invariants.md#adapters-and-sdk-never-import-openits-models)
in `invariants.md` for what actually fails the build if it's crossed.

What that buys, concretely, is that a change to the catalog is a one-package
edit instead of an every-adapter edit — and the domain model is allowed to
be richer than any one wire version. Where the two disagree, the mapping
layer makes an explicit, reviewable map-or-drop decision (more on that
below) instead of a collection blocker upstream.

**What "versioned emitter" means.** `internal/wire/openits` is the one
package implementing `wire.Emitter` today, and it is deliberately
unsuffixed rather than named for a release. Exactly one models version is
ever compiled into the collector at a time — the pin names a single semver
release tag ([ADR 0018](../adr/0018-tagged-model-pins.md)), never a
`replace` directive, and every deployment runs one catalog version
([ADR 0005](../adr/0005-one-catalog-version-per-instance.md)) — so one
unsuffixed package is correct.

The one-package-per-release split (`internal/wire/openits_v1`,
`internal/wire/openits_v2`, and so on, with config picking one at boot) is
what would let a fleet run mixed model versions across deployments without a
single binary compiling in only one. It has not started, and **it is not
gated on the pin being tagged.** [ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md)
originally coupled the two; ADR 0018 decoupled them, because they answer
different questions — how the collector *adopts* a release, versus how many
releases it *compiles at once*. The split begins when something genuinely
needs two releases in one binary. Until then a models change is exactly the
one-package edit ADR 0002 exists to guarantee.

## What the envelope carries

The publisher ships every event as a CloudEvent in NATS binary mode:
`ce-*` attributes ride as message headers, the body is the raw encoded
payload. Four of those attributes are worth understanding individually,
because each is fixed by a different rule and for a different reason.

### `ce-type`: catalog-verbatim, fixed

`ce-type` is schema identity. The emitter looks it up from its own routing
table — keyed on the domain event's kind and device kind, e.g.
`{"mode-changed", "asc"}` → `openits.signal-control.mode-changed.v1` — and
what comes out is the catalog's own string, unmodified. It is not built,
templated, or derived from anything operator-configured; it is the same
string whatever subject the operator has routed the event to on this
particular deployment.

### `ce-source`: the profile URN, and why it feeds `ce-id`

`ce-source` is the profile URN [ADR 0015](../adr/0015-ce-source-urn-scheme.md)
defines:

```
urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>
```

built by `SourceFor` (`internal/cloudevents/subject.go`). `entity-kind` is
not the collector's own `deviceKind` string — it's a separate mapping
(`entityKindFor`) onto the profile's vocabulary: `asc` → `controller`,
`dms` → `sign`, `cctv` → `cctv`, `traffic-sensor` → `traffic-sensor`,
`perception` → `perception`, and a device-less event (the boot event is
the only one today) → `collector`, using the tenant's `Site` as the id
segment since the collector is then the subject of its own event. A device
kind with no entry in that table — `rsu`, for instance, which the
collector does not yet model — makes `SourceFor` return `""`, and the
caller (`internal/app/app.go`) drops the event with a warning rather than
inventing a URN segment for an entity type upstream has never defined.

The reason `ce-source`'s exact shape matters beyond routing: **its literal
bytes are the first field hashed into `ce-id`.** Change the token order, the
entity-kind vocabulary, or the device-less fallback, and every event id a
fleet has ever produced changes with it. That's not a hypothetical
tightening — it's why ADR 0015 exists: the format the code has always
produced had drifted out of every ADR that claimed to describe it, and
`ce-id` hashing it verbatim is what turns that kind of drift from cosmetic
into a silent id break.

### `ce-id`: a deterministic ULID, not a content hash

This is the one attribute worth reading the source for rather than taking
on faith, because "deterministic" is easy to round off to "a hash of the
payload" and that is specifically wrong. `EventID`
(`internal/cloudevents/eventid.go`) computes:

```
digest = SHA-256( source ‖ ceType ‖ stableMillis(stableTime) ‖ identity )
id     = ULID(timestamp = stableTime-ms, randomness = digest[0:10])
```

Field by field, in the order the code actually hashes them:

1. **`source`** — the literal `ce-source` URN bytes described above.
2. **`ceType`** — the literal `ce-type` string bytes.
3. **`stableMillis(stableTime)`** — `stableTime` formatted as RFC 3339 with
   fixed millisecond precision, UTC, trailing `Z`
   (`2006-01-02T15:04:05.000Z`). Fixed precision is deliberate: the same
   instant rendered with a different number of fractional digits would hash
   differently. `stableTime` is the event's `occurred-at` — the device's
   own clock, or, for events the collector infers by diffing polls (which
   is most of what this collector emits), the observing poll's
   `Snapshot.SampledAt`. In today's code `ce-time` is set from that same
   value (`envelope.New` derives both `Time` and the digest's `stableTime`
   from one `ev.OccurredAt.UTC()`), so the two currently coincide — but
   conceptually the digest is anchored to occurred-at, not to whatever
   `ce-time` happens to hold.
4. **`identity`** — the encoded payload with producer-assigned leaves
   (`sequence`, `observed-by`) already cleared, so the id describes the
   occurrence rather than the act of observing it. Clearing those leaves is
   the emitter's job — only `internal/wire` knows the proto shape (that's
   the same boundary as the section above) — so `EventID` just takes bytes.

Between each pair of adjacent fields the digest writes a single `0x1f` unit
separator — three separators total, none leading, none trailing. Without
it, `("ab", "c")` and `("a", "bc")` would hash identically and adjacent
fields could shift without changing the id, which is exactly what a fixed
separator forecloses.

The ULID's leading 48 bits are `stableTime` in milliseconds — not a clock
read at encode time, an argument the caller supplies — and the trailing 80
bits are the digest's first 10 bytes. The full 128 bits are padded with 2
leading zero bits to 130 and sliced into 26 five-bit groups, each rendered
through the Crockford base32 alphabet (no `I`, `L`, `O`, or `U`, so the id
survives being read aloud or transcribed off a screen); the first group
therefore carries only the top 3 bits of the timestamp's first byte. A
millisecond timestamp in the ULID does not make the id non-deterministic —
it's an input, not a clock read — so two collectors observing the same
occurrence, or one collector before and after a restart, produce the same
id without coordinating, and JetStream dedup survives a restart.

This isn't a self-consistency claim. openits-models' own `ce-id-spec.md`
names the collector as its reference implementation, publishes a test
vector, and `EventID` reproduces it byte for byte — this repo has to be
right against that vector, not merely internally consistent.

`asyncapi.yaml`'s `ce-id` description carries this same formula verbatim
(`SHA-256(source ‖ ce-type ‖ stable-time ‖ payload-bytes)`), and
`internal/docs/asyncapi_test.go` asserts that literal string, field order
and all, specifically to catch a regression back to a bare content hash or
a reordered digest — an earlier version of this derivation hashed
`(type, source, …)` instead, which is a different id for every event ever
produced under the old order.

### `ce-dataschema`: pins the defining module

Set only on `openits.*` events, from a hard-coded constant table
(`dataSchemaFor` in `internal/wire/openits/identities.go`) keyed on
whichever module *defines* that event and that module's own revision —
never on a base or types module the payload happens to compose. Health
events omit the attribute entirely rather than point at a registry they
were never part of ([ADR 0007](../adr/0007-collector-owned-health-schema.md)).
The table is hard-coded because openits-models ships no Go catalog API to
read it from at runtime; the trade is that a pin bump shows up as a
reviewable diff of these constants, and the golden tests lock them, rather
than a behavior change nobody can see in a diff.

## The drop rule: decline rather than approximate

The emitter's mapping functions come in two shapes, and the difference
between them is the whole rule. Most return `(value, ok bool)`: when there
is no honest wire rendering of a domain value, `ok` is `false` and the
event is not claimed, rather than being encoded against the nearest
available identity. `controllerModeIdentity` is the clearest example —
`ModeStandby` and `ModeUnknown` have no controller-mode identity upstream
at all (the set is coordinated/free/flash/preempt/priority/manual/off), so
a `ModeChanged` event carrying either value is declined outright rather
than forced onto a mode the controller never reported. `dmsControlModeIdentity`,
`cctvControlModeIdentity`, and `tourRunStateFor` decline the same way, each
for the same reason: the wire's enum has no honest slot for what the
domain actually observed, and asserting one anyway would be a wrong value
on the bus — worse than no value at all, because a consumer has no way to
tell an invented state from a real one.

The rule is "decline when there is no honest positive statement to make,"
not "decline whenever the domain is more specific than the wire." Where a
vaguer-but-still-true statement exists, the emitter uses it instead of
dropping: `faultKindIdentity` falls back to a service's base fault identity
(`sc-fault-event-kind` and so on) for a fault category the wire has no
specific leaf for, because "a fault occurred, class unmapped" is still true
and still worth an operator seeing — unlike a mode, which has no such
honest fallback because `current` must assert one specific state. The two
behaviors read the same in the source: an `ok bool` return the caller must
check, and a comment explaining, per mapping, why this one drops and that
one degrades instead of guessing.

A device kind with no `ce-source` entity-kind mapping is a second, later
drop path on the same principle: the payload might encode cleanly, but
`SourceFor` still returns `""` and the event is dropped rather than
published under an invented URN segment (see above). And if a domain event
kind has no case in the emitter's routing switch at all, the shared
default (`return nil, false, nil`) declines it the same way.

Both drop paths converge on the same place: `internal/app/app.go`'s
`encodeAndPublish` logs a `slog.Warn` — `"event dropped: no ce-source
entity kind for device kind"` or `"event dropped: no emitter for domain
event"` — and returns without publishing. **That is the part that
surprises people: these drops are logged, but they are not counted.**
There is no metrics subsystem anywhere in this repository today — nothing
under `internal/` or `sdk/` imports Prometheus, `expvar`, or OpenTelemetry
— so a fleet operator can grep logs for a drop but cannot alert on a drop
rate or see one on a dashboard. This is a known, tracked gap, not a small
patch waiting to be written; see
["Unclaimed wire-emitter events drop without a metric"](../README.md#known-gaps-and-successor-work)
in the known-gaps list for what closing it actually involves.
