# P1c — Adapter Layer + ntcip SnapshotReader Adapters Implementation Plan


**Goal:** Introduce the vendor-adapter abstraction — an `adapter` package (interface + capability model + registry) and `vendors/ntcip/{asc,rsu}` `SnapshotReader` adapters that wrap the existing SNMP driver — so P1d can make it the live poll path.

**Architecture:** An `Adapter` advertises a `Descriptor{Vendor, DeviceKind, Caps}`. A `SnapshotReader` adapter returns a `*driver.Reading` from `Read(ctx)`. The `ntcip` adapters are thin wrappers over the existing `sdk/drivers/snmp` driver (built via `snmp.NewFactory`), so all current transport, circuit-breaker, translator, and tracing machinery is reused verbatim — no duplication. Nothing is wired into `main`/scheduler in P1c (that is P1d); the adapters are introduced and unit-tested standalone.

**Tech Stack:** Go 1.26, stdlib `testing`. Reuses `sdk/drivers/snmp`, `internal/translator`, `sdk/driver`.

## Scope boundaries (what P1c does NOT do)

- Does **not** move `driver.Reading` into a `core/reading` package (repo-wide import churn, no functional gain — deferred; may become a type alias later).
- Does **not** change `main`, the scheduler, `device.Manager`, or config. The old `driver.Driver` path stays the live path until P1d.
- Does **not** introduce the `EventProducer` or `Commander` capabilities (P2 reframes ATSPM/TrafficVision as `EventProducer`; P4 adds `Commander`). P1c defines the `Capability` bitset with all three constants but implements only `SnapshotReader`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- New packages: `internal/adapter` and `internal/vendors/ntcip`. (Under `internal/` to match the existing tree; the design's `vendors/` is realized as `internal/vendors/`.)
- Registry key format is exactly `<vendor>-<device_kind>` (e.g. `ntcip-asc`), matching the design.
- The `ntcip` adapters reuse `snmp.NewFactory(translatorReg)` — do not re-implement SNMP client/circuit-breaker/config parsing.
- Behavior-preserving for the existing path: since nothing is wired in, the full existing suite (8 packages) must stay green; the only new code is additive packages + their tests.
- Commit after each task; `gofmt -s -w .` before committing.

---

### Task 1: `internal/adapter` package — interface, capability, registry

**Files:**
- Create: `internal/adapter/adapter.go`
- Create: `internal/adapter/registry.go`
- Test: `internal/adapter/registry_test.go`

**Interfaces:**
- Produces:
  - `adapter.Capability` (uint8 bitset) with `CapSnapshot`, `CapProducer`, `CapCommand`.
  - `adapter.Descriptor{Vendor, DeviceKind string; Caps Capability}` with method `Key() string` returning `Vendor + "-" + DeviceKind`.
  - `adapter.Adapter` interface: `Descriptor() Descriptor`, `Close() error`.
  - `adapter.SnapshotReader` interface: `Adapter` + `Read(ctx context.Context) (*driver.Reading, error)`.
  - `adapter.Factory` = `func(deviceID string, cfg map[string]any) (Adapter, error)`.
  - `adapter.Registry` with `NewRegistry()`, `Register(vendor, deviceKind string, caps Capability, f Factory)`, `Build(vendor, deviceKind, deviceID string, cfg map[string]any) (Adapter, error)`, `Has(vendor, deviceKind string) bool`, `Keys() []string`.

- [ ] **Step 1: Write `internal/adapter/adapter.go`:**

```go
// Package adapter defines the vendor x device-kind adapter abstraction: the
// unit that reads (and later commands) one device. Adapters are registered
// under a "<vendor>-<device_kind>" key and advertise their capabilities so
// the driving loop knows how to run them.
package adapter

import (
	"context"

	"github.com/Vikasa2M/openits-collector/sdk/driver"
)

// Capability is a bitset of what an adapter can do.
type Capability uint8

const (
	// CapSnapshot: the adapter implements SnapshotReader (poll -> Reading;
	// the core diffs consecutive Readings into events).
	CapSnapshot Capability = 1 << iota
	// CapProducer: the adapter emits events straight to a Sink (P2).
	CapProducer
	// CapCommand: the adapter writes commands to the device (P4).
	CapCommand
)

// Has reports whether c includes cap.
func (c Capability) Has(cap Capability) bool { return c&cap != 0 }

// Descriptor identifies an adapter and its capabilities.
type Descriptor struct {
	Vendor     string // e.g. "ntcip", "qfree"
	DeviceKind string // e.g. "asc", "rsu"
	Caps       Capability
}

// Key is the registry key: "<vendor>-<device_kind>".
func (d Descriptor) Key() string { return d.Vendor + "-" + d.DeviceKind }

// Adapter is the common surface every vendor x device-kind unit implements.
type Adapter interface {
	Descriptor() Descriptor
	Close() error
}

// SnapshotReader polls the device and returns a normalized Reading. The core
// runs synth.Diff over consecutive Readings to produce events.
type SnapshotReader interface {
	Adapter
	Read(ctx context.Context) (*driver.Reading, error)
}
```

- [ ] **Step 2: Write `internal/adapter/registry.go`:**

```go
package adapter

import "fmt"

// Factory builds an Adapter for one device from its connection config.
type Factory func(deviceID string, cfg map[string]any) (Adapter, error)

type entry struct {
	caps    Capability
	factory Factory
}

// Registry maps "<vendor>-<device_kind>" keys to adapter factories. Wiring is
// explicit (vendors call RegisterTo from cmd/main); there is no init-time
// global, so the active adapter set is observable at startup.
type Registry struct {
	entries map[string]entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]entry)}
}

// Register adds a factory under "<vendor>-<device_kind>". Panics on duplicate
// registration (a programmer error, matching driver.Registry).
func (r *Registry) Register(vendor, deviceKind string, caps Capability, f Factory) {
	key := vendor + "-" + deviceKind
	if _, ok := r.entries[key]; ok {
		panic("adapter: duplicate registration for " + key)
	}
	r.entries[key] = entry{caps: caps, factory: f}
}

// Build constructs an adapter for the given vendor/device-kind/device.
func (r *Registry) Build(vendor, deviceKind, deviceID string, cfg map[string]any) (Adapter, error) {
	key := vendor + "-" + deviceKind
	e, ok := r.entries[key]
	if !ok {
		return nil, fmt.Errorf("adapter: no adapter registered for %q", key)
	}
	return e.factory(deviceID, cfg)
}

// Has reports whether an adapter is registered for vendor/device-kind.
func (r *Registry) Has(vendor, deviceKind string) bool {
	_, ok := r.entries[vendor+"-"+deviceKind]
	return ok
}

// Keys returns the registered keys (unsorted).
func (r *Registry) Keys() []string {
	out := make([]string, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 3: Write `internal/adapter/registry_test.go`:**

```go
package adapter

import (
	"context"
	"testing"

	"github.com/Vikasa2M/openits-collector/sdk/driver"
)

type fakeAdapter struct{ desc Descriptor }

func (f fakeAdapter) Descriptor() Descriptor                          { return f.desc }
func (f fakeAdapter) Close() error                                    { return nil }
func (f fakeAdapter) Read(context.Context) (*driver.Reading, error)   { return &driver.Reading{}, nil }

func TestRegistry_BuildResolvesByKey(t *testing.T) {
	reg := NewRegistry()
	reg.Register("ntcip", "asc", CapSnapshot, func(deviceID string, _ map[string]any) (Adapter, error) {
		return fakeAdapter{desc: Descriptor{Vendor: "ntcip", DeviceKind: "asc", Caps: CapSnapshot}}, nil
	})

	if !reg.Has("ntcip", "asc") {
		t.Fatal("Has(ntcip, asc) = false")
	}
	a, err := reg.Build("ntcip", "asc", "asc-001", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := a.Descriptor().Key(); got != "ntcip-asc" {
		t.Errorf("Key = %q, want ntcip-asc", got)
	}
	if !a.Descriptor().Caps.Has(CapSnapshot) {
		t.Error("expected CapSnapshot")
	}
}

func TestRegistry_BuildUnknownKeyErrors(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Build("qfree", "asc", "x", nil); err == nil {
		t.Fatal("expected error for unregistered qfree-asc")
	}
}

func TestRegistry_DuplicateRegisterPanics(t *testing.T) {
	reg := NewRegistry()
	f := func(string, map[string]any) (Adapter, error) { return nil, nil }
	reg.Register("ntcip", "asc", CapSnapshot, f)
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	reg.Register("ntcip", "asc", CapSnapshot, f)
}

func TestCapability_Has(t *testing.T) {
	c := CapSnapshot | CapCommand
	if !c.Has(CapSnapshot) || !c.Has(CapCommand) {
		t.Error("expected snapshot+command")
	}
	if c.Has(CapProducer) {
		t.Error("did not expect producer")
	}
}
```

- [ ] **Step 4: Run tests.**

Run: `gofmt -s -w . && go test ./internal/adapter/... -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/adapter/
git commit -m "feat(adapter): adapter interface, capability bitset, and registry"
```

---

### Task 2: `internal/vendors/ntcip` — SnapshotReader adapters over the SNMP driver

**Files:**
- Create: `internal/vendors/ntcip/ntcip.go`
- Test: `internal/vendors/ntcip/ntcip_test.go`

**Interfaces:**
- Consumes: `adapter.{Adapter, SnapshotReader, Descriptor, Capability, CapSnapshot, Registry}` (Task 1); `snmp.NewFactory(*translator.TranslatorRegistry) driver.Factory`, `snmp.CfgDeviceKind`, `snmp.CfgDeviceID`; `driver.{Driver, Config, Reading}`.
- Produces: `ntcip.RegisterTo(adapterReg *adapter.Registry, translatorReg *translator.TranslatorRegistry)` registering `ntcip-asc` and `ntcip-rsu`.

- [ ] **Step 1: Write `internal/vendors/ntcip/ntcip.go`:**

```go
// Package ntcip provides the generic (standard-only) NTCIP SnapshotReader
// adapters — the compat target for existing SNMP-polled ASCs and RSUs. The
// adapter is a thin wrapper over the existing sdk/drivers/snmp driver: it adds
// the vendor/device-kind Descriptor and the SnapshotReader capability, and
// delegates Read to the driver's Collect. The real ntcip-base-plus-vendor-delta
// composition arrives when a concrete vendor (e.g. Q-Free) is onboarded.
package ntcip

import (
	"context"

	"github.com/Vikasa2M/openits-collector/internal/adapter"
	"github.com/Vikasa2M/openits-collector/internal/translator"
	"github.com/Vikasa2M/openits-collector/sdk/driver"
	"github.com/Vikasa2M/openits-collector/sdk/drivers/snmp"
)

// Vendor is the vendor token for the generic NTCIP adapters.
const Vendor = "ntcip"

// snapshotAdapter wraps a driver.Driver as a SnapshotReader.
type snapshotAdapter struct {
	desc adapter.Descriptor
	drv  driver.Driver
}

var _ adapter.SnapshotReader = (*snapshotAdapter)(nil)

func (a *snapshotAdapter) Descriptor() adapter.Descriptor { return a.desc }
func (a *snapshotAdapter) Read(ctx context.Context) (*driver.Reading, error) {
	return a.drv.Collect(ctx)
}
func (a *snapshotAdapter) Close() error { return a.drv.Close() }

// newSnapshotAdapter wraps an already-built driver.Driver. Exposed unexported
// for tests to inject a fake driver without standing up SNMP.
func newSnapshotAdapter(desc adapter.Descriptor, drv driver.Driver) *snapshotAdapter {
	return &snapshotAdapter{desc: desc, drv: drv}
}

// factory returns an adapter.Factory that builds an SNMP driver for the given
// device kind (via the shared snmp driver factory) and wraps it.
func factory(deviceKind string, translatorReg *translator.TranslatorRegistry) adapter.Factory {
	snmpFactory := snmp.NewFactory(translatorReg)
	return func(deviceID string, cfg map[string]any) (adapter.Adapter, error) {
		dc := driver.Config{}
		for k, v := range cfg {
			dc[k] = v
		}
		dc[snmp.CfgDeviceKind] = deviceKind
		dc[snmp.CfgDeviceID] = deviceID
		drv, err := snmpFactory(dc)
		if err != nil {
			return nil, err
		}
		desc := adapter.Descriptor{Vendor: Vendor, DeviceKind: deviceKind, Caps: adapter.CapSnapshot}
		return newSnapshotAdapter(desc, drv), nil
	}
}

// RegisterTo registers ntcip-asc and ntcip-rsu with the adapter registry. The
// translator registry resolves the per-device-kind SNMP translator, exactly as
// the existing SNMP driver path does.
func RegisterTo(adapterReg *adapter.Registry, translatorReg *translator.TranslatorRegistry) {
	adapterReg.Register(Vendor, "asc", adapter.CapSnapshot, factory("asc", translatorReg))
	adapterReg.Register(Vendor, "rsu", adapter.CapSnapshot, factory("rsu", translatorReg))
}
```

- [ ] **Step 2: Write `internal/vendors/ntcip/ntcip_test.go`.** Test the wrapper logic with a fake `driver.Driver` (no network), and the registry wiring:

```go
package ntcip

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/adapter"
	"github.com/Vikasa2M/openits-collector/internal/translator"
	"github.com/Vikasa2M/openits-collector/sdk/driver"
)

type fakeDriver struct {
	reading *driver.Reading
	closed  bool
}

func (f *fakeDriver) Collect(context.Context) (*driver.Reading, error) { return f.reading, nil }
func (f *fakeDriver) Close() error                                     { f.closed = true; return nil }

func TestSnapshotAdapter_ReadDelegatesToDriver(t *testing.T) {
	want := &driver.Reading{ControllerID: "asc-001", SampledAt: time.Unix(1, 0)}
	fd := &fakeDriver{reading: want}
	a := newSnapshotAdapter(
		adapter.Descriptor{Vendor: Vendor, DeviceKind: "asc", Caps: adapter.CapSnapshot},
		fd,
	)

	if a.Descriptor().Key() != "ntcip-asc" {
		t.Errorf("Key = %q, want ntcip-asc", a.Descriptor().Key())
	}
	got, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != want {
		t.Errorf("Read returned %v, want the driver's Reading %v", got, want)
	}
	if err := a.Close(); err != nil || !fd.closed {
		t.Errorf("Close did not delegate to driver (err=%v closed=%v)", err, fd.closed)
	}
}

func TestRegisterTo_RegistersAscAndRsu(t *testing.T) {
	adapterReg := adapter.NewRegistry()
	transReg := translator.NewTranslatorRegistry()
	translator.RegisterStubs(transReg) // asc + rsu stub translators

	RegisterTo(adapterReg, transReg)

	if !adapterReg.Has("ntcip", "asc") || !adapterReg.Has("ntcip", "rsu") {
		t.Fatalf("expected ntcip-asc and ntcip-rsu registered; keys=%v", adapterReg.Keys())
	}

	// Build ntcip-asc with a minimal SNMP connection config; the stub
	// translator means Read won't touch the network, but Build must succeed.
	a, err := adapterReg.Build("ntcip", "asc", "asc-001", map[string]any{"host": "127.0.0.1"})
	if err != nil {
		t.Fatalf("Build ntcip-asc: %v", err)
	}
	defer a.Close()
	if a.Descriptor().Vendor != "ntcip" || a.Descriptor().DeviceKind != "asc" {
		t.Errorf("descriptor = %+v", a.Descriptor())
	}
	sr, ok := a.(adapter.SnapshotReader)
	if !ok {
		t.Fatal("ntcip-asc adapter must implement SnapshotReader")
	}
	// With the stub translator, Read returns an empty Reading with no error.
	if _, err := sr.Read(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Read with stub translator: %v", err)
	}
}
```

> Note: `translator.RegisterStubs` (in `internal/translator/registry.go`) wires stub ASC+RSU translators whose `TranslateRaw` returns an empty Reading without touching SNMP — this lets `Build` + `Read` run without a real device. Verify that helper exists and behaves as described before relying on it; if `Read` still constructs a live SNMP client that errors, assert only that `Build` succeeds and the descriptor/capability are correct, and note the adjustment in your report.

- [ ] **Step 3: Run tests + full suite.**

Run: `gofmt -s -w . && go test ./internal/vendors/ntcip/... -v && go test ./...`
Expected: new package PASS; full suite still green (nothing else changed).

- [ ] **Step 4: Commit.**

```bash
git add internal/vendors/ntcip/
git commit -m "feat(vendors/ntcip): ntcip-asc/ntcip-rsu SnapshotReader adapters over SNMP driver"
```

---

## Self-Review

**Spec coverage (design §3–4, P1c):** `adapter` package delivers the `Adapter`/`SnapshotReader` interfaces, the `Capability` bitset (all three constants, only Snapshot implemented per scope), the `Descriptor` with `<vendor>-<device_kind>` `Key()`, and the `Registry` (Task 1). `internal/vendors/ntcip` delivers the `ntcip-asc`/`ntcip-rsu` adapters as thin wrappers over the existing SNMP driver, reusing `snmp.NewFactory` (Task 2). Deferred-by-design (documented in Scope boundaries): moving `driver.Reading`, wiring into main/scheduler (P1d), `EventProducer`/`Commander` (P2/P4).

**Placeholder scan:** No "TODO"/"similar to". Task 2's one uncertainty (whether `translator.RegisterStubs` lets `Read` run network-free) is handled with an explicit verify-and-adjust instruction plus a concrete fallback assertion — not a placeholder.

**Type consistency:** `adapter.Factory = func(deviceID string, cfg map[string]any) (Adapter, error)` is used consistently in `Registry.Build`, the ntcip `factory`, and both tests. `snapshotAdapter` implements `adapter.SnapshotReader` (compile-time asserted). `snmp.NewFactory(*translator.TranslatorRegistry) driver.Factory`, `snmp.CfgDeviceKind`/`CfgDeviceID`, and `driver.Config`/`driver.Driver`/`driver.Reading` match `sdk/drivers/snmp/driver.go` and `sdk/driver/driver.go`.
