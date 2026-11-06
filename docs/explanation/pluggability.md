# Pluggability: how adapters plug in, and why that way

[`architecture.md`](architecture.md) names the adapter's one obligation:
return `sdk/model` types. [`adapter-to-model.md`](adapter-to-model.md)
explains what those types are. This document covers the surface a
contributor implements to become an adapter at all, `sdk/adapter`, and the
reasons for its shape. Read it before writing a vendor integration, or if
you are wondering why the collector has no out-of-process plugins.

## The adapter contract

Everything a contributor implements against lives in three small files:
`sdk/adapter/adapter.go`, `registry.go`, and `conn.go`. They are small on
purpose. [ADR 0003](../adr/0003-stable-sdk-in-tree-adapters.md) treats this
surface as a stable public contract, not an internal detail that happens to
be exported.

### Adapter and StateReader: what you implement

```go
type Adapter interface {
	Descriptor() Descriptor
	Close() error
}

type StateReader interface {
	Adapter
	Read(ctx context.Context) (*model.Snapshot, error)
}
```

Every adapter today is a `StateReader`. The runner calls `Read` once per
poll; the adapter returns a snapshot of the device's current state, and the
core diffs it against the previous one. Boot fails if a built adapter is
not a `StateReader` (`buildReaders` in `internal/app/app.go`). `Close`
releases the transport at shutdown.

### Descriptor and Capability: what an adapter declares about itself

```go
type Descriptor struct {
	Vendor     string // e.g. "ntcip", "econolite", "qfree"
	DeviceKind string // e.g. "asc", "rsu", "dms"
	Caps       Capability
}

// Key is the registry key: "<vendor>-<device_kind>".
func (d Descriptor) Key() string { return d.Vendor + "-" + d.DeviceKind }

type Capability uint8

const (
	CapState  Capability = 1 << iota // implements StateReader
	CapEvents                        // also implements EventReader (ADR 0025)
)
```

A `Descriptor` is vendor × device kind, nothing else. `ntcip` is a vendor
value like any other. It is the generic, standards-only implementation that
other vendors' adapters are compared against. The two adapters in the repo
are `ntcip-asc` and `ntcip-dms` (`ascDescriptor` in
`internal/vendors/ntcip/asc.go`, `dmsDescriptor` in `dms.go`).

`Capability` is a bitset with two bits. `CapState` is the state-reading
path every adapter has. `CapEvents` is the event-log path: an
`EventReader` reads a log the device keeps (a signal controller's
high-resolution log) on its own loop, from a cursor the core stores
([ADR 0025](../adr/0025-event-reader-with-checkpointing.md)); `maxtime-asc`
is its first implementer. A command writer will arrive with the adapter
that needs it, as a new bit and a new interface
([ADR 0021](../adr/0021-sdk-interfaces-ship-with-their-first-implementer.md)).
Go interfaces are satisfied implicitly, so adding one changes no existing
adapter.

### Factory and Registry: how an adapter gets built

```go
// Factory builds an Adapter for one configured device. conn is the
// device's `connection` config block — opaque to the core, parsed here.
type Factory func(deviceID string, conn map[string]any) (Adapter, error)

func (r *Registry) Register(d Descriptor, f Factory)
func (r *Registry) Known(vendor, deviceKind string) bool
func (r *Registry) Build(vendor, deviceKind, deviceID string, conn map[string]any) (Adapter, error)
```

A factory is called once per configured device, not once per vendor. Three
`ntcip-asc` devices in `collector.yaml` mean three calls, each with its own
`connection` block.

`Register` panics on a duplicate key: two adapters claiming the same
`<vendor>-<device_kind>` is a programming error, caught at startup. `Known`
answers "is there an adapter for this vendor and kind", which config
validation uses. `Build` constructs one for a device, and returns an error
wrapping `ErrNotRegistered` for an unknown pair.
`internal/vendors/ntcip/register.go`'s `RegisterTo` is the pattern: one
`Register` call per device kind the package implements, each with a factory
that parses the connection block and dials the transport.

## Registering an adapter: the one place the binary decides

Registration is not automatic. A compiled-in adapter package does nothing
until something calls its registration function. That happens in exactly
one place, `cmd/collector/adapters.go`:

```go
func RegisterAdapters(r *adapter.Registry) {
	ntcip.RegisterTo(r)
}
```

Contributing a new vendor therefore means two things: a package under
`internal/vendors/<vendor>/`, with one file per device kind, that implements
`StateReader`; and one added line in `RegisterAdapters`. That line is
deliberate, not something to automate away. It is the one place in the
binary that decides which vendors ship, and it is where a maintainer
notices a new adapter landing.

## The opaque `connection` block

A factory's `conn` argument is the device's `connection:` block from
`collector.yaml`, passed through unparsed. Config validation checks that
`vendor` and `device_kind` resolve to a registered adapter and stops there.
Everything inside the block belongs to the adapter.

The block is `map[string]any` rather than a fixed struct so that a new
transport shape is purely additive. A REST adapter would look for a `rest`
key with its own fields, and the core would not change.

Unparsed by the core does not mean unchecked. The adapter is the trust
boundary for its own block
([ADR 0014](../adr/0014-config-is-the-trust-boundary.md)), and `conn.go`
gives it two helpers that make the strict reading the easy one:

- `adapter.OnlyKeys(path, m, allowed...)` rejects any key the adapter does
  not know.
- `adapter.OptString(path, m, key)` reads an optional string, and rejects a
  present value of the wrong type.

`parseSNMPBlock` in `internal/vendors/ntcip/register.go` is the pattern:

```go
func parseSNMPBlock(conn map[string]any) (snmp.DialConfig, error) {
	if err := adapter.OnlyKeys("connection", conn, "snmp"); err != nil {
		return snmp.DialConfig{}, err
	}
	raw, ok := conn["snmp"].(map[string]any)
	if !ok {
		return snmp.DialConfig{}, errors.New("connection.snmp block required")
	}
	if err := adapter.OnlyKeys("connection.snmp", raw, "address", "community", "version"); err != nil {
		return snmp.DialConfig{}, err
	}
	addr, err := adapter.OptString("connection.snmp", raw, "address")
	...
}
```

The lenient alternative, `addr, _ := raw["address"].(string)`, does
neither: a misspelt key or a YAML number silently becomes the default. For
example, `version: 1` (a YAML integer) would quietly run as v2c.
[`reference/configuration.md`](../reference/configuration.md) documents the
full shape of `collector.yaml`, including `connection`.

## Why in-tree, not plugins

[ADR 0003](../adr/0003-stable-sdk-in-tree-adapters.md) decided that adapters
compile into the collector binary rather than load as separate plugins. A
plugin interface has a failure mode this project cannot afford. A plugin
built separately must keep working against every core version that might
load it, or fail at runtime on a mismatch. Go's own `plugin` package also
requires the plugin and host to be built with exactly the same toolchain,
which is unworkable for code contributed by people outside the release
process. Subprocess plugins (`hashicorp/go-plugin` and similar) avoid the
toolchain problem but add a running process per adapter, which is weight
cabinet hardware cannot spare.

Compiling in-tree turns that runtime failure into a compile error. A
breaking change to `sdk/adapter` or `sdk/model` fails the build for every
adapter in the tree, at PR time, where a reviewer sees it. The cost is that
`sdk/` must be treated as a public API: a change to it gets the care a
breaking change deserves.

## Why pull-only

[ADR 0004](../adr/0004-pull-only-state-and-event-readers.md) decided that
every adapter is pull-driven: the runner calls the adapter, and nothing an
adapter does calls back into the core. The reason is the devices
themselves. A signal controller answering SNMP, or a log endpoint waiting
to be fetched, does not call out on its own, so there is no push source to
build a listener for. A listener with nothing to drive it would be
speculative infrastructure.

This is about the collector's local link to devices in the cabinet. It is
a separate matter from the cabinet's WAN uplink, where carrier NAT keeps
the fleet from dialling *into* the collector
([ADR 0012](../adr/0012-host-executed-updates.md)).

ADR 0004 also keeps state and events on separate interfaces, even though
both are pulled. "Pull" describes the transport direction, not what the
data means. One interface with a state-or-event flag would push that
distinction into every consumer instead of settling it once at the
interface. `StateReader` and `EventReader` are those two interfaces
(ADR 0025).

## The rule of three

There is no overlay or shared-base mechanism for vendors that vary NTCIP:
no OID-overlay table, no shared polling helper exposed at the architecture
level. That is a deliberate deferral. Sharing between closely related
adapters stays ordinary library reuse *inside* `internal/vendors/`,
invisible to the core. The two `ntcip` adapters already share
`parseSNMPBlock` and the `sdk/transport/snmp` client that way. A general
mechanism waits until there are roughly three NTCIP-variant vendors to
generalise from, rather than being designed against a sample of one.

## Config is the trust boundary, for adapters too

`Registry.Known` is what lets `internal/config` reject an unregistered
`vendor`/`device_kind` pair at boot rather than at first poll. Whether
config validation may let anything unrecognized through is a rule with a
canonical statement:
[config is the trust boundary](../reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized).
