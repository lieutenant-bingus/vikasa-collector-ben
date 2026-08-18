# Map an event to the wire

This is the guide for taking a domain event that already exists —
whether it's one you just added following
[`add-a-domain-facet.md`](add-a-domain-facet.md) or an existing one with
no wire mapping yet — and giving it a `(ce-type, payload)` in
`internal/wire/openits`. It does not cover adopting a new openits-models
release (moving the pin, re-mapping everything the release touched); that
is a separate, larger workflow with its own document.

Read [`docs/explanation/wire-boundary.md`](../explanation/wire-boundary.md)
first if you haven't. It covers why the domain model and the wire model
are kept apart, what rides in the CloudEvents envelope, and the drop rule
in full — this document assumes that reading and stays procedural.

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
2. **Builds the payload**, in a type switch over `ev` in `Encode`. Each
   case does field-by-field copies and explicit enum-to-identity lookups
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

The version currently pinned is whatever `go.mod` says today — check
there rather than assuming; it moves under
[ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md)'s lockstep
mechanism.

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
3. **Add a case to `Encode`'s type switch.** Build the proto message with
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
6. **Run `TestPrintGoldens`** (`internal/wire/openits/golden_test.go`) if
   you need the hex to paste in rather than compute by hand — it's there
   for exactly this step.

Every mapping — new or existing — is a field-by-field, explicit,
reviewable diff by design; there is no mapping DSL to configure instead of
following these steps.

## Why the emitter declines rather than approximates

`Encode` returning `ok=false` is not a fallback path bolted onto the type
switch — it's the return value most mapping functions
(`controllerModeIdentity`, `dmsControlModeIdentity`, `cctvControlModeIdentity`,
`tourRunStateFor`) are built around, each with an `(identity, ok bool)`
signature that forces the caller to check. `TestEncode_ModeChangedToStandby_IsNotClaimed`
and `TestEncode_DMSControlUnknown_IsNotClaimed`
(`internal/wire/openits/emitter_test.go`) are the golden proof of the
shape: a domain value with no honest wire identity produces a declined
event, not a payload carrying the nearest-looking enum value. The full
reasoning — including the cases where a *vaguer* true statement exists and
the emitter uses that instead of declining (`faultKindIdentity`'s
per-service fallback) — is
[`wire-boundary.md`'s drop-rule section](../explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate);
this document only points a new mapping decision at it rather than
re-deriving the rule.

## A known gap: no catalog-conformance check

Nothing in step 5 above checks your new mapping against the pinned
openits-models catalog itself — `TestGoldensCoverEveryCEType` only checks
that the emitter's own goldens keep up with its own routing table, which
is a narrower claim than it looks. `internal/wire/health` has a real
conformance test against an external document; `internal/wire/openits`
does not, and this is a tracked gap rather than an oversight this document
is the first to notice — see
[`docs/README.md`'s known-gaps entry for it](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state)
for the full account.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

`make check` runs `go vet`, the full suite, and
`scripts/lint-boundary.sh` — the same gate that fails the build if
anything outside `internal/wire` reaches for an openits-models type.
`go test ./... -race` matters here because emitters are called
concurrently, one goroutine per polled device — `TestEncode_SequenceIsSafeUnderConcurrentDevices`
(`internal/wire/openits/emitter_test.go`) exists specifically to exercise
that. `gofmt -l .` should print nothing.
