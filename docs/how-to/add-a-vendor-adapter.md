# Add a vendor adapter

This is the guide for a real integration: a real device, a real vendor, a
PR meant to merge. It assumes you've done
[`docs/tutorial/build-your-first-adapter.md`](../tutorial/build-your-first-adapter.md) —
that document handed you every choice already made (one vendor, one device
kind, one facet, a fake device) so you could see the whole pipeline work
once. This document is the choices it withheld.

If you haven't read
[`docs/explanation/architecture.md`](../explanation/architecture.md),
[`adapter-to-model.md`](../explanation/adapter-to-model.md),
[`pluggability.md`](../explanation/pluggability.md), and
[`testing-strategy.md`](../explanation/testing-strategy.md), this document
will send you to them repeatedly rather than re-explain what they already
cover. Have them open.

## Before you start

Read `internal/vendors/ntcip/asc.go` and `internal/vendors/ntcip/register.go`
end to end. `ntcip-asc` is the one adapter this repo ships and the model to
build from — its per-facet read methods, its `RegisterTo`/factory shape, and
its golden-test layout are what your own adapter should look like.

It is not a model for everything, and getting this wrong costs a
contributor real review time. **Two specific things in `ntcip-asc` do not
clear this repo's own bar, and copying them will not clear yours either:**

1. **Its fixtures are hand-typed, not recorded.** `internal/vendors/ntcip/asc_test.go`'s
   `healthyFixture` is a Go map literal someone wrote, not a capture from a
   real or simulated device. ADR 0008 requires the latter — see
   [the "No fixtures, no merge" row](../reference/invariants.md#no-fixtures-no-merge)
   in `invariants.md`.
2. **Its alarm-bitmap table admits, in its own comment, that it has never
   been validated against a physical controller.** `alarmBitmap` in
   `internal/vendors/ntcip/asc.go` carries the caveat verbatim — bit
   positions are a best guess carried from the gen-1 collector, not a
   confirmed mapping.

Both are tracked, known gaps, not a precedent —
[`docs/README.md`'s known-gaps list](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state)
has the full accounting. Copy the *structure*: per-facet failure isolation,
the absence-of-evidence handling, the golden-test shape. Don't copy the
fixture provenance or treat an unvalidated bitmap as a template for your
own.

## Choose the device kind, and check for an existing facet

An adapter's registry key is `<vendor>-<device_kind>` (`Descriptor.Key()`,
`sdk/adapter/adapter.go`). Before writing any code, work out which side of
that pair you're actually adding:

- **A new vendor for a device kind that already exists** (another ASC,
  another DMS controller, ...) — the facet, differ, and wire mapping are
  already built. Your adapter is the only new code.
- **A device kind nothing in `sdk/model` represents yet** — that's a
  bigger contribution; see
  ["When the device exposes something no facet models"](#when-the-device-exposes-something-no-facet-models)
  below before you start.

Eight facet kinds are modeled, diffed, and wired end to end today, and only
three of them (`signal-status`, `fault-set`, `detector-samples`) have any
adapter producing them —
[`docs/reference/starter-tasks.md`](../reference/starter-tasks.md) lists
the other five (`dms-status`, `cctv-status`, `traffic-intervals`,
`zone-incidents`, `zone-intervals`) with what each requires from a device.
Landing an adapter for one of those five touches
`internal/vendors/<vendor>/` alone — no `sdk/model` change, no
`internal/synth` change, no `internal/wire` change — which makes it the
cheapest way to get a real contribution through review. Check that table
before assuming you need a new facet; a device that looks unfamiliar at
first glance (a dynamic message sign, a camera, a roadside sensor) may
already have a facet sitting there unused.

## Pick the transport, and parse the connection block

Only one transport ships today, `sdk/transport/snmp`, with its replay fake
at `sdk/transport/snmp/snmptest`. If your device speaks SNMP, use it
directly, the way `internal/vendors/ntcip/register.go`'s `parseSNMPBlock`
does. If it doesn't, your adapter owns building and parsing its own
transport — nothing in `internal/` or `sdk/adapter` needs to change to
accommodate a new one.

The decision that matters more than which transport you pick is **where
connection details are parsed**: the core does not parse them. A device's
`connection:` block in `collector.yaml` arrives at your `Factory` as
`conn map[string]any`, completely opaque to `internal/config` — it checks
only that `vendor`/`device_kind` resolve to a registered adapter and stops
there.
[`pluggability.md`'s "The opaque `connection` block" section](../explanation/pluggability.md#the-opaque-connection-block)
covers why, with `parseSNMPBlock` as the worked example. Follow its shape:
look for your own top-level key (`snmp`, or whatever your transport calls
for), reject a missing required field at `Factory` construction time rather
than dialing a broken configuration and failing on first poll, and give
sensible defaults for anything optional the way `parseSNMPBlock` defaults
`community` to `"public"`.
[`docs/reference/configuration.md`](../reference/configuration.md)'s
`devices.connection` row is the field-reference entry this all cashes out
to.

## Implement the read: one method per facet

`Read(ctx) (*model.Snapshot, error)` — the `StateReader` method — is where
the actual device interaction happens. Follow `ntcip-asc`'s `Read` shape:
one unconditional call per facet method (`a.readSignalStatus(...)`,
`a.readFaultSet(...)`, ...), never gated on whether an earlier one
succeeded. A facet's read method decides, independently, whether it
succeeded:

- **Read succeeded, real data or genuinely none** — append the facet to
  `snap.Facets`. A device with no detectors wired up, no active faults, or
  no incidents in view is a healthy, empty facet, not an error.
- **Read failed for that facet specifically** (an unanswered OID, a
  malformed response, a sub-request that errored while the rest of the
  poll succeeded) — append a `model.FacetError{Kind, Err}` to
  `snap.Errors` and append nothing to `snap.Facets` for that kind.
- **The whole poll failed** (the device is unreachable) — return a hard
  error from `Read` itself. That's a transport failure, not a facet
  failure, and the runner turns it into a health event rather than a
  fault.

Getting the middle case right — and never quietly collapsing it into the
first — is the single most consequential decision in an adapter, and it
has its own canonical statement rather than one repeated here: see
[the "Absence of evidence is never a state change" row](../reference/invariants.md#absence-of-evidence-is-never-a-state-change)
in `invariants.md`. For the full reasoning behind the three states a facet
can be in (present-with-data, present-and-empty, absent), read
[`adapter-to-model.md`'s section by that name](../explanation/adapter-to-model.md#present-absent-or-empty-three-states-a-facet-can-be-in) —
`readFaultSet`'s zero-bits-vs-unanswered-OID contrast there is the case
every new adapter gets wrong on a first draft if it skips this reading.

Two more things worth reading directly out of `ntcip-asc` before you write
your own multi-facet `Read`:

- **Facets fail independently, in the same poll.** One OID or sub-request
  not answering must never suppress a facet that *was* readable — that's
  the whole point of calling each `read*` method unconditionally rather
  than short-circuiting on the first error.
- **Batch a table read rather than walking it one row at a time**, if your
  device exposes an indexed table the way NTCIP's detector table does.
  `readDetectors`'s doc comment in `internal/vendors/ntcip/asc.go` explains
  the ~510-round-trips-to-~32 trade it makes by building the full list of
  synthesized indexed OIDs up front and issuing one batched `Get`. The
  specific mechanism is SNMP-specific; the principle — don't pay a
  round-trip per row when the transport can batch — generally isn't.

## Set capability bits to match what you implemented

`Descriptor.Caps` is a bitset (`Capability`, `sdk/adapter/adapter.go`):
`CapState` for `StateReader`, `CapEvents` for `EventReader`, `CapCommand`
for the still-dormant `Commander` seam. Set only the bits your adapter
actually satisfies — `ascDescriptor`'s `Caps: adapter.CapState` in both
`ntcip-asc` and the tutorial's `acme-asc` is the pattern, because both
adapters implement `StateReader` and nothing else. Claiming a capability
your type doesn't implement will not compile (the registration signature
requires the interface), but claiming a capability your adapter doesn't
*mean* to support — e.g. leaving `CapCommand` set on a read-only adapter
copy-pasted from another one — is a review-time mistake, not a
compile-time one, so check it explicitly before opening the PR. See
[`pluggability.md`'s "Capability: what an adapter can do" section](../explanation/pluggability.md#capability-what-an-adapter-can-do)
for the full contract, including one gap worth knowing before it surprises
you: only `StateReader` is currently wired into the poll path
(`internal/runner.New` takes a `StateReader` specifically), so an
`EventReader` adapter compiles and registers today but has nothing calling
its `Fetch`.

## Register the adapter

Two pieces, both covered in depth by
[`pluggability.md`'s "Registering an adapter" section](../explanation/pluggability.md#registering-an-adapter-the-one-place-the-binary-decides):
a `RegisterTo(r *adapter.Registry)` function in your new
`internal/vendors/<vendor>/register.go` that calls `r.Register` with your
`Descriptor` and a factory closure, and one added line in
`RegisterAdapters` (`cmd/collector/main.go`) — the single place in the
binary that decides which vendors it ships with. `internal/app` never
registers adapters itself; it receives an already-populated
`*adapter.Registry`.

Lay the package out the way `ntcip` actually is:
`internal/vendors/<vendor>/<kind>.go` plus `<kind>_test.go` and a shared
`register.go`, sitting directly in `internal/vendors/<vendor>/` — not a
`<kind>/` subdirectory. Some older material describes a subdirectory
layout; `find internal/vendors -type f` shows it isn't what the one
adapter in the tree does, and your new package should match the tree, not
the older description.

## When the device exposes something no facet models

If your device reports something none of the eight existing facet kinds
can hold — not "a value I have to squeeze into an existing field," but a
genuinely different kind of state — that is not a reason to bend an
existing facet or reach for an ad hoc field. It's a **facet contribution**,
with its own bar and its own workflow: read the `add-domain-facet` skill
(`.claude/skills/add-domain-facet/SKILL.md`) for the shape a new facet,
differ, and event set has to take, and
[`adapter-to-model.md`'s "Facets are per-device-kind, never per-vendor" section](../explanation/adapter-to-model.md#facets-are-per-device-kind-never-per-vendor)
for why the domain model draws that line where it does.

Send it as a **separate PR from the adapter**. A facet change touches
`sdk/model` and `internal/synth` and needs review at the domain-model
level — differ correctness, the absence-of-evidence path, whether the
concept genuinely belongs to the device *kind* rather than your vendor's
product line specifically. Bundled with an adapter PR, a reviewer has to
evaluate a new domain concept and a new vendor integration at the same
time, and the two questions have almost nothing to do with each other. Land
the facet first (or in parallel, reviewed independently), then the adapter
that produces it.

## Meet the test bar

[`docs/reference/test-requirements.md`'s "A new adapter" section](../reference/test-requirements.md#a-new-adapter)
is the checklist a reviewer will hold your PR to — a golden read test per
facet, a facet-failure test proving `model.FacetError` rather than a zero
value, facets-fail-independently coverage, connection-parse rejection
tests, and correct capability bits. `internal/vendors/ntcip/asc_test.go`'s
`TestASCReadGolden`, `TestASCDetectorGoldenAndOccupancyConversion`,
`TestASCUnansweredAlarmIsFaultSetFacetError`,
`TestASCDetectorTableGetFailureIsFacetError`, and
`TestASCFacetsFailIndependently` are the named examples that checklist
points at — read them as the shape, not (per the warning above) as fixture
provenance to copy.

**On fixtures specifically, one thing to know before you start recording:**
there is no tooling in this repo today that records a fixture for you.
`sdk/transport/snmp/snmptest.Static` replays a fixed map you supply — it
does not capture one. Getting a real recording means running an SNMP walk
against your device by hand (or a simulator faithfully reproducing one) and
transcribing the raw responses into your fixture yourself. That is real,
manual work, not a formality, and it's the honest state of things: a
fixture-recording tool is planned successor work, not something you're
missing a flag for. Whatever you do to capture your fixture, say so in a
comment next to it — what you ran it against (real hardware, model number,
firmware version, or a specific simulator), and when. A fixture with no
provenance comment reads, to a reviewer, exactly like one that was
hand-typed, because
[nothing in this repo's test format can tell the difference](../explanation/testing-strategy.md#fixture-replay-reproducible-not-verified) —
provenance is established by what you say about it, not by the file
format, because there is only one file format here.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

`make check` runs `go vet`, the full test suite, and `scripts/lint-boundary.sh`
— the same gate CI runs and the one that will reject an adapter reaching for
an openits-models type directly (see
[the "Adapters and `sdk/` never import openits-models" row](../reference/invariants.md#adapters-and-sdk-never-import-openits-models)
in `invariants.md`). `go test ./... -race` is required separately because
poll loops and the publisher are genuinely concurrent —
[`testing-strategy.md`'s `-race` section](../explanation/testing-strategy.md#-race-because-polling-and-publishing-are-concurrent)
covers what it does and doesn't prove. `gofmt -l .` should print nothing;
fix anything it lists with `gofmt -w` before opening the PR.
