# P1d-1 — Config Schema (`vendor`/`device_kind`/`connection`) + Compat Shim


**Goal:** Add the adapter-selecting config fields (`vendor`, `device_kind`, `connection`) to `DeviceConfig` and a normalization shim that maps legacy `driver_type: snmp` + `device_kind`-in-`driver_config` onto them — so P1d-2 can build adapters from config without breaking any existing config file.

**Architecture:** Additive fields on `config.DeviceConfig` plus a `normalizeDeviceConfig` step in `config.Load` (and the inventory→DeviceConfig projection in P1d-2) that back-fills `vendor`/`device_kind`/`connection` from the legacy `driver_type`/`driver_config` shape when they're absent. Nothing consumes the new fields yet (P1d-2 does), so this phase is behavior-preserving. `connection` is a FLAT `map[string]any` (same shape as the legacy `driver_config` and what the `ntcip` adapter consumes); per-transport sub-blocks are deferred until a dual-transport vendor needs them.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, stdlib `testing`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- **Behavior-preserving:** existing config files (using `driver_type` + `driver_config`) must Load with identical results plus back-filled `vendor`/`device_kind`/`connection`; the existing poll path (still reading `driver_type`/`driver_config`) is untouched. The full suite (10 packages) stays green.
- Compat mapping: legacy SNMP device → `vendor = "ntcip"`, `device_kind` = `driver_config["device_kind"]`, `connection` = the `driver_config` map (flat). New-style configs (`vendor` already set) pass through unchanged.
- Do NOT wire the new fields into `device.Manager`, the scheduler, or the adapter registry — that is P1d-2.
- Commit after each task; `gofmt -s -w .` before committing.

---

### Task 1: Add fields + `normalizeDeviceConfig` shim + tests

**Files:**
- Modify: `internal/config/config.go` (`DeviceConfig` struct ~line 234; `Load` ~line 409; add `normalizeDeviceConfig`)
- Test: `internal/config/config_test.go` (create — this is the config package's first test)

**Interfaces:**
- Produces: `DeviceConfig` gains `Vendor string`, `DeviceKind string`, `Connection map[string]any`. A package-level `normalizeDeviceConfig(d *DeviceConfig)` back-fills them from the legacy shape.

- [ ] **Step 1: Add the fields.** In `internal/config/config.go`, extend the `DeviceConfig` struct (currently `ID`, `DriverType`, `DriverConfig`, `PollInterval`):

```go
// DeviceConfig is the driver-agnostic per-device poller configuration.
type DeviceConfig struct {
	ID           string         `yaml:"id"`
	DriverType   string         `yaml:"driver_type"`
	DriverConfig map[string]any `yaml:"driver_config"`
	PollInterval time.Duration  `yaml:"poll_interval"`

	// Adapter selection (P1d). Vendor + DeviceKind resolve to the adapter
	// registry key "<vendor>-<device_kind>"; Connection is the adapter-parsed
	// connection block (flat, same shape as the legacy driver_config). When
	// unset, normalizeDeviceConfig back-fills them from the legacy
	// driver_type/driver_config fields for compatibility.
	Vendor     string         `yaml:"vendor"`
	DeviceKind string         `yaml:"device_kind"`
	Connection map[string]any `yaml:"connection"`
}
```

- [ ] **Step 2: Add the shim + call it in `Load`.** Add `normalizeDeviceConfig`, and call it in `Load`'s device loop right after `applyDeviceDefaults`:

```go
// In Load(), the existing device loop becomes:
	for i := range cfg.Devices {
		applyDeviceDefaults(&cfg.Devices[i])
		normalizeDeviceConfig(&cfg.Devices[i])
	}
```

```go
// normalizeDeviceConfig back-fills the adapter-selection fields (Vendor,
// DeviceKind, Connection) from the legacy driver_type/driver_config shape when
// they are not set explicitly. Legacy SNMP devices map to the generic "ntcip"
// vendor; device_kind comes from driver_config["device_kind"]; connection is
// the driver_config map itself (flat). New-style configs that already set
// Vendor are left untouched.
func normalizeDeviceConfig(d *DeviceConfig) {
	if d.Vendor == "" && d.DriverType == "snmp" {
		d.Vendor = "ntcip"
	}
	if d.DeviceKind == "" && d.DriverConfig != nil {
		if k, ok := d.DriverConfig["device_kind"].(string); ok {
			d.DeviceKind = k
		}
	}
	if d.Connection == nil && d.DriverConfig != nil {
		d.Connection = d.DriverConfig
	}
}
```

- [ ] **Step 3: Write the tests.** Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeDeviceConfig_LegacySnmp(t *testing.T) {
	d := DeviceConfig{
		ID:         "asc-001",
		DriverType: "snmp",
		DriverConfig: map[string]any{
			"device_kind": "asc",
			"host":        "10.0.0.1",
			"community":   "public",
		},
		PollInterval: 10 * time.Second,
	}
	normalizeDeviceConfig(&d)

	if d.Vendor != "ntcip" {
		t.Errorf("Vendor = %q, want ntcip", d.Vendor)
	}
	if d.DeviceKind != "asc" {
		t.Errorf("DeviceKind = %q, want asc", d.DeviceKind)
	}
	if d.Connection == nil || d.Connection["host"] != "10.0.0.1" {
		t.Errorf("Connection = %v, want the driver_config map", d.Connection)
	}
}

func TestNormalizeDeviceConfig_NewStylePassesThrough(t *testing.T) {
	d := DeviceConfig{
		ID:         "asc-002",
		Vendor:     "qfree",
		DeviceKind: "asc",
		Connection: map[string]any{"host": "10.0.0.2"},
	}
	normalizeDeviceConfig(&d)

	if d.Vendor != "qfree" {
		t.Errorf("Vendor = %q, want qfree (unchanged)", d.Vendor)
	}
	if d.DeviceKind != "asc" {
		t.Errorf("DeviceKind = %q, want asc (unchanged)", d.DeviceKind)
	}
	if d.Connection["host"] != "10.0.0.2" {
		t.Errorf("Connection = %v, want unchanged", d.Connection)
	}
}

func TestNormalizeDeviceConfig_ExplicitVendorNotOverwrittenByLegacyDriverType(t *testing.T) {
	// A config that sets both vendor AND a legacy driver_type keeps its vendor.
	d := DeviceConfig{
		ID:         "asc-003",
		Vendor:     "qfree",
		DriverType: "snmp",
		DriverConfig: map[string]any{"device_kind": "asc"},
	}
	normalizeDeviceConfig(&d)
	if d.Vendor != "qfree" {
		t.Errorf("Vendor = %q, want qfree (explicit vendor wins over legacy driver_type)", d.Vendor)
	}
}

func TestLoad_BackfillsAdapterFieldsFromLegacyConfig(t *testing.T) {
	// End-to-end through Load with a temp YAML file: a legacy device gets its
	// adapter fields back-filled, and existing behavior (poll interval, driver
	// fields) is preserved.
	yaml := `
poller:
  id: poller-1
  region: us-tx
  agency: txdot
  agency_unit: d07
  default_interval: 10s
nats:
  url: nats://localhost:4222
devices:
  - id: asc-001
    driver_type: snmp
    poll_interval: 10s
    driver_config:
      device_kind: asc
      host: 10.0.0.1
`
	path := filepath.Join(t.TempDir(), "poller.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(cfg.Devices))
	}
	d := cfg.Devices[0]
	if d.Vendor != "ntcip" || d.DeviceKind != "asc" {
		t.Errorf("back-fill failed: vendor=%q device_kind=%q", d.Vendor, d.DeviceKind)
	}
	if d.Connection == nil || d.Connection["host"] != "10.0.0.1" {
		t.Errorf("connection not back-filled: %v", d.Connection)
	}
	// Legacy fields preserved.
	if d.DriverType != "snmp" || d.PollInterval != 10*time.Second {
		t.Errorf("legacy fields changed: driver_type=%q interval=%v", d.DriverType, d.PollInterval)
	}
}
```

- [ ] **Step 4: Run tests + full suite.**

Run: `gofmt -s -w . && go test ./internal/config/... -v && go test ./...`
Expected: new config tests PASS; full suite still green (nothing else changed).

- [ ] **Step 5: Commit.**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add vendor/device_kind/connection fields + legacy compat shim"
```

---

## Self-Review

**Spec coverage (design §6–7, P1d config layer):** `DeviceConfig` gains `Vendor`/`DeviceKind`/`Connection` (Task 1 Step 1); `normalizeDeviceConfig` back-fills them from the legacy shape and is wired into `Load` (Steps 2). Tests cover: legacy→ntcip mapping, new-style pass-through, explicit-vendor-wins, and end-to-end `Load` back-fill with legacy fields preserved (Step 3). Deferred by design (documented in Architecture): consuming the fields (P1d-2), per-transport `connection` sub-blocks, and adapter-existence validation (happens at `device.Manager.Add` in P1d-2, since `config.Validate` has no adapter registry).

**Placeholder scan:** None — the end-to-end `Load` test uses `os.WriteFile`/`filepath.Join`/`t.TempDir()` directly.

**Type consistency:** `Connection map[string]any` matches the `driver_config` type and the `adapter.Factory`'s `cfg map[string]any` param (P1c) that will consume it in P1d-2. `normalizeDeviceConfig(d *DeviceConfig)` takes a pointer (mutates in place), called on `&cfg.Devices[i]`.
