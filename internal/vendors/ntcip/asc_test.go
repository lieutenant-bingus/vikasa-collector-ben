package ntcip

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// Fixture: healthy controller, plan 3, no preemption, no conflict flash.
var healthyFixture = map[string]int64{
	".1.3.6.1.4.1.1206.4.2.1.2.7.0": 2, // operation: normal
	".1.3.6.1.4.1.1206.4.2.1.2.5.0": 0, // flash: none
	".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3, // pattern: plan 3
	".1.3.6.1.4.1.1206.4.2.1.6.5.0": 0, // preempt: none
}

func TestASCReadGolden(t *testing.T) {
	a := NewASC("asc-1", &snmptest.Static{Values: healthyFixture})
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.DeviceID != "asc-1" || snap.SampledAt.IsZero() {
		t.Fatalf("bad header: %+v", snap)
	}
	f, ok := snap.Facet(model.KindSignalStatus)
	if !ok {
		t.Fatal("missing signal-status facet")
	}
	want := model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}
	if got := f.(model.SignalStatus); !reflect.DeepEqual(got, want) {
		t.Fatalf("SignalStatus = %+v, want %+v", got, want)
	}
	// healthyFixture carries no alarm OID (see TestASCUnansweredAlarmIsFaultSetFacetError),
	// so only the signal-status facet is asserted error-free here.
	if snap.FacetFailed(model.KindSignalStatus) {
		t.Fatalf("unexpected signal-status facet error: %+v", snap.Errors)
	}
}

func TestASCReadPreemptionAndFlash(t *testing.T) {
	fx := map[string]int64{
		".1.3.6.1.4.1.1206.4.2.1.2.7.0": 4, // flash mode
		".1.3.6.1.4.1.1206.4.2.1.2.5.0": 2, // conflict flash
		".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3,
		".1.3.6.1.4.1.1206.4.2.1.6.5.0": 2, // preempt 2 active
	}
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := snap.Facet(model.KindSignalStatus)
	want := model.SignalStatus{
		Mode: model.ModeFlash, InConflictFlash: true, ActivePlanID: 3,
		PreemptionActive: true, PreemptionSource: "preempt-2",
	}
	if !reflect.DeepEqual(got.(model.SignalStatus), want) {
		t.Fatalf("SignalStatus = %+v, want %+v", got, want)
	}
}

func TestASCReadPartialFailureIsFacetError(t *testing.T) {
	// Agent answered nothing for the mandatory operation-status OID:
	// the facet must be reported failed, NOT defaulted (absence ≠ state).
	fx := map[string]int64{".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3}
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("partial data must not be a hard error: %v", err)
	}
	if _, ok := snap.Facet(model.KindSignalStatus); ok {
		t.Fatal("incomplete facet must not be present")
	}
	if !snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("expected signal-status FacetError")
	}
}

func TestASCReadTransportErrorIsHardError(t *testing.T) {
	_, err := NewASC("asc-1", &snmptest.Static{Err: errors.New("timeout")}).Read(context.Background())
	if err == nil {
		t.Fatal("transport failure must be a hard Read error")
	}
}

const oidAlarm = ".1.3.6.1.4.1.1206.4.2.1.5.1.0"

func faultsIn(t *testing.T, snap *model.Snapshot) []model.Fault {
	t.Helper()
	f, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set facet")
	}
	return f.(model.FaultSet).Faults
}

func TestASCNoAlarmBitsMeansEmptyFaultSet(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := faultsIn(t, snap); len(got) != 0 {
		t.Fatalf("no alarm bits must yield an empty fault set, got %+v", got)
	}
}

func TestASCFullyHealthyReadProducesNoFacetErrors(t *testing.T) {
	// A genuinely healthy controller: all mandatory OIDs answered, including alarm.
	// Must yield both facets with zero errors.
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	fx[oidAlarm] = 0

	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Blanket guarantee: fully-answered read produces zero facet errors.
	if len(snap.Errors) != 0 {
		t.Fatalf("fully healthy read must have zero facet errors, got %+v", snap.Errors)
	}

	// Both facets must be present and healthy.
	if _, ok := snap.Facet(model.KindSignalStatus); !ok {
		t.Fatal("signal-status facet missing")
	}
	if snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("signal-status facet should not have failed")
	}

	if _, ok := snap.Facet(model.KindFaultSet); !ok {
		t.Fatal("fault-set facet missing")
	}
	if snap.FacetFailed(model.KindFaultSet) {
		t.Fatal("fault-set facet should not have failed")
	}

	// Fault set is empty (zero alarm bits = no faults, not an error).
	if got := faultsIn(t, snap); len(got) != 0 {
		t.Fatalf("zero alarm bits must yield empty fault set, got %+v", got)
	}
}

func TestASCAlarmBitmapDecodeGolden(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	// bits 1 (mmu-fault) and 3 (power-loss), plus bit 12 which is undefined.
	fx[oidAlarm] = (1 << 1) | (1 << 3) | (1 << 12)

	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := faultsIn(t, snap)
	want := []model.Fault{
		{ID: "mmu-fault", Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "MMU fault detected"},
		{ID: "power-loss", Severity: model.SeverityMajor, Category: model.CategoryPower, Description: "Power loss / generator running"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("faults = %+v\nwant     %+v", got, want)
	}
}

func TestASCUnansweredAlarmIsFaultSetFacetError(t *testing.T) {
	// healthyFixture has no alarm OID: the fault facet must be reported failed
	// rather than defaulted to "no faults" — absence of evidence is never a
	// state change, and an empty set would clear every real fault downstream.
	snap, err := NewASC("asc-1", &snmptest.Static{Values: healthyFixture}).Read(context.Background())
	if err != nil {
		t.Fatalf("partial data must not be a hard error: %v", err)
	}
	if _, ok := snap.Facet(model.KindFaultSet); ok {
		t.Fatal("unreadable fault facet must not be present")
	}
	if !snap.FacetFailed(model.KindFaultSet) {
		t.Fatal("expected fault-set FacetError")
	}
	// The other facets are independent and must survive.
	if _, ok := snap.Facet(model.KindSignalStatus); !ok {
		t.Fatal("signal-status must still publish when the alarm OID fails")
	}
}

// Facets are independent failure domains: a dead operation-status OID must not
// suppress the fault set. (Read previously returned early here.)
func TestASCFacetsFailIndependently(t *testing.T) {
	fx := map[string]int64{
		".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3,      // pattern only; operation-status absent
		oidAlarm:                        1 << 2, // cabinet-door
	}
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("signal-status should have failed")
	}
	got := faultsIn(t, snap)
	if len(got) != 1 || got[0].ID != "cabinet-door" {
		t.Fatalf("fault set must still publish, got %+v", got)
	}
}

const (
	oidMaxDetectors = ".1.3.6.1.4.1.1206.4.2.1.2.3.0"
	volPrefix       = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.2"
	occPrefix       = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.4"
)

func withDetectors(base map[string]int64, maxCh int64, chans map[int]struct{ vol, occHalfPct int64 }) map[string]int64 {
	fx := map[string]int64{}
	for k, v := range base {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	fx[oidMaxDetectors] = maxCh
	for ch, d := range chans {
		fx[fmt.Sprintf("%s.%d", volPrefix, ch)] = d.vol
		fx[fmt.Sprintf("%s.%d", occPrefix, ch)] = d.occHalfPct
	}
	return fx
}

func samplesIn(t *testing.T, snap *model.Snapshot) []model.DetectorSample {
	t.Helper()
	f, ok := snap.Facet(model.KindDetectorSamples)
	if !ok {
		t.Fatal("missing detector-samples facet")
	}
	return f.(model.DetectorSamples).Samples
}

// NTCIP occupancy is half-percent (0..200); the domain carries tenths
// (0..1000). 40 half-percent = 20% = 200 tenths.
func TestASCDetectorGoldenAndOccupancyConversion(t *testing.T) {
	fx := withDetectors(healthyFixture, 2, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 1234, occHalfPct: 40},
		2: {vol: 99, occHalfPct: 200},
	})
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []model.DetectorSample{
		{Channel: 1, VolumeCount: 1234, OccupancyTenths: 200},
		{Channel: 2, VolumeCount: 99, OccupancyTenths: 1000},
	}
	if got := samplesIn(t, snap); !reflect.DeepEqual(got, want) {
		t.Fatalf("samples = %+v\nwant      %+v", got, want)
	}
}

// A negative occupancy is out of NTCIP's documented half-percent range
// (0..200). Unclamped, uint16(occ*5) wraps to a huge unsigned value (e.g.
// -1 -> 65531 tenths, a 6553% occupancy); OccupancyTenths must instead clamp
// to the domain's documented floor of 0. The channel's volume is an
// independent, unaffected reading and must still come through.
func TestASCNegativeOccupancyClampsToZero(t *testing.T) {
	fx := withDetectors(healthyFixture, 1, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 77, occHalfPct: -1},
	})
	want := []model.DetectorSample{
		{Channel: 1, VolumeCount: 77, OccupancyTenths: 0},
	}
	if got := samplesIn(t, mustRead(t, fx)); !reflect.DeepEqual(got, want) {
		t.Fatalf("samples = %+v\nwant      %+v", got, want)
	}
}

// An out-of-spec-high occupancy (above the 200 half-percent max) must clamp
// to the domain's documented ceiling of 1000 tenths, not silently overflow
// uint16(occ*5) (250*5 = 1250, which does not overflow uint16 but does
// violate the documented 0..1000 contract) or wrap for larger values.
func TestASCOutOfSpecHighOccupancyClampsTo1000(t *testing.T) {
	fx := withDetectors(healthyFixture, 1, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 88, occHalfPct: 250},
	})
	want := []model.DetectorSample{
		{Channel: 1, VolumeCount: 88, OccupancyTenths: 1000},
	}
	if got := samplesIn(t, mustRead(t, fx)); !reflect.DeepEqual(got, want) {
		t.Fatalf("samples = %+v\nwant      %+v", got, want)
	}
}

// A sparse table is normal: absent channels are skipped, not zero-filled.
func TestASCSparseDetectorTable(t *testing.T) {
	fx := withDetectors(healthyFixture, 5, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 10, occHalfPct: 0},
		4: {vol: 40, occHalfPct: 20},
	})
	got := samplesIn(t, mustRead(t, fx))
	if len(got) != 2 || got[0].Channel != 1 || got[1].Channel != 4 {
		t.Fatalf("sparse table = %+v, want channels 1 and 4", got)
	}
}

// A channel answering volume but not occupancy is half-read; skip it rather
// than report a fabricated 0% occupancy.
func TestASCChannelWithoutOccupancyIsSkipped(t *testing.T) {
	fx := withDetectors(healthyFixture, 2, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 10, occHalfPct: 20},
	})
	fx[fmt.Sprintf("%s.%d", volPrefix, 2)] = 55 // volume only, no occupancy
	got := samplesIn(t, mustRead(t, fx))
	if len(got) != 1 || got[0].Channel != 1 {
		t.Fatalf("half-read channel must be skipped, got %+v", got)
	}
}

// A controller with no detectors is a normal deployment: empty facet, NOT a
// FacetError, which would be a permanent false alarm.
func TestASCNoDetectorsIsEmptyFacetNotError(t *testing.T) {
	fx := withDetectors(healthyFixture, 0, nil)
	snap := mustRead(t, fx)
	if snap.FacetFailed(model.KindDetectorSamples) {
		t.Fatal("no detectors must not be a FacetError")
	}
	if got := samplesIn(t, snap); len(got) != 0 {
		t.Fatalf("want empty samples, got %+v", got)
	}
}

// An unanswered maxVehicleDetectors OID falls back to 32 channels rather than
// reporting the facet failed: the bound is an optimisation, not the data.
func TestASCMissingMaxDetectorsFallsBackTo32(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	fx[fmt.Sprintf("%s.%d", volPrefix, 30)] = 7
	fx[fmt.Sprintf("%s.%d", occPrefix, 30)] = 2
	got := samplesIn(t, mustRead(t, fx))
	if len(got) != 1 || got[0].Channel != 30 {
		t.Fatalf("want channel 30 within the 32-channel fallback, got %+v", got)
	}
}

// An out-of-range maxVehicleDetectors answer (too large, or negative) must
// not be trusted: readDetectors only accepts 0 < v < 256, else falls back
// to 32 channels — the same fallback TestASCMissingMaxDetectorsFallsBackTo32
// exercises for the unanswered case.
//
// Channel 30 alone cannot distinguish "trusted the reported value" from
// "fell back to 32": it is within range either way. Channel 100 is the
// discriminator — it is reachable only if the out-of-range value were
// wrongly trusted (e.g. 256), never under the 32-channel fallback. Fixture
// data is planted at both channels so a broken bound (accepting the
// reported value) would surface channel 100 and fail this test.
func TestASCOutOfRangeMaxDetectorsFallsBackTo32(t *testing.T) {
	for name, maxCh := range map[string]int64{"too-large": 256, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			fx := map[string]int64{}
			for k, v := range healthyFixture {
				fx[k] = v
			}
			fx[oidAlarm] = 0
			fx[oidMaxDetectors] = maxCh
			fx[fmt.Sprintf("%s.%d", volPrefix, 30)] = 7
			fx[fmt.Sprintf("%s.%d", occPrefix, 30)] = 2
			fx[fmt.Sprintf("%s.%d", volPrefix, 100)] = 9
			fx[fmt.Sprintf("%s.%d", occPrefix, 100)] = 4
			got := samplesIn(t, mustRead(t, fx))
			if len(got) != 1 || got[0].Channel != 30 {
				t.Fatalf("want only channel 30 within the 32-channel fallback (channel 100 must not be queried), got %+v", got)
			}
		})
	}
}

// The detector table's second Get can fail independently of the scalar Get:
// the device is reachable (scalars answered) but the table is not. That
// must surface as a FacetError on DetectorSamples ONLY — SignalStatus and
// FaultSet, read from the first Get, must still publish.
func TestASCDetectorTableGetFailureIsFacetError(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	fx[oidMaxDetectors] = 4

	snap, err := NewASC("asc-1", &snmptest.Static{
		Values:   fx,
		FailCall: map[int]error{2: errors.New("timeout")},
	}).Read(context.Background())
	if err != nil {
		t.Fatalf("a failed table Get must not be a hard Read error: %v", err)
	}

	if !snap.FacetFailed(model.KindDetectorSamples) {
		t.Fatal("expected detector-samples FacetError")
	}
	if _, ok := snap.Facet(model.KindDetectorSamples); ok {
		t.Fatal("unreadable detector-samples facet must not be present")
	}

	// Facets are independent failure domains: the scalar-derived facets
	// must still publish even though the second Get failed.
	if _, ok := snap.Facet(model.KindSignalStatus); !ok {
		t.Fatal("signal-status must still publish when the detector table Get fails")
	}
	if snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("signal-status should not have failed")
	}
	if _, ok := snap.Facet(model.KindFaultSet); !ok {
		t.Fatal("fault-set must still publish when the detector table Get fails")
	}
	if snap.FacetFailed(model.KindFaultSet) {
		t.Fatal("fault-set should not have failed")
	}
}

func mustRead(t *testing.T, fx map[string]int64) *model.Snapshot {
	t.Helper()
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return snap
}
