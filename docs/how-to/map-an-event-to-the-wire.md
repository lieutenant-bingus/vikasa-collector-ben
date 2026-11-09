# Map an event to the wire

**At the end** a domain event that has no wire mapping yet — one you just
added with [`add-a-domain-facet.md`](add-a-domain-facet.md), or an
existing one — publishes as a catalog ce-type with an explicit payload, a
`ce-dataschema`, and a byte-exact golden test.

**You need** [`wire-boundary.md`](../explanation/wire-boundary.md) read: it
covers why the domain and wire models are kept apart, what rides in the
CloudEvents envelope, and the drop rule. The catalog must already declare
the ce-type at the pinned openits-models release; if it doesn't, the
collector does not invent one. Moving the pin to a newer release is a
separate guide,
[`adopt-an-openits-models-release.md`](adopt-an-openits-models-release.md).

## The mapping path, end to end

`internal/wire/openits`'s `emitter` (`internal/wire/openits/emitter.go`)
is the only type implementing `wire.Emitter`
(`internal/wire/emitter.go`) for the openits-models catalog. One call to
`Encode(ev model.Event)` does four things in sequence:

1. **Looks up the ce-type.** `ceTypeFor` is a `map[key]string` keyed on
   `key{event, deviceKind}` — the tuple, not the event kind alone, because
   the catalog defines some event shapes once and reuses them across
   services (`fault-raised` routes to `openits.signal-control.fault-raised.v1`
   for an `asc` and `openits.dms.fault-raised.v1` for a `dms`, off the same
   `model.FaultRaised` type). No entry means `Encode` returns
   `ok=false` immediately — not claimed.
2. **Builds the payload**, in a type switch in the event's service-family
   encoder: `encodeSignalControl`, `encodeDMS`, `encodeShared` (the
   reachability and fault events every kind shares), and so on, one per
   `internal/wire/openits/encode_<family>.go`. Each case does field-by-field copies and explicit enum-to-identity lookups
   (`controllerModeIdentity`, `dmsControlModeIdentity`,
   `faultKindIdentity`, and friends in `internal/wire/openits/identities.go`)
   — no reflection, no generic mapping helper. A mapping function that
   returns `ok=false` for a value it can't represent makes `Encode` decline
   the whole event rather than encode a partial or approximate payload.
3. **Attaches `ce-dataschema`** from `dataSchemaFor`
   (`internal/wire/openits/identities.go`), a hard-coded
   `map[string]string` keyed on ce-type (why it's hard-coded rather than
   read from the catalog is covered where step 4 below links it).
4. **Returns `wire.Encoded`** (`internal/wire/emitter.go`): `Data` (what
   ships) and `Identity` (`Data` with producer-assigned leaves like
   `sequence` cleared — what `internal/cloudevents`' `ce-id` derivation
   hashes). Only the emitter can compute `Identity`, since only
   `internal/wire` knows the payload's shape.

`CETypes()` derives its list from `ceTypeFor` itself rather than
maintaining a second list, so the two structurally cannot drift — boot
validation renders every entry through the operator's subject template,
so an emitter that under-reports here defeats that check silently.

## Ground truth: probe, don't read

openits-models' prose and its generated code have disagreed more than
once, and the prose is the one that lies. Before mapping anything against
it, resolve the module at the pinned version in a scratch Go module and
**run** it — construct the message, marshal it, print the bytes. Shapes
looking right in the generated `.pb.go` is not a result; a probe that
never produced a byte proves nothing.

```bash
mkdir /tmp/probe && cd /tmp/probe && go mod init probe
go get github.com/Vikasa2M/openits-models@<the version in go.mod>
# then a main.go that builds the message, marshals it, and prints hex
```

Use the version `go.mod` pins (`grep openits-models go.mod`); it is
always a release tag ([ADR 0018](../adr/0018-tagged-model-pins.md)).

Things that have surprised people before, worth checking rather than
assuming:

- **Enums may have no `UNSPECIFIED` zero value.** Several start their
  first real member at 0, so a zero field is a meaningful value on the
  wire, not "unset" — you cannot distinguish the two.
- **`identityref` leaves are plain `string`** on the generated structs,
  carrying `defining-module:identity-name` (see the `scTypes`, `dmsTypes`,
  and sibling constants at the top of
  `internal/wire/openits/identities.go`). They are not Go enums, and the
  identity set is not always what the domain enum's name suggests —
  confirm which module actually defines the identity you mean, since
  several services define same-named ones.
- **Some numeric-looking leaves are strings.** YANG `decimal64` renders as
  a Go `string` in the generated types, so a field the domain holds as an
  integer or float may need formatting rather than a numeric conversion.
- **`ce-id-spec.md` ships a test vector.** Reproduce it from the real
  generated type before trusting any `ce-id` implementation — it exercises
  the payload encoding and the digest chain together. If it doesn't
  reproduce, the implementation is wrong, not the vector. `ce-id` itself is
  openits-models' contract, not this repo's, in full detail in
  [`wire-boundary.md`'s `ce-id` section](../explanation/wire-boundary.md#ce-id-a-deterministic-ulid-not-a-content-hash).

Record what you verify, and against which version, so the next person
re-probes only what a later pin bump could actually have moved.

## Adding a ce-type mapping

1. **Confirm the domain event exists** (from
   [`add-a-domain-facet.md`](add-a-domain-facet.md) or already in
   `sdk/model/events.go`) and probe the catalog's identity for the value(s)
   it carries, per the ritual above.
2. **Add the routing entry** to `ceTypeFor` in
   `internal/wire/openits/emitter.go`, keyed on
   `key{event.EventKind(), deviceKind}`. Use the catalog's ce-type string
   verbatim — never templated, never derived from anything
   operator-configured.
3. **Add a case to the family encoder's type switch**
   (`internal/wire/openits/encode_<family>.go`; a new service family gets
   its own file and a line in the family list in `payload`, in
   `emitter.go`). Build the proto message with
   explicit field copies. Where the domain value has no honest identity on
   the wire, return `ok=false` from the identity-lookup function and let
   the case (and therefore `Encode`) decline — see the next section.
4. **Add a `dataSchemaFor` entry** for the new ce-type, pointing at the
   defining module's own schema-registry revision — never a base or
   types module the payload happens to compose, per
   [`wire-boundary.md`'s `ce-dataschema` section](../explanation/wire-boundary.md#ce-dataschema-pins-the-defining-module).
5. **Add a golden case.** Append to `goldenCases` in
   `internal/wire/openits/golden_test.go`: a fixture event, the expected
   ce-type, the expected `ce-dataschema`, and the exact hex bytes for both
   `Data` and `Identity`, encoded at the fixed `goldenAt` timestamp so the
   golden never depends on wall-clock time.
   [`docs/reference/test-requirements.md`'s "A new ce-type mapping" section](../reference/test-requirements.md#a-new-ce-type-mapping)
   is the checklist a reviewer holds this to —
   `TestGoldens` (`internal/wire/openits/golden_test.go`) is what actually
   compares your bytes, and `TestGoldensCoverEveryCEType` (same file) fails
   the build if a ce-type reachable through `CETypes()` has no golden case
   at all.
6. **Generate the hex with `TestPrintGoldens`** (same file) rather than by
   hand. It skips unless asked:
   `PRINT_GOLDENS=1 go test ./internal/wire/openits -run TestPrintGoldens -v`.
   Read the decoded message it prints against your fixture
   before pasting the hex: a golden copied without checking only records
   whatever the code happened to do.
7. **Delete the ce-type's line from the `unmapped` allowlist** in
   `internal/wire/openits/catalog_test.go` — see
   [the catalog-conformance check](#the-catalog-conformance-check) below.
8. **If the ce-type is in a service the Tier 2 harness judges**
   (`signal-control`, `dms`, `cctv`, `traffic-sensor`, `perception` and
   `zone-occupancy` today), add a step that produces it to that kind's
   scripted device in `internal/wire/tier2/tier2_test.go` and the ce-type
   to `want` (plus its decoder in `payloadFor`), so openits-models' own
   conformance checks run against it. If a harness check was ruled out in
   `unpublished` because this ce-type was not mapped,
   `TestEveryHarnessCheckIsRunOrRuledOut` now fails until you move it into
   `checks`.

Every mapping — new or existing — is a field-by-field, explicit,
reviewable diff by design; there is no mapping DSL to configure instead of
following these steps.

## Why the emitter declines rather than approximates

`Encode` returning `ok=false` is not a fallback path bolted onto the type
switches — it's the return value most mapping functions
(`controllerModeIdentity`, `dmsControlModeIdentity`, `cctvControlModeIdentity`,
`tourRunStateFor`) are built around, each with an `(identity, ok bool)`
signature that forces the caller to check. `TestEncode_UnrepresentableTargetIsNotClaimed`
(`internal/wire/openits/emitter_test.go`) proves the shape: a domain value
with no honest wire identity produces a declined event, not a payload
carrying the nearest-looking enum value. The full
reasoning — including the cases where a *vaguer* true statement exists and
the emitter uses that instead of declining (`faultKindIdentity`'s
per-service fallback) — is
[`wire-boundary.md`'s drop-rule section](../explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate);
this document only points a new mapping decision at it rather than
re-deriving the rule.

## The catalog-conformance check

`internal/wire/openits/catalog_test.go` compares the emitter against the
pinned openits-models catalog in both directions. When you map a ce-type,
delete its line from the `unmapped` allowlist in that file —
`TestUnmappedAllowlistIsCurrent` fails until you do, because an allowlisted
type that is now mapped is a stale claim. A ce-type you emit that the pinned
release does not declare fails `TestEmittedCETypesAreInPinnedCatalog`; the
fix is never to invent one (see
[the "not reshaped" rule](../reference/invariants.md#openits-models-is-not-reshaped-to-suit-the-collector)).

## Verify

```bash
make check
```

This is the CI gate: `go vet`, golangci-lint (gofmt included), the whole
suite under `-race`, and the boundary lint that fails the build if anything
outside `internal/wire` imports openits-models. The suite includes the
goldens, the catalog-conformance tests, and the Tier 2 harness run.
`-race` matters because emitters are called concurrently, one goroutine
per polled device; `TestEncode_SequenceIsSafeUnderConcurrentDevices`
(`internal/wire/openits/emitter_test.go`) exercises exactly that.
