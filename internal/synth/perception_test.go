package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var pcpAt = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func pcpBase() model.Base {
	return model.Base{DeviceID: "lidar-01", DeviceKind: "perception", OccurredAt: pcpAt}
}

func inc(id string, sev model.IncidentSeverity, conf uint8) model.ZoneIncident {
	return model.ZoneIncident{
		IncidentID: id, ZoneID: "zone-a", Type: model.IncidentStoppedVehicle,
		Severity: sev, ObjectClass: model.ObjectTruck, ConfidencePercent: conf,
		SpeedHundredthsKPH: 0, SpeedReported: true, TrackID: 7, TrackEpoch: 2,
	}
}

func set(incs ...model.ZoneIncident) model.ZoneIncidents {
	return model.ZoneIncidents{Incidents: incs}
}

func TestZoneIncidentDiffer_FirstObservationDetectsStandingIncidents(t *testing.T) {
	// Same deliberate exception the fault differ makes: an incident already in
	// progress when the collector starts is a STATE, not a transition we
	// missed. Staying silent would hide a stopped vehicle from the operator
	// for as long as it stays stopped.
	evs := NewZoneIncidentDiffer().Diff(nil, set(inc("i-1", model.IncidentMajor, 90)), pcpBase())
	if len(evs) != 1 {
		t.Fatalf("first observation produced %d events, want 1", len(evs))
	}
	d, ok := evs[0].(model.ZoneIncidentDetected)
	if !ok {
		t.Fatalf("event type = %T", evs[0])
	}
	if d.IncidentID != "i-1" || d.Type != model.IncidentStoppedVehicle {
		t.Errorf("detected = %+v", d)
	}
	if d.EventDeviceKind() != "perception" {
		t.Errorf("DeviceKind = %q", d.EventDeviceKind())
	}
}

func TestZoneIncidentDiffer_NoChangeEmitsNothing(t *testing.T) {
	prev := set(inc("i-1", model.IncidentMajor, 90))
	if evs := NewZoneIncidentDiffer().Diff(prev, set(inc("i-1", model.IncidentMajor, 90)), pcpBase()); len(evs) != 0 {
		t.Fatalf("unchanged incident emitted %d events, want 0", len(evs))
	}
}

func TestZoneIncidentDiffer_AssessmentChangeIsAnUpdate(t *testing.T) {
	prev := set(inc("i-1", model.IncidentMinor, 60))
	evs := NewZoneIncidentDiffer().Diff(prev, set(inc("i-1", model.IncidentMajor, 95)), pcpBase())
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	u, ok := evs[0].(model.ZoneIncidentUpdated)
	if !ok {
		t.Fatalf("event type = %T, want ZoneIncidentUpdated", evs[0])
	}
	if u.Severity != model.IncidentMajor || u.ConfidencePercent != 95 {
		t.Errorf("update = %+v", u)
	}
}

func TestZoneIncidentDiffer_DisappearanceClears(t *testing.T) {
	prev := set(inc("i-1", model.IncidentMajor, 90), inc("i-2", model.IncidentMinor, 50))
	evs := NewZoneIncidentDiffer().Diff(prev, set(inc("i-1", model.IncidentMajor, 90)), pcpBase())
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	c, ok := evs[0].(model.ZoneIncidentCleared)
	if !ok {
		t.Fatalf("event type = %T", evs[0])
	}
	if c.IncidentID != "i-2" {
		t.Errorf("cleared %q, want i-2", c.IncidentID)
	}
}

func TestZoneIncidentDiffer_AxesAreIndependent(t *testing.T) {
	// One poll can detect, update and clear at once; each is its own event.
	prev := set(inc("i-1", model.IncidentMinor, 60), inc("i-2", model.IncidentMajor, 90))
	curr := set(inc("i-1", model.IncidentMajor, 60), inc("i-3", model.IncidentMinor, 40))
	evs := NewZoneIncidentDiffer().Diff(prev, curr, pcpBase())

	var detected, updated, cleared int
	for _, e := range evs {
		switch e.(type) {
		case model.ZoneIncidentDetected:
			detected++
		case model.ZoneIncidentUpdated:
			updated++
		case model.ZoneIncidentCleared:
			cleared++
		}
	}
	if detected != 1 || updated != 1 || cleared != 1 {
		t.Errorf("detected=%d updated=%d cleared=%d, want 1/1/1 (%d events)", detected, updated, cleared, len(evs))
	}
}

func TestZoneIncidentDiffer_ReclassificationIsNotAnUpdate(t *testing.T) {
	// Zone or object class changing means the sensor is describing something
	// else. Reporting that as an "update" to the same incident would hide a
	// re-identification behind a severity tweak.
	prev := set(inc("i-1", model.IncidentMajor, 90))
	changed := inc("i-1", model.IncidentMajor, 90)
	changed.ObjectClass = model.ObjectPedestrian
	if evs := NewZoneIncidentDiffer().Diff(prev, set(changed), pcpBase()); len(evs) != 0 {
		t.Fatalf("reclassification emitted %d events; it is not an assessment update", len(evs))
	}
}
