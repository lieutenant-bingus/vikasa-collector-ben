package cameleon

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon/gesrtp"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestLiveACSRead(t *testing.T) {
	// Address stays in the environment, not in the repo (same pattern as DMS_SNMP_ADDR).
	addr := os.Getenv("ACS_SRTP_ADDR")
	if addr == "" {
		t.Skip("set ACS_SRTP_ADDR=host:18245 to poll a real ACS over SRTP")
	}
	client, err := gesrtp.Dial(addr, 8*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	a := NewACS("acs-lab", client, 90, 110,
		[]gateCfg{
			{id: "WG-111", kind: model.GateKindWarning, dsw: 101},
			{id: "BG-114", kind: model.GateKindBarrier, dsw: 104},
		},
		cabinetCfg{lfFault: 179, spFault: 180, door: 181, gateEstop: 182},
	)
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	bank, ok := snap.Facet(model.KindGateBank)
	if !ok {
		t.Fatal("missing gate-bank")
	}
	for _, g := range bank.(model.GateBank).Gates {
		t.Logf("%s mode=%s pos=%s raw=0x%04X", g.ID, g.Mode, g.Position, g.Raw)
		if g.Mode == model.GateModeUnknown {
			t.Errorf("%s mode unknown", g.ID)
		}
		if g.Position == model.GatePositionUnknown {
			t.Errorf("%s position unknown", g.ID)
		}
	}
	if _, ok := snap.Facet(model.KindFaultSet); !ok {
		t.Fatal("missing fault-set")
	}
}

func TestDecodeGateWordACS1Live(t *testing.T) {
	// 0x0025 = mode auto (1) | lock-open (bit2) | status closed (2<<4)
	g := DecodeGateWord("WG-111", model.GateKindWarning, 0x0025)
	want := model.GateReading{
		ID: "WG-111", Kind: model.GateKindWarning,
		Mode: model.GateModeAuto, Position: model.GatePositionClosed,
		LockOpen: true, Raw: 0x0025,
	}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("got %+v want %+v", g, want)
	}
	// 0x0021 = auto + closed, no lock-open
	g = DecodeGateWord("BG-114", model.GateKindBarrier, 0x0021)
	if g.LockOpen || g.Position != model.GatePositionClosed || g.Mode != model.GateModeAuto {
		t.Fatalf("0x0021 decode = %+v", g)
	}
}

func TestDecodeGateWordClampsOutOfRange(t *testing.T) {
	// position bits 4-6 = 7 → unknown
	g := DecodeGateWord("X", model.GateKindUnknown, 0x0070)
	if g.Position != model.GatePositionUnknown {
		t.Fatalf("position = %v, want unknown", g.Position)
	}
}

func TestDecodeGateAlarms(t *testing.T) {
	w := uint16((1 << 10) | (1 << 12) | (1 << 4)) // failed-close + estop + closing
	g := DecodeGateWord("WG-1", model.GateKindWarning, w)
	if !g.FailedClose || !g.Estop || g.Position != model.GatePositionClosing {
		t.Fatalf("alarms decode = %+v", g)
	}
	fs := gateFaults(g)
	if len(fs) != 2 {
		t.Fatalf("faults = %+v", fs)
	}
}

func TestACSReadGoldenACS1(t *testing.T) {
	client := &staticWords{start: 90, words: acs1DSWR90}
	gates := []gateCfg{
		{id: "WG-111", kind: model.GateKindWarning, dsw: 101},
		{id: "BG-114", kind: model.GateKindBarrier, dsw: 104},
	}
	cab := cabinetCfg{lfFault: 179, spFault: 180, door: 181, gateEstop: 182}
	a := NewACS("acs-1", client, 90, 110, gates, cab)

	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.DeviceID != "acs-1" || snap.SampledAt.IsZero() {
		t.Fatalf("bad header: %+v", snap)
	}
	f, ok := snap.Facet(model.KindGateBank)
	if !ok {
		t.Fatal("missing gate-bank")
	}
	bank := f.(model.GateBank)
	if len(bank.Gates) != 2 {
		t.Fatalf("gates = %d", len(bank.Gates))
	}
	// Sorted by ID: BG-114 then WG-111
	if bank.Gates[0].ID != "BG-114" || bank.Gates[1].ID != "WG-111" {
		t.Fatalf("order = %q, %q", bank.Gates[0].ID, bank.Gates[1].ID)
	}
	if bank.Gates[1].Raw != 0x0025 || bank.Gates[1].Position != model.GatePositionClosed {
		t.Fatalf("WG-111 = %+v", bank.Gates[1])
	}
	if bank.Gates[0].Raw != 0x0021 || bank.Gates[0].Kind != model.GateKindBarrier {
		t.Fatalf("BG-114 = %+v", bank.Gates[0])
	}
	fs, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set")
	}
	if n := len(fs.(model.FaultSet).Faults); n != 0 {
		t.Fatalf("live snapshot had no alarms; got %d faults", n)
	}
}

func TestACSReadRaisesGateAndCabinetFaults(t *testing.T) {
	words := append([]uint16(nil), acs1DSWR90...)
	// %R101 (idx 11): set failed-open + estop on top of 0x0025
	words[101-90] = 0x0025 | (1 << 11) | (1 << 12)
	words[182-90] = 1 // cabinet gate estop
	client := &staticWords{start: 90, words: words}
	a := NewACS("acs-1", client, 90, 110,
		[]gateCfg{{id: "WG-111", kind: model.GateKindWarning, dsw: 101}},
		cabinetCfg{gateEstop: 182})
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	fs, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set")
	}
	got := map[string]bool{}
	for _, f := range fs.(model.FaultSet).Faults {
		got[f.ID] = true
	}
	for _, id := range []string{
		"gate/WG-111/failed-to-open", "gate/WG-111/estop", "cabinet/gate-estop",
	} {
		if !got[id] {
			t.Fatalf("missing fault %q in %+v", id, got)
		}
	}
}

func TestACSUnreachableIsHardError(t *testing.T) {
	a := NewACS("acs-1", &errClient{err: context.DeadlineExceeded}, 90, 110,
		[]gateCfg{{id: "WG-111", kind: model.GateKindWarning, dsw: 101}}, cabinetCfg{})
	_, err := a.Read(context.Background())
	if err == nil {
		t.Fatal("expected hard error")
	}
}

type errClient struct{ err error }

func (e *errClient) ReadWords(byte, int, int) ([]uint16, error) { return nil, e.err }
func (e *errClient) Close() error                               { return nil }

func TestParseACSBlockRejectsBadConfig(t *testing.T) {
	_, err := parseACSBlock(map[string]any{})
	if err == nil {
		t.Fatal("empty conn must fail")
	}
	_, err = parseACSBlock(map[string]any{
		"srtp":  map[string]any{"address": "10.0.0.1:18245"},
		"dsw":   map[string]any{"start": 90, "length": 110},
		"gates": []any{map[string]any{"id": "WG-111", "dsw": 50, "kind": "warning"}},
	})
	if err == nil {
		t.Fatal("dsw outside block must fail")
	}
	_, err = parseACSBlock(map[string]any{
		"srtp":    map[string]any{"address": "10.0.0.1:18245"},
		"dsw":     map[string]any{"start": 90, "length": 110},
		"gates":   []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
		"mystery": true,
	})
	if err == nil {
		t.Fatal("unrecognized key must fail")
	}
}

func TestParseACSBlockAcceptsGateKind(t *testing.T) {
	cfg, err := parseACSBlock(map[string]any{
		"srtp":  map[string]any{"address": "10.0.0.1:18245"},
		"dsw":   map[string]any{"start": 90, "length": 100},
		"gates": []any{map[string]any{"id": "WG-100", "dsw": 101, "kind": "gate"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.gates[0].kind != model.GateKindUnknown {
		t.Fatalf("kind = %v", cfg.gates[0].kind)
	}
}

func TestLazyBuildDoesNotDial(t *testing.T) {
	reg := adapter.NewRegistry()
	RegisterTo(reg)
	a, err := reg.Build("cameleon", "acs", "acs-down", map[string]any{
		"srtp":  map[string]any{"address": "203.0.113.1:18245", "timeout": "1s"},
		"dsw":   map[string]any{"start": 90, "length": 110},
		"gates": []any{map[string]any{"id": "WG-1", "dsw": 101, "kind": "warning"}},
	})
	if err != nil {
		t.Fatalf("Build must succeed without dialing: %v", err)
	}
	_ = a.Close()
}

func TestParseACSBlockOK(t *testing.T) {
	cfg, err := parseACSBlock(map[string]any{
		"srtp": map[string]any{"address": "10.0.0.1:18245", "timeout": "3s"},
		"dsw":  map[string]any{"start": 90, "length": 110},
		"gates": []any{
			map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"},
			map[string]any{"id": "BG-114", "dsw": 104, "kind": "barrier"},
		},
		"cabinet": map[string]any{"lf_fault": 179, "gate_estop": 182},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.addr != "10.0.0.1:18245" || cfg.dswStart != 90 || len(cfg.gates) != 2 {
		t.Fatalf("%+v", cfg)
	}
	if cfg.cabinet.lfFault != 179 || cfg.cabinet.gateEstop != 182 {
		t.Fatalf("cabinet %+v", cfg.cabinet)
	}
}
