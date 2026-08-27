package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func dmsEnvSnap(at time.Time, env model.DMSEnvironment) *model.Snapshot {
	return &model.Snapshot{DeviceID: "dms-1", DeviceKind: "dms", SampledAt: at,
		Facets: []model.Facet{env}}
}

var dmsEnvWarm = model.DMSEnvironment{
	BrightnessPercent: 62, BrightnessReported: true,
	AmbientLightPercent: 71, AmbientLightReported: true,
	CabinetTempDeciC: 415, CabinetTempReported: true,
	FaceTempDeciC: 470, FaceTempReported: true,
	FanActive: true, FanReported: true,
}

// A report differ, not a transition differ: the first poll already reports.
func TestDMSEnvFirstPollEmitsReport(t *testing.T) {
	e := NewEngine(NewDMSEnvironmentDiffer())
	evs := e.Apply(dmsEnvSnap(t0, dmsEnvWarm))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 sign-status-report", kinds(evs))
	}
	r, ok := evs[0].(model.DMSSignStatusReport)
	if !ok || r.CabinetTempDeciC != 415 || !r.CabinetTempReported ||
		r.FaceTempDeciC != 470 || !r.FanActive {
		t.Fatalf("bad sign-status-report: %+v", evs[0])
	}
	if r.HumidityReported || r.IlluminanceReported {
		t.Fatalf("sensors the device never answered must stay unreported: %+v", r)
	}
}

// Unchanged readings still report: the event is a cadence, not a diff.
func TestDMSEnvUnchangedStillReports(t *testing.T) {
	e := NewEngine(NewDMSEnvironmentDiffer())
	e.Apply(dmsEnvSnap(t0, dmsEnvWarm))
	if evs := e.Apply(dmsEnvSnap(t0.Add(time.Second), dmsEnvWarm)); len(evs) != 1 {
		t.Fatalf("unchanged environment must still report, got %v", kinds(evs))
	}
}

// The iron rule: a failed environment read is a gap in the series, never a
// repeat of the last reading.
func TestDMSEnvFailedReadEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSEnvironmentDiffer())
	e.Apply(dmsEnvSnap(t0, dmsEnvWarm))

	failed := &model.Snapshot{DeviceID: "dms-1", DeviceKind: "dms", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindDMSEnvironment, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}
}

func TestDMSEnvReportCarriesDeviceKind(t *testing.T) {
	e := NewEngine(NewDMSEnvironmentDiffer())
	evs := e.Apply(dmsEnvSnap(t0, dmsEnvWarm))
	if len(evs) != 1 || evs[0].EventDeviceKind() != "dms" {
		t.Fatalf("sign-status-report must carry DeviceKind=dms, got %+v", evs)
	}
}
