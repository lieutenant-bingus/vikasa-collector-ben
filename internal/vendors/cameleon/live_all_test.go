package cameleon_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// TestLiveAllACSCabinets polls every cameleon-acs device in a local config.
// Addresses stay out of git — point ACS_CONFIG at a *.local.yaml (gitignored).
//
//	python scripts/gen_acs_config.py
//	ACS_CONFIG=configs/collector-acs.local.yaml go test ./internal/vendors/cameleon -run TestLiveAllACS -v -count=1
func TestLiveAllACSCabinets(t *testing.T) {
	cfgPath := os.Getenv("ACS_CONFIG")
	if cfgPath == "" {
		t.Skip("set ACS_CONFIG=/path/to/collector-acs.local.yaml to poll real ACS cabinets")
	}

	reg := adapter.NewRegistry()
	cameleon.RegisterTo(reg)
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		t.Fatalf("load %s: %v", cfgPath, err)
	}
	if len(cfg.Devices) == 0 {
		t.Fatal("no devices in ACS_CONFIG")
	}

	var reachable, unreachable int
	for _, d := range cfg.Devices {
		d := d
		t.Run(d.ID, func(t *testing.T) {
			a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			t.Cleanup(func() { _ = a.Close() })
			sr := a.(adapter.StateReader)

			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			snap, err := sr.Read(ctx)
			if err != nil {
				unreachable++
				t.Logf("unreachable (ok for some cabinets): %v", err)
				return
			}
			reachable++
			bank, ok := snap.Facet(model.KindGateBank)
			if !ok {
				t.Fatal("missing gate-bank")
			}
			gates := bank.(model.GateBank).Gates
			rawGates, ok := d.Connection["gates"].([]any)
			if !ok {
				t.Fatalf("connection.gates type %T", d.Connection["gates"])
			}
			if len(gates) != len(rawGates) {
				t.Fatalf("gates = %d, want %d", len(gates), len(rawGates))
			}
			for _, g := range gates {
				t.Logf("%s mode=%s pos=%s raw=0x%04X alarms=[fc=%v fo=%v estop=%v refused=%v invalid=%v]",
					g.ID, g.Mode, g.Position, g.Raw,
					g.FailedClose, g.FailedOpen, g.Estop, g.CmdRefused, g.InvalidInputs)
				if g.Mode == model.GateModeUnknown && g.Raw != 0 {
					t.Errorf("%s: mode unknown with raw=0x%04X", g.ID, g.Raw)
				}
				status := (g.Raw >> 4) & 7
				if g.Position == model.GatePositionUnknown && status >= 1 && status <= 4 {
					t.Errorf("%s: position unknown with status bits set raw=0x%04X", g.ID, g.Raw)
				}
			}
			fs, ok := snap.Facet(model.KindFaultSet)
			if !ok {
				t.Fatal("missing fault-set")
			}
			for _, f := range fs.(model.FaultSet).Faults {
				t.Logf("fault %s sev=%s: %s", f.ID, f.Severity, f.Description)
			}
		})
	}
	t.Logf("summary: reachable=%d unreachable=%d total=%d", reachable, unreachable, len(cfg.Devices))
	if reachable == 0 {
		t.Fatal("no ACS cabinet reachable — check VPN / ACS_CONFIG addresses")
	}
}
