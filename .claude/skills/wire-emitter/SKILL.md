---
name: wire-emitter
description: Work on the wire-emitter layer — mapping domain events to openits-models payloads, adopting a new openits-models release (pin bump), or adding ce-type mappings. Use this whenever a task touches internal/wire, mentions openits-models versions/protos/ce-types, CloudEvents encoding, or "publish X to the bus" — and when reviewing changes under internal/wire.
contract: v1
---

# Wire emitters and openits-models pins

## When this applies

Mapping a domain event onto an openits-models `(ce-type, payload)`, adding
a ce-type mapping, or moving the openits-models pin forward. Also applies
when reviewing a change under `internal/wire`.

This skill covers `internal/wire/openits` — the openits-models emitter
family. It does not cover `internal/wire/health`, the collector-owned
health schema (ADR 0007, JSON bodies, `openits-collector.health.*`
ce-types) — that family does not track openits-models releases and needs
none of this during a pin bump.

## Invariants

- [Adapters and `sdk/` never import openits-models](../../../docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models) — inverted here: `internal/wire` is the one place allowed to; that's this skill's whole reason to exist.
- [The openits-models pin carries no `replace` directive](../../../docs/reference/invariants.md#the-openits-models-pin-carries-no-replace-directive)
- [The openits-models pin is main HEAD, not a stale tag](../../../docs/reference/invariants.md#the-openits-models-pin-is-main-head-not-a-stale-tag)
- [Every mapped ce-type has a byte-exact golden](../../../docs/reference/invariants.md#every-mapped-ce-type-has-a-byte-exact-golden)
- [Subjects are operator-configurable; the CloudEvents envelope stays canonical](../../../docs/reference/invariants.md#subjects-are-operator-configurable-the-cloudevents-envelope-stays-canonical) — `ce-type`, `ce-source`, and `ce-id` never derive from the operator's subject template, or vice versa.

## Procedure

### Adding a ce-type mapping

1. **Probe the pinned module before mapping anything.** A shape that looks
   right in the generated code is not a result — only bytes you produced
   are. The scratch-module recipe and the list of things that have caught
   people out before (enums with no `UNSPECIFIED` zero value, `identityref`
   and `decimal64` leaves that are plain strings, the `ce-id-spec.md` test
   vector) are
   [`map-an-event-to-the-wire.md`'s "Ground truth: probe, don't read"](../../../docs/how-to/map-an-event-to-the-wire.md#ground-truth-probe-dont-read).
   Do not work from this list; work from that section.
2. Add the routing entry to `ceTypeFor` (`internal/wire/openits/emitter.go`),
   keyed on `key{event.EventKind(), deviceKind}` — the catalog's ce-type
   string verbatim, never templated.
3. Add the `Encode` case: field-by-field copies, explicit enum switches.
   No reflection, no mapping DSL. Where the domain value has no honest
   wire identity, return `ok=false` and let the case decline rather than
   encode an approximation.
4. Add a `dataSchemaFor` entry pointing at the defining module's own
   schema-registry revision — never a base or types module the payload
   happens to compose.
5. Add a golden case to `goldenCases`
   (`internal/wire/openits/golden_test.go`): the fixture event, expected
   ce-type, expected `ce-dataschema`, and exact hex bytes for both `Data`
   and `Identity`.

### Adopting a new openits-models release

1. `grep openits-models go.mod` for the current pin — a main-HEAD
   pseudo-version, never a tag, never a `replace`.
2. Probe the new module (step 1 above) and diff its release's
   `bindings/nats/asyncapi.yaml` ce-type set against the emitter's current
   `CETypes()`.
3. `go get -u github.com/Vikasa2M/openits-models@main`. While only one
   release compiles at a time, edit `internal/wire/openits` in place; copy
   to `internal/wire/<version>` only once the fleet genuinely needs two
   releases compiled together.
4. Add or adjust mappings for anything the probe turned up, and claim any
   newly-available ce-types that were previously dropping (the drop
   warnings name the candidates) — follow "Adding a ce-type mapping"
   above.
5. Re-run the goldens; treat every byte diff as a decision to confirm
   against the actual models change, never a regenerate-and-move-on.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

Expected: `make check` and `go test ./... -race` both pass; `gofmt -l .`
prints nothing. For a pin bump specifically, `make check`'s boundary lint
is what catches a `replace` directive left behind from local debugging.

```bash
go test ./internal/wire/openits/... -run TestGoldens -v
```

Expected: every golden case passes. A golden that moved with no matching
mapping edit means the module changed under you, not that the golden
needs regenerating.

## Canonical doc

[`docs/how-to/map-an-event-to-the-wire.md`](../../../docs/how-to/map-an-event-to-the-wire.md) — mapping a single event.
[`docs/how-to/adopt-an-openits-models-release.md`](../../../docs/how-to/adopt-an-openits-models-release.md) — moving the pin forward.
