---
name: add-vendor-adapter
description: Implement a new vendor adapter (vendor × device-kind) for the collector — the reader contract, registration, connection parsing, and the fixture/golden test bar. Use this whenever adding support for a new device vendor or device kind (e.g. "add an Econolite ASC adapter", "support Daktronics DMS", "integrate this RSU"), extending an existing adapter with new readings, or reviewing an adapter PR.
---

# Adding a vendor adapter

Adapters are the collector's contribution surface. Each one owns a
vendor × device-kind pair (registry key `<vendor>-<device_kind>`), owns its
transport entirely, and returns only `sdk/model` types. Read
`docs/adr/0002-domain-model-and-wire-emitter-boundary.md` and
`docs/adr/0008-fixture-golden-testing-bar.md` before starting; the rules
below exist because 10+ contributed adapters are planned and every
undisciplined adapter multiplies maintenance across all of them.

The reference implementation is `internal/vendors/ntcip/` (the generic,
standards-only NTCIP ASC adapter, ADR 0003). Mirror its structure; deviate
only where the vendor genuinely differs.

## Hard rules (CI-enforced or review-blocking)

- **Only `sdk/model` types cross the boundary.** Adapters must not import
  openits-models or anything under `internal/wire` — `scripts/lint-boundary.sh`
  fails CI if they do. If the domain model lacks a concept you need, that's
  a `sdk/model` facet proposal (see the `add-domain-facet` skill), not a
  reason to smuggle wire types in.
- **No fixtures, no merge** (ADR 0008). Every read path ships recorded
  fixtures with golden tests. Record from a real device where possible, and
  scrub deployment-identifying data (addresses, community strings, site
  names) from recordings before committing.
- **Pull only** (ADR 0004). `StateReader.Read` and `EventReader.Fetch` are
  polled by the core; adapters never push, never own goroutines that
  outlive a call, and never buffer between polls. `Commander` is a dormant
  seam — implement it only if explicitly asked.

## Workflow

1. **Pick the surface.** `StateReader` (device state polled into a
   `model.Snapshot` the core diffs — most devices) or `EventReader`
   (sources that already yield discrete events, e.g. hi-res logs). See
   `sdk/adapter/adapter.go` for the exact contracts.

2. **Create `internal/vendors/<vendor>/`** with a `RegisterTo(r *adapter.Registry)`
   that registers a `Descriptor{Vendor, DeviceKind, Caps}` and a factory.
   The factory receives the device's `connection` config block as
   `map[string]any` — it is opaque to the core; parse and validate it here,
   returning errors prefixed `"<vendor>-<kind> <deviceID>: ..."` so boot
   failures identify the device. Wire the registration into
   `RegisterAdapters` in `cmd/collector/main.go`, alongside the existing
   `ntcip.RegisterTo(r)` — that function is the one place the binary decides
   which vendors it ships with. Nothing in `internal/app` registers adapters;
   it receives an already-populated `*adapter.Registry`.

3. **Implement the read.** Populate one facet per subsystem read
   (`sdk/model` facets: operational status, fault set, detector samples,
   DMS status, ...). Failure semantics matter more than the happy path:
   - A subsystem the device doesn't have → **empty facet, not an error**
     (a controller with no detectors is a normal deployment).
   - A subsystem the adapter tried and failed to read → record a
     `model.FacetError` in `Snapshot.Errors` and keep the other facets.
     Facets fail independently; one bad table must not poison the poll.
   - Transport failure (device unreachable) → return a hard error from
     `Read`; the core's health tracking owns unreachability. Never
     synthesize a fault event for it (see ADR 0013 — the
     differ's rule is "absence of evidence is never a state change").
   - Out-of-spec values from the device → clamp or skip *with a test
     proving it*; never let a misbehaving device panic the collector.

4. **Tests, following `internal/vendors/ntcip/asc_test.go`:**
   - Golden reads over recorded fixtures (for SNMP, OID→value maps served
     via `sdk/transport/snmp/snmptest`).
   - One test per failure mode above (partial failure → FacetError,
     transport error → hard error, missing subsystem → empty facet).
   - Decode edge cases: bitmaps, clamping, sparse tables, fallbacks for
     unanswered OIDs.

5. **Gate:** `make check` (vet + tests + boundary lint) and
   `go test ./... -race` — exactly what CI runs. Document the vendor's
   `connection` block shape in a comment on `RegisterTo` (the
   `collector.yaml` example shows the ntcip shape).

## Conventions

- Config keys are lowercase snake_case inside the vendor's `connection`
  block; sensible defaults over required knobs (ntcip defaults
  `community: public`, 2s timeout).
- Commit style: Conventional Commits (`feat(vendor): add econolite-asc
  adapter`), no co-author attribution.
