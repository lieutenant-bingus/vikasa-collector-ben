package main

import (
	"path/filepath"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// The README tells users to run `-config collector.yaml`. That example must
// stay loadable against the real registry this binary wires up — otherwise it
// rots silently and the first person to follow the README hits the error.
func TestExampleConfigIsValid(t *testing.T) {
	reg := adapter.NewRegistry()
	RegisterAdapters(reg)

	cfg, err := config.Load(filepath.Join("..", "..", "collector.yaml"), reg)
	if err != nil {
		t.Fatalf("shipped collector.yaml does not load: %v", err)
	}
	if len(cfg.Devices) == 0 {
		t.Fatal("example config should demonstrate at least one device")
	}
	// Boot validation resolves each device against the registry, so reaching
	// here means every vendor/device_kind in the example is really registered.
	for _, d := range cfg.Devices {
		if !reg.Known(d.Vendor, d.DeviceKind) {
			t.Errorf("device %q: %s-%s not registered", d.ID, d.Vendor, d.DeviceKind)
		}
	}
}
