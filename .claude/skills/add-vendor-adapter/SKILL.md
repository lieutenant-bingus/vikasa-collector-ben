---
name: add-vendor-adapter
description: Implement a new vendor adapter (vendor × device-kind) for the collector — the reader contract, registration, connection parsing, and the fixture/golden test bar. Use this whenever adding support for a new device vendor or device kind (e.g. "add an Econolite ASC adapter", "support Daktronics DMS", "integrate this RSU"), or extending an existing adapter with new readings.
contract: v1
---

# Adding a vendor adapter

## When this applies

Adding a new vendor × device-kind adapter under `internal/vendors/`
(registry key `<vendor>-<device_kind>`), or extending an existing adapter
with a new facet read. Applies even to a vague request ("integrate this
RSU") that turns out to be adapter work.

It does not apply to:

- Adding a device concept no facet models yet — that's the
  `add-domain-facet` skill. This skill assumes the facet already exists.
- Mapping a domain event onto the wire — the `wire-emitter` skill.
- Reviewing an adapter PR someone else wrote — the
  `review-adapter-contribution` skill.

## Invariants

- [Adapters and `sdk/` never import openits-models](../../../docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models) — an adapter reaching for a wire type, even transitively through a helper package, fails `scripts/lint-boundary.sh`.
- [Absence of evidence is never a state change](../../../docs/reference/invariants.md#absence-of-evidence-is-never-a-state-change) — a failed or absent facet read must produce a `model.FacetError`, never a zero-valued facet.
- [No fixtures, no merge](../../../docs/reference/invariants.md#no-fixtures-no-merge) — every read path ships a recorded fixture with a golden test.
- [Config is the trust boundary; boot fails on the unrecognized](../../../docs/reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized) — connection-block parsing rejects malformed config in your `Factory`, not on first poll.

## Procedure

1. Read `internal/vendors/ntcip/` as the structural reference:
   `register.go` for registration and strict connection parsing, `asc.go`
   and `dms.go` for the read. Do not copy `asc.go`'s fixture provenance or
   its alarm-bitmap table; both are known gaps (see the canonical doc's
   "Before you start" section). Record your fixtures with `collector
   record-fixture` and replay them with `snmptest.LoadFixture`
   (`docs/how-to/record-fixtures-from-a-device.md`); `dms_test.go`'s
   fixtures, transcribed by hand from a live controller with a provenance
   comment, predate the recorder.
2. Confirm the facet you need already exists in `sdk/model` —
   `docs/reference/starter-tasks.md` lists the facets that are modeled and
   diffed with no adapter producing them yet. If your device needs a
   concept no facet models, stop: that's a separate `add-domain-facet`
   contribution, its own PR.
3. Implement `StateReader` (`sdk/adapter/adapter.go`) and set
   `Caps: adapter.CapState` — every adapter is pull-driven (ADR 0004: the
   runner calls `Read`, nothing an adapter does calls back into the core).
   If the device yields discrete events rather than state, stop: there is
   no event reader in the SDK, and adding one is a design change
   (ADR 0021), not part of an adapter PR.
4. Parse the `connection` block inside your `Factory`: your own top-level
   key, lowercase snake_case fields, sensible defaults over required
   knobs, and a rejected build on anything malformed rather than a dial
   that fails later. Use `adapter.OnlyKeys` on every block (an unknown or
   misspelled key fails) and `adapter.OptString` for each field (a
   wrong-typed value fails). Never discard the `ok` of a type assertion
   (`v, _ := raw["k"].(string)`): that turns a mistake into a silent default.
5. Implement the read with one unconditional call per facet method. Per
   facet: real data or genuine absence (device has no such subsystem) →
   append to `Snapshot.Facets`; that facet's read failed → append a
   `model.FacetError` to `Snapshot.Errors`, nothing to `Snapshot.Facets`;
   the device itself is unreachable → hard error from `Read`, never a
   synthesized fault event. Out-of-spec values from the device (bad
   bitmaps, values outside the documented range): clamp or skip, with a
   test proving it — the runner recovers a panicking adapter
   (`internal/runner`'s `readGuarded`), but that's a last-resort net, not
   a substitute for handling a malformed reading yourself.
6. Set `Descriptor.Caps` to exactly the interfaces your type implements —
   nothing claimed that isn't real, even if it would compile.
7. Register. A new vendor package gets a `RegisterTo(r *adapter.Registry)`
   and one added line in `RegisterAdapters` (`cmd/collector/adapters.go`).
   A new device kind in an existing vendor package adds one `r.Register`
   call to that package's existing `RegisterTo` instead. `internal/app`
   takes no part in this.
8. Write fixtures and golden tests to the bar in
   `docs/reference/test-requirements.md`'s "A new adapter" section, with a
   provenance comment on every fixture — what you ran it against, and
   when.

## Verify

```bash
go test -race ./internal/vendors/<vendor>/...
make check
```

Expected: both pass. `make check` already runs the whole suite under
`-race`, gofmt (through golangci-lint) and the boundary lint; the first
command is the fast loop while you work.

## Canonical doc

[`docs/how-to/add-a-vendor-adapter.md`](../../../docs/how-to/add-a-vendor-adapter.md) — the full narrative.
