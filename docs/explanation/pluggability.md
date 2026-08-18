# Pluggability: how adapters plug in, and why that way

[`architecture.md`](architecture.md) walks the whole pipeline and names the
adapter stage's one obligation: return `sdk/model` types. [`adapter-to-model.md`](adapter-to-model.md)
goes deep on the other side of that obligation — what a `Snapshot` and a
`Facet` are once an adapter has produced one. This document stays upstream
of both: the small, stable surface (`sdk/adapter`) a contributor implements
to become an adapter at all, and the reasoning behind its shape. If you are
about to write a new vendor integration, or you are wondering why the
collector does not support out-of-process plugins, this is the page.

## The adapter contract: four pieces

Everything a contributor needs lives in `sdk/adapter/adapter.go` and
`sdk/adapter/registry.go` — around a hundred lines combined, on purpose
(ADR 0003 treats this surface as a stable public contract, not an internal
implementation detail that happens to be exported).

### Descriptor: what an adapter declares about itself

```go
type Descriptor struct {
	Vendor     string // e.g. "ntcip", "econolite", "qfree"
	DeviceKind string // e.g. "asc", "rsu", "dms"
	Caps       Capability
}

// Key is the registry key: "<vendor>-<device_kind>".
func (d Descriptor) Key() string { return d.Vendor + "-" + d.DeviceKind }
```

A `Descriptor` is vendor × device-kind, nothing else. `ntcip` is itself a
vendor value here, not a special case — it is the generic,
standards-only implementation and the interoperability target every other
vendor's adapter is compared against. The registry key it produces,
`"<vendor>-<device_kind>"`, is what the rest of the boot path uses to find
an adapter — `ntcip-asc` for the one adapter this repo ships today
(`internal/vendors/ntcip/asc.go`'s `ascDescriptor`).

### Capability: what an adapter can do

```go
type Capability uint8

const (
	CapState   Capability = 1 << iota // implements StateReader
	CapEvents                         // implements EventReader
	CapCommand                        // implements Commander
)
```

`Capability` is a bitset an adapter reports on its `Descriptor`, and the
two read-shaped bits correspond directly to two interfaces:

```go
type StateReader interface { // poll → snapshot; core diffs
	Adapter
	Read(ctx context.Context) (*model.Snapshot, error)
}

type EventReader interface { // poll → discrete events; nothing to diff
	Adapter
	Fetch(ctx context.Context) ([]model.Event, error)
}
```

The split is by *semantics*, not transport: an ASC status poll returns
state to be diffed against the last poll (`StateReader`), while a
hi-res-log fetch returns events that already happened and are forwarded
as-is (`EventReader`). Both are still poll-driven — see
["Why pull-only"](#why-pull-only) below. `CapCommand` and its `Commander`
interface are declared and otherwise unused: v1 is collect-only by
decision (ADR 0004), and the capability exists so commanding can bolt on
later without changing any adapter that predates it.

One gap worth knowing about before it surprises you: only `StateReader` is
wired into the poll path today. `internal/runner.New` takes an
`adapter.StateReader` specifically, not a general `Adapter`, so an
`EventReader` adapter would compile and register but have nothing in the
runner to call its `Fetch`. This is tracked as open work, not a design
decision to route around — see
[the known-gaps list](../README.md#known-gaps-and-successor-work).

### Factory: how an adapter gets built

```go
// Factory builds an Adapter for one configured device. conn is the
// device's `connection` config block — opaque to the core, parsed here.
type Factory func(deviceID string, conn map[string]any) (Adapter, error)
```

A `Factory` is called once per configured device, not once per vendor — a
`collector.yaml` with three `ntcip-asc` devices calls the `ntcip-asc`
factory three times, once per device ID, each with its own `connection`
block. See ["The opaque `connection` block"](#the-opaque-connection-block)
for what that block is and who is allowed to look inside it.

### Registry: where adapters live

```go
type Registry struct{ factories map[string]Factory }

func (r *Registry) Register(d Descriptor, f Factory)
func (r *Registry) Known(vendor, deviceKind string) bool
func (r *Registry) Build(vendor, deviceKind, deviceID string, conn map[string]any) (Adapter, error)
```

`Register` panics on a duplicate key — two adapters claiming the same
`<vendor>-<device_kind>` is a programmer error caught at registration time,
not a runtime ambiguity. `Known` and `Build` are the two operations
everything else in the collector needs: `Known` answers "does an adapter
exist for this vendor and device kind," and `Build` constructs one for a
specific device. `internal/vendors/ntcip/register.go`'s `RegisterTo`
function is the pattern every adapter follows — call `Register` once per
device kind the package implements, with a factory closure over the actual
construction logic.

## Registering an adapter: the one place the binary decides

Registration is not automatic or discovered — a compiled-in adapter package
does nothing until something imports it and calls `Register`. That
something is `cmd/collector/main.go`'s `RegisterAdapters`:

```go
func RegisterAdapters(r *adapter.Registry) {
	ntcip.RegisterTo(r)
}
```

Contributing a new vendor means two things: a new package under
`internal/vendors/<vendor>/` (a file per device kind — e.g.
`internal/vendors/ntcip/asc.go` — not a `<kind>/` subdirectory, matching
`architecture.md`'s note on the same point) implementing `Adapter` plus
`StateReader` and/or `EventReader`, and one added line in
`RegisterAdapters`. That line is deliberate, not an oversight to be
automated away: it is the one place in the whole binary that decides which
vendors it ships with, and it is where a maintainer notices a new adapter
landing.

## The opaque `connection` block

A `Factory`'s second argument, `conn map[string]any`, comes straight from
the device's `connection:` block in `collector.yaml`. The core parses
nothing in it — `internal/config.Config.validate` checks that `vendor` and
`device_kind` resolve to a known adapter (see
["Config is the trust boundary, for adapters too"](#config-is-the-trust-boundary-for-adapters-too)
below) and stops there. Everything past that point belongs to the adapter.
`internal/vendors/ntcip/register.go`'s `parseSNMPBlock` is the pattern:

```go
func parseSNMPBlock(conn map[string]any) (snmp.DialConfig, error) {
	raw, ok := conn["snmp"].(map[string]any)
	if !ok {
		return snmp.DialConfig{}, fmt.Errorf("connection.snmp block required")
	}
	addr, _ := raw["address"].(string)
	if addr == "" {
		return snmp.DialConfig{}, fmt.Errorf("connection.snmp.address required")
	}
	...
}
```

A REST-polling adapter's factory would look for a `rest` key instead, with
its own required fields, and the core would never need to change to
accommodate it. This is the load-bearing reason the block is `map[string]any`
rather than a fixed struct: a new transport shape is purely additive, one
adapter package's problem, not a schema the core has to grow a case for.
See [`reference/configuration.md`](../reference/configuration.md) for the
full field-by-field shape of `collector.yaml`, including `connection`.

## Why in-tree, not plugins

[ADR 0003](../adr/0003-stable-sdk-in-tree-adapters.md) decided adapters
compile into the collector binary rather than load as separate plugin
artifacts. The failure mode a plugin ABI creates is the reason: an adapter
distributed as a separate binary or shared object has to keep working
against every version of the core's plugin interface it might be loaded
by, indefinitely, or fail at runtime on a mismatch — and Go's own runtime
`plugin` package requires the plugin and the host to have been built with
the exact same toolchain, which is unworkable for a package contributed by
someone who does not control the collector's release process. A
subprocess-plugin model (`hashicorp/go-plugin` and similar) sidesteps the
toolchain problem but adds a second running process per adapter, which is
weight this project is not willing to spend on cabinet-class edge hardware
with everything else already running on it.

Compiling in-tree turns the same problem into a compile error instead: a
breaking change to `sdk/adapter` or `sdk/model` fails the build for every
adapter in the tree, at PR time, where a reviewer sees it — not as a
runtime surprise inside a cabinet nobody is watching. The cost is that
`sdk/` is a genuinely stable interface contributors depend on, and a
change to it is treated as a breaking change requiring the same care a
public API would get.

## Why pull-only

[ADR 0004](../adr/0004-pull-only-state-and-event-readers.md) decided every
adapter is pull-driven — the runner calls `Read` or `Fetch`; nothing an
adapter does calls back into the core. The reason is where these devices
live: a cabinet collector sits behind cellular or carrier NAT with no
reachable inbound address (the same fact
[ADR 0012](../adr/0012-host-executed-updates.md) and
[ADR 0014](../adr/0014-config-is-the-trust-boundary.md) cite for why the
collector cannot be remotely dialed into). A push or callback design
would need something on the core side listening for connections nothing on
the public internet — or even the operator's own network, most of the
time — can actually reach. Pull sidesteps the problem entirely: the
collector always initiates, so there is never a listener to expose.

`StateReader` and `EventReader` exist as two interfaces rather than one
because "pull" describes the transport direction, not what the data means
once it arrives — collapsing them into one interface with an
event-vs-state flag would push that discrimination into every consumer
instead of settling it once at the interface boundary.

## The rule of three

Nothing in this repo has an overlay or shared-base mechanism for
NTCIP-variant vendors — no OID-overlay table, no shared polling helper
exposed at the architecture level — and that is a deliberate deferral, not
an oversight. Sharing code between closely related vendor adapters (an
NTCIP base an SNMP-heavy vendor extends, say) is left as ordinary library
reuse *inside* `internal/vendors/`, invisible to the core, until there are
roughly three NTCIP-variant adapters to generalize from. Designing an
overlay mechanism against one adapter (`ntcip-asc`, the only one that
exists today) would be designing against a sample size of one.

## Config is the trust boundary, for adapters too

`Registry.Known` is what lets `internal/config.Config.validate` catch an
unrecognized `vendor`/`device_kind` pair at boot instead of at first poll.
Whether that check — and config validation generally — is allowed to let
anything unrecognized through is a load-bearing rule with its own
canonical statement, not restated here; see the "Config is the trust
boundary" row in
[`docs/reference/invariants.md`](../reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized).
