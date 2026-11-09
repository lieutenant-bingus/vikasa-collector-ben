# Add a vendor adapter

**At the end** you will have a pull request that adds one vendor × device
kind (say, `econolite-asc`) to the collector: an adapter that polls the
device and returns `sdk/model` facets, registered in the binary, with
fixture-backed tests that clear review.

**You need:**

- **The device**, or a simulator that faithfully reproduces it. An adapter
  encodes what a specific controller actually returns — OID quirks, vendor
  deviations, firmware differences — and the fixture bar cannot be met
  without one. Treat it as a prerequisite, not a hurdle at the end.
- **The tutorial done once.**
  [`build-your-first-adapter.md`](../tutorial/build-your-first-adapter.md)
  makes every choice for you so you can see the pipeline work end to end.
  This guide covers the choices it skipped.
- The background it links to rather than repeats:
  [`architecture.md`](../explanation/architecture.md),
  [`adapter-to-model.md`](../explanation/adapter-to-model.md),
  [`pluggability.md`](../explanation/pluggability.md) and
  [`testing-strategy.md`](../explanation/testing-strategy.md).

The steps, each a section below:

1. [Scope the PR and read the reference adapters](#before-you-start).
2. [Choose the device kind and check for an existing facet](#choose-the-device-kind-and-check-for-an-existing-facet).
3. [Parse the connection block strictly](#pick-the-transport-and-parse-the-connection-block).
4. [Implement `Read`, one method per facet](#implement-the-read-one-method-per-facet).
5. [Set the capability bits](#set-capability-bits-to-match-what-you-implemented).
6. [Register the adapter](#register-the-adapter).
7. [Meet the test bar](#meet-the-test-bar).
8. [Verify, then read the review checklist](#verify).

## Before you start

**What your PR should touch, and nothing else:**

- `internal/vendors/<vendor>/<kind>.go` and `<kind>_test.go` — new files
- `internal/vendors/<vendor>/register.go` — new for a new vendor, one more
  `r.Register` call for an existing one
- one line in `RegisterAdapters` (`cmd/collector/adapters.go`) for a new
  vendor
- optionally an example device in `collector.yaml`

A diff that also changes `sdk/model`, `internal/synth` or `internal/wire`
means you hit a gap in the domain model. That may be legitimate, but it is
a second contribution and belongs in its own PR — see
["When the device exposes something no facet models"](#when-the-device-exposes-something-no-facet-models).
Changes to `internal/config`, `internal/subject` or `internal/publish` in
an adapter PR almost always mean something went wrong.

**Read the reference adapters end to end:** `internal/vendors/ntcip/asc.go`,
`dms.go` and `register.go`. They show the per-facet read methods, the
`RegisterTo`/factory shape, and the golden-test layout your adapter should
follow. Two caveats about `ntcip-asc` specifically, both known gaps listed in
[`docs/README.md`](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state):

1. **Its fixtures are hand-typed, not recorded.** `healthyFixture` in
   `asc_test.go` is a map someone wrote. ADR 0008 requires a recording —
   see [the "No fixtures, no merge" row](../reference/invariants.md#no-fixtures-no-merge).
   `ntcip-dms`'s fixtures (`dms_test.go`) are transcribed from a real sign
   with a provenance comment; that is the one to copy.
2. **Its alarm-bitmap table has never been validated against a physical
   controller**, as `alarmBitmap`'s own comment in `asc.go` says.

Copy the *structure* — per-facet failure isolation, absence-of-evidence
handling, the golden-test shape — not those two.

## Choose the device kind, and check for an existing facet

An adapter's registry key is `<vendor>-<device_kind>` (`Descriptor.Key()`,
`sdk/adapter/adapter.go`). Work out which kind of contribution you have:

- **A new vendor for a device kind that already exists** (another ASC,
  another DMS) — the facet, differ and wire mapping already exist. Your
  adapter is the only new code.
- **A device kind nothing in `sdk/model` represents** — a bigger job; read
  ["When the device exposes something no facet models"](#when-the-device-exposes-something-no-facet-models)
  first.

Several facet kinds are modeled, diffed and wired to ce-types with no
adapter producing them yet.
[`docs/reference/starter-tasks.md`](../reference/starter-tasks.md) lists
them with what each needs from a device. Check it before assuming you need
a new facet: a camera or a roadside sensor may already have one waiting.

## Pick the transport, and parse the connection block

One transport ships today: `sdk/transport/snmp`, with its replay fake
`sdk/transport/snmp/snmptest`. If your device speaks SNMP, use it. If it
does not, build a transport first — the `add-transport` skill
(`.claude/skills/add-transport/SKILL.md`) is that workflow — and nothing in
`internal/` or `sdk/adapter` changes to accommodate it.

**The core does not parse the `connection` block; your factory does.**
`config.Load` checks only that `vendor` and `device_kind` resolve and hands
the block over untouched, so your factory is the trust boundary for
everything inside it
([ADR 0014](../adr/0014-config-is-the-trust-boundary.md);
[`pluggability.md`'s "The opaque `connection` block"](../explanation/pluggability.md#the-opaque-connection-block)
explains why). Parse it strictly with the helpers in `sdk/adapter/conn.go`:

- `adapter.OnlyKeys(path, m, allowed...)` rejects any key you don't know,
  so a misspelled `comunity` fails boot instead of running as `public`.
  Call it on the top-level block and on every nested one.
- `adapter.OptString(path, m, key)` reads an optional string: absent is
  `""`, but a value of the wrong type (`address: 161`, a YAML number) is
  an error, not "absent".

Then reject a missing required field, and fill defaults for optional ones,
inside the factory — at boot, not on the first poll.
`parseSNMPBlock` in `internal/vendors/ntcip/register.go` is the worked
example. Don't shorten the reads to `v, _ := m["address"].(string)`: that
form turns an operator's typo into a silent default.
[`configuration.md`](../reference/configuration.md)'s `devices.connection`
row is the field reference this cashes out to.

## Implement the read: one method per facet

`Read(ctx) (*model.Snapshot, error)` — the `StateReader` method — is where
the device interaction happens. Follow `ntcip-asc`'s shape: one
unconditional call per facet method (`a.readSignalStatus(...)`,
`a.readFaultSet(...)`, ...), never gated on whether an earlier one
succeeded. Each facet method decides independently:

- **Read succeeded, with data or genuinely none** — append the facet to
  `snap.Facets`. No detectors wired, no active faults, no incidents in view
  is a healthy, empty facet, not an error.
- **That facet's read failed** (an unanswered OID, a malformed value, a
  sub-request that errored while the rest succeeded) — append a
  `model.FacetError{Kind, Err}` to `snap.Errors` and nothing to
  `snap.Facets` for that kind.
- **The whole poll failed** (the device is unreachable) — return an error
  from `Read`. The runner turns it into a reachability event, not a fault.

Never collapse the middle case into the first. That is the most
consequential rule in an adapter:
[absence of evidence is never a state change](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).
[`adapter-to-model.md`'s "Present, absent, or empty"](../explanation/adapter-to-model.md#present-absent-or-empty-three-states-a-facet-can-be-in)
works through it, using `readFaultSet`'s zero-bits-versus-unanswered-OID
contrast.

Two more things to read in the source rather than here: how
`readSignalStatus`, `readFaultSet` and `readDetectors` fail independently
within one `Read`, and why `readDetectors` batches the whole detector table
into one `Get` (its doc comment has the round-trip arithmetic). `ntcip-dms`
adds a case `asc` doesn't have: an optional OID kept out of the main
request because a device that lacks it would fail the whole PDU
(`mergeOptionalHumidity` in `dms.go`).

## Set capability bits to match what you implemented

`Descriptor.Caps` is a bitset (`sdk/adapter/adapter.go`) with one bit
today: `CapState`, for `StateReader`. Set it, as `ascDescriptor` and
`dmsDescriptor` do. If your device produces discrete events rather than
state (a log fetcher, say), the SDK has no interface for that yet, by
design: [ADR 0021](../adr/0021-sdk-interfaces-ship-with-their-first-implementer.md)
says an interface ships with its first implementer, so that adapter would
define it and its runner path too. Raise it before writing the adapter.

## Register the adapter

Add a `RegisterTo(r *adapter.Registry)` function in
`internal/vendors/<vendor>/register.go` that calls `r.Register` with your
`Descriptor` and a factory closure (parse the connection block, dial, wrap
the client in your adapter). For a new vendor, add one call to
`RegisterAdapters` in `cmd/collector/adapters.go` — the one place the
binary decides which vendors it ships with.
[`pluggability.md`'s "Registering an adapter"](../explanation/pluggability.md#registering-an-adapter-the-one-place-the-binary-decides)
covers why it is compile-time and in-tree. `internal/app` takes no part; it
only receives the populated registry.

Lay the package out as `ntcip` is: `internal/vendors/<vendor>/<kind>.go`
plus `<kind>_test.go` and a shared `register.go`, not a `<kind>/`
subdirectory ([`starter-tasks.md`'s note](../reference/starter-tasks.md#the-reference-implementation)
explains where the subdirectory shape came from).

## When the device exposes something no facet models

If the device reports a genuinely different kind of state — not a value
you would have to squeeze into an existing field — don't bend an existing
facet. It's a **facet contribution**:
[`add-a-domain-facet.md`](add-a-domain-facet.md) is the guide, and
[`adapter-to-model.md`'s "Facets are per-device-kind, never per-vendor"](../explanation/adapter-to-model.md#facets-are-per-device-kind-never-per-vendor)
explains where the line is drawn.

Send it as a **separate PR**, landed before (or reviewed alongside) the
adapter. A facet change is reviewed at the domain-model level — differ
correctness, the absence path, whether the concept belongs to the device
kind rather than your vendor's product line — which has little to do with
reviewing a vendor integration.

## Meet the test bar

[`test-requirements.md`'s "A new adapter"](../reference/test-requirements.md#a-new-adapter)
is the checklist a reviewer holds your PR to: a golden read test per facet,
a facet-failure test per facet, proof that facets fail independently,
connection-parse rejection tests, and the capability bits. Read it before
you open the PR. Examples of each, in `internal/vendors/ntcip/`:

- golden reads — `TestASCReadGolden`, `TestDMSReadGolden`
- facet failures — `TestASCUnansweredAlarmIsFaultSetFacetError`,
  `TestDMSUnansweredControlModeIsFacetError`
- independent failure — `TestASCFacetsFailIndependently`,
  `TestDMSEnvGetFailureStillReturnsStatus`
- connection parsing — `TestParseSNMPBlockRejectsUnrecognized`,
  `TestParseSNMPBlockRejectsUnknownConnectionKey` (`register_test.go`)
- capability bits — `TestDMSDescriptorCaps`

**Fixtures must be recorded from the device**, saying what you captured,
from which make, model and firmware, and when. For an SNMP adapter,
`collector record-fixture` writes the file and its provenance for you;
[`record-fixtures-from-a-device.md`](record-fixtures-from-a-device.md) is
the recipe, and the manual one for other transports. A hand-transcribed
fixture with no provenance comment reads to a reviewer exactly like a
hand-typed one, because
[nothing in the test format can tell them apart](../explanation/testing-strategy.md#fixture-replay-reproducible-not-verified).

## Verify

```bash
make check
```

This is the CI gate: `go vet`, golangci-lint (gofmt included), the
go.mod tidiness check, the whole suite under `-race`, the boundary lint
(which rejects an adapter that imports openits-models — see
[the boundary rule](../reference/invariants.md#adapters-and-sdk-never-import-openits-models)),
and the docs lint. Why `-race` matters for a poll loop is in
[`testing-strategy.md`](../explanation/testing-strategy.md#-race-because-polling-and-publishing-are-concurrent).

**Then read the review checklist** your PR will be held to:
[`.claude/skills/review-adapter-contribution/SKILL.md`](../../.claude/skills/review-adapter-contribution/SKILL.md).
It is plain markdown, and it weighs hardest the things no command can see:
whether your fixtures are really recorded, whether a failed read is
handled as absence rather than a value, whether your facets decompose the
way the domain model expects, and whether the diff stays inside the files
listed under [Before you start](#before-you-start).
