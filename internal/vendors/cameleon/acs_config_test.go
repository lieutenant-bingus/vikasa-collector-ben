package cameleon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/ntcip"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

func TestExampleConfigBootsACS1(t *testing.T) {
	reg := adapter.NewRegistry()
	ntcip.RegisterTo(reg)
	RegisterTo(reg)
	cfg, err := config.Load(filepath.Join("..", "..", "..", "collector.yaml"), reg)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	var found bool
	for _, d := range cfg.Devices {
		if d.ID != "acs-1" {
			continue
		}
		found = true
		a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
		if err != nil {
			t.Fatalf("boot acs-1: %v", err)
		}
		t.Cleanup(func() { _ = a.Close() })
	}
	if !found {
		t.Fatal("collector.yaml has no acs-1 device")
	}
}

func TestACS1BootRejectsDuplicateGateAndAddressOutsideBlock(t *testing.T) {
	reg := adapter.NewRegistry()
	RegisterTo(reg)
	header := `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cab-collector
devices:
  - id: acs-1
    vendor: cameleon
    device_kind: acs
    poll_interval: 1s
    connection:
      srtp: { address: "<host>:18245" }
      dsw: { start: 90, length: 110 }
      cabinet: { lf_fault: 179, sp_fault: 180, door: 181, gate_estop: 182 }
      plc: { cabinet_id: 1 }
`
	dup := header + "      gates:\n        - { id: \"WG-111\", dsw: 101, kind: warning }\n        - { id: \"WG-111\", dsw: 102, kind: warning }\n"
	if err := boot(t, reg, dup); err == nil {
		t.Fatal("duplicate gate id must fail boot")
	}
	out := header + "      gates:\n        - { id: \"WG-111\", dsw: 50, kind: warning }\n"
	if err := boot(t, reg, out); err == nil {
		t.Fatal("dsw outside the block must fail boot")
	}
}

func boot(t *testing.T, reg *adapter.Registry, body string) error {
	t.Helper()
	cfg, err := config.Load(writeYAML(t, body), reg)
	if err != nil {
		return err
	}
	d := cfg.Devices[0]
	a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
	if err != nil {
		return err
	}
	_ = a.Close()
	return nil
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
