package cameleon

import (
	"context"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func plcACS(words []uint16, plc plcCfg, now func() time.Time) *acs {
	a := NewACS("acs-1", &staticWords{start: 90, words: words}, 90, 110,
		[]gateCfg{{id: "WG-111", kind: model.GateKindWarning, dsw: 101}},
		cabinetCfg{},
	).(*acs)
	a.plc = plc
	if now != nil {
		a.now = now
	}
	return a
}

func faultIDs(t *testing.T, snap *model.Snapshot) map[string]bool {
	t.Helper()
	fs, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set")
	}
	got := map[string]bool{}
	for _, f := range fs.(model.FaultSet).Faults {
		got[f.ID] = true
	}
	return got
}

func TestPLCStatusBitsRaiseAndClear(t *testing.T) {
	cases := []struct {
		name string
		bit  int
		id   string
	}{
		{"plc fault", 10, "plc/fault"},
		{"cabinet id changed", 7, "plc/cabinet-id-changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			words := make([]uint16, 110)
			words[0] = 1 << tc.bit
			a := plcACS(words, plcCfg{}, nil)
			e := synth.NewEngine(synth.NewFaultDiffer())
			var raised []string
			for _, ev := range e.Apply(mustSnap(t, a)) {
				fr, ok := ev.(model.FaultRaised)
				if !ok {
					t.Fatalf("event %T", ev)
				}
				raised = append(raised, fr.FaultID)
			}
			if len(raised) != 1 || raised[0] != tc.id {
				t.Fatalf("raises = %v", raised)
			}
			words[0] = 0
			var cleared []string
			for _, ev := range e.Apply(mustSnap(t, a)) {
				fc, ok := ev.(model.FaultCleared)
				if !ok {
					t.Fatalf("event %T", ev)
				}
				cleared = append(cleared, fc.FaultID)
			}
			if len(cleared) != 1 || cleared[0] != tc.id {
				t.Fatalf("clears = %v", cleared)
			}
		})
	}
}

func TestPLCOptionalKeysAbsentDoNotFault(t *testing.T) {
	words := make([]uint16, 110)
	words[0] = 2 << 1 // cabinet id 2, no fault bit
	words[1], words[2] = 0x1111, 0x2222
	words[3], words[4] = 0x3333, 0x4444
	got := faultIDs(t, mustSnap(t, plcACS(words, plcCfg{}, nil)))
	for _, id := range []string{"plc/cabinet-id-mismatch", "plc/program-crc-mismatch", "plc/config-crc-mismatch", "plc/fault"} {
		if got[id] {
			t.Fatalf("unconfigured key raised %s in %+v", id, got)
		}
	}
	cfg := plcCfg{hasCabinetID: true, cabinetID: 1, hasProgramCRC: true, programCRC: 0, hasConfigCRC: true, configCRC: 0}
	got = faultIDs(t, mustSnap(t, plcACS(words, cfg, nil)))
	for _, id := range []string{"plc/cabinet-id-mismatch", "plc/program-crc-mismatch", "plc/config-crc-mismatch"} {
		if !got[id] {
			t.Fatalf("missing %s in %+v", id, got)
		}
	}
}

func TestPLCWatchdogStallAndWrap(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	words := make([]uint16, 110)
	words[0] = 31 << 11
	a := plcACS(words, plcCfg{watchdogStall: 5 * time.Second}, func() time.Time { return now })
	if faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("first sample must not raise plc/not-scanning")
	}
	now = now.Add(4 * time.Second)
	if faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("raised before the stall window")
	}
	now = now.Add(time.Second)
	if !faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("wanted plc/not-scanning at 5s")
	}
	words[0] = 0 // 31 -> 0 is an increment, not a stall
	now = now.Add(10 * time.Second)
	if faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("watchdog wrap 31 -> 0 must count as a change")
	}
	now = now.Add(5 * time.Second)
	if !faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("wanted a new stall after the wrap")
	}
}

func TestPLCWatchdogFailedReadRestartsTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	words := make([]uint16, 110)
	words[0] = 1 << 11
	a := plcACS(words, plcCfg{watchdogStall: 5 * time.Second}, func() time.Time { return now })
	_ = mustSnap(t, a)
	now = now.Add(5 * time.Second)
	if !faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("wanted the stall before the failed read")
	}
	a.client = &errClient{err: context.DeadlineExceeded}
	if _, err := a.Read(context.Background()); err == nil {
		t.Fatal("expected hard error")
	}
	if a.wdHave {
		t.Fatal("failed read left the watchdog window running")
	}
	a.client = &staticWords{start: 90, words: words}
	now = now.Add(5 * time.Second)
	if faultIDs(t, mustSnap(t, a))["plc/not-scanning"] {
		t.Fatal("restarted window must not raise on the first sample after the failure")
	}
}

func TestPLCNotScanningFailedReadNeitherClearsNorReraises(t *testing.T) {
	fault := model.Fault{
		ID: "plc/not-scanning", Severity: model.SeverityCritical,
		Category: model.CategoryCabinet, Description: "PLC watchdog has not advanced",
	}
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	with := &model.Snapshot{
		DeviceID: "acs-1", DeviceKind: "acs", SampledAt: at,
		Facets: []model.Facet{model.FaultSet{Faults: []model.Fault{fault}}},
	}
	failed := &model.Snapshot{
		DeviceID: "acs-1", DeviceKind: "acs", SampledAt: at.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindFaultSet, Err: "srtp timeout"}},
	}
	e := synth.NewEngine(synth.NewFaultDiffer())
	if evs := e.Apply(with); len(evs) != 1 {
		t.Fatalf("first poll = %v", evs)
	}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read emitted %v; it must not clear plc/not-scanning", evs)
	}
	with.SampledAt = at.Add(2 * time.Second)
	if evs := e.Apply(with); len(evs) != 0 {
		t.Fatalf("recovery emitted %v; a still-raised plc/not-scanning must not be re-raised", evs)
	}
}

func TestParsePLCBlock(t *testing.T) {
	ok, err := parseACSBlock(map[string]any{
		"srtp":  map[string]any{"address": "<host>:18245"},
		"dsw":   map[string]any{"start": 90, "length": 110},
		"gates": []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
		"plc": map[string]any{
			"cabinet_id":     1,
			"program_crc":    "0xAABBCCDD",
			"config_crc":     "0x10",
			"watchdog_stall": "0s",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok.plc.hasCabinetID || ok.plc.cabinetID != 1 || ok.plc.programCRC != 0xAABBCCDD || ok.plc.configCRC != 0x10 || ok.plc.watchdogStall != 0 {
		t.Fatalf("plc = %+v", ok.plc)
	}
	bare, err := parseACSBlock(map[string]any{
		"srtp":  map[string]any{"address": "<host>:18245"},
		"dsw":   map[string]any{"start": 90, "length": 110},
		"gates": []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bare.plc.watchdogStall != 5*time.Second || bare.plc.hasCabinetID {
		t.Fatalf("default plc = %+v", bare.plc)
	}
	for _, conn := range []map[string]any{
		{"srtp": map[string]any{"address": "<host>:18245"}, "dsw": map[string]any{"start": 90, "length": 110},
			"gates": []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
			"plc":   map[string]any{"cabinet_id": 0}},
		{"srtp": map[string]any{"address": "<host>:18245"}, "dsw": map[string]any{"start": 90, "length": 110},
			"gates": []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
			"plc":   map[string]any{"program_crc": 12}},
		{"srtp": map[string]any{"address": "<host>:18245"}, "dsw": map[string]any{"start": 90, "length": 110},
			"gates": []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
			"plc":   map[string]any{"watchdog_stall": "-1s"}},
		{"srtp": map[string]any{"address": "<host>:18245"}, "dsw": map[string]any{"start": 90, "length": 110},
			"gates": []any{map[string]any{"id": "WG-111", "dsw": 101, "kind": "warning"}},
			"plc":   map[string]any{"nope": true}},
		{"srtp": map[string]any{"address": "<host>:18245"}, "dsw": map[string]any{"start": 200, "length": 10},
			"gates": []any{map[string]any{"id": "WG-111", "dsw": 200, "kind": "warning"}},
			"plc":   map[string]any{"cabinet_id": 1}},
	} {
		if _, err := parseACSBlock(conn); err == nil {
			t.Fatalf("accepted bad plc block %#v", conn["plc"])
		}
	}
}

func mustSnap(t *testing.T, a *acs) *model.Snapshot {
	t.Helper()
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap.DeviceKind = "acs"
	return snap
}
