package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewZoneIncidentDiffer turns consecutive incident sets into detected /
// updated / cleared events, keyed on the sensor's own IncidentID.
//
// Structurally this is the fault differ with a third axis. Faults and
// incidents are both SETS of standing conditions identified by a stable id,
// so appearance and disappearance mean the same thing in both. Incidents add
// a middle state: an active incident whose assessment moves without the
// incident itself starting or ending.
func NewZoneIncidentDiffer() Differ { return zoneIncidentDiffer{} }

type zoneIncidentDiffer struct{}

func (zoneIncidentDiffer) Kind() model.Kind { return model.KindZoneIncidents }

func (zoneIncidentDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.ZoneIncidents)
	currByID := byIncidentID(c)

	var prevByID map[string]model.ZoneIncident
	if prev != nil {
		if p, ok := prev.(model.ZoneIncidents); ok {
			prevByID = byIncidentID(p)
		}
	}

	var events []model.Event

	// Detected and updated, in a stable order so a poll's events do not
	// reshuffle between runs.
	for _, id := range sortedIncidentIDs(currByID) {
		ci := currByID[id]
		pi, existed := prevByID[id]
		if !existed {
			// On first observation prevByID is nil, so everything currently
			// active is detected. That is the fault differ's exception applied
			// for the same reason: an incident already in progress when the
			// collector starts is a state, not a transition we missed, and
			// silence would hide a stopped vehicle for as long as it stays
			// stopped.
			events = append(events, model.ZoneIncidentDetected{Base: base, ZoneIncident: ci})
			continue
		}
		if assessmentChanged(pi, ci) {
			events = append(events, model.ZoneIncidentUpdated{
				Base:               base,
				IncidentID:         ci.IncidentID,
				ZoneID:             ci.ZoneID,
				Severity:           ci.Severity,
				SpeedHundredthsKPH: ci.SpeedHundredthsKPH,
				SpeedReported:      ci.SpeedReported,
				ConfidencePercent:  ci.ConfidencePercent,
			})
		}
	}

	// Cleared: present before, absent now.
	for _, id := range sortedIncidentIDs(prevByID) {
		if _, still := currByID[id]; still {
			continue
		}
		pi := prevByID[id]
		events = append(events, model.ZoneIncidentCleared{
			Base: base, IncidentID: pi.IncidentID, ZoneID: pi.ZoneID,
		})
	}
	return events
}

// assessmentChanged reports whether an ACTIVE incident's assessment moved.
//
// Only severity, speed and confidence count. Zone, type, object class and
// track identity deliberately do not: if those change the sensor is
// describing something else, and reporting that as an update to the same
// incident would hide a re-identification behind what looks like a severity
// tweak. Such a change is left silent rather than mislabelled — the sensor
// should have issued a new IncidentID.
func assessmentChanged(prev, curr model.ZoneIncident) bool {
	return prev.Severity != curr.Severity ||
		prev.ConfidencePercent != curr.ConfidencePercent ||
		prev.SpeedHundredthsKPH != curr.SpeedHundredthsKPH ||
		prev.SpeedReported != curr.SpeedReported
}

func byIncidentID(zi model.ZoneIncidents) map[string]model.ZoneIncident {
	out := make(map[string]model.ZoneIncident, len(zi.Incidents))
	for _, i := range zi.Incidents {
		out[i.IncidentID] = i
	}
	return out
}

func sortedIncidentIDs(m map[string]model.ZoneIncident) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// NewZoneIntervalDiffer emits a report each time the sensor presents a new
// aggregate interval.
//
// Deliberately a sibling of the traffic-sensor interval differ rather than a
// shared generic: they are two instances of one shape, and the repo's rule of
// three says a third is the moment to unify. Merging now would mean a type
// parameter over facets that have different member names and are free to
// diverge — perception counts by object class, traffic-sensor by length bin.
func NewZoneIntervalDiffer() Differ { return zoneIntervalDiffer{} }

type zoneIntervalDiffer struct{}

func (zoneIntervalDiffer) Kind() model.Kind { return model.KindZoneIntervals }

func (zoneIntervalDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.ZoneIntervals)
	if len(c.Zones) == 0 {
		return nil
	}
	// Same re-read suppression as the traffic differ: polling faster than the
	// sensor's binning window returns one interval repeatedly, and
	// republishing it inflates every count derived from the stream.
	if prev != nil {
		if p, ok := prev.(model.ZoneIntervals); ok && p.IntervalStart.Equal(c.IntervalStart) {
			return nil
		}
	}
	zones := append([]model.ZoneMeasurement(nil), c.Zones...)
	sort.Slice(zones, func(i, j int) bool { return zones[i].ZoneID < zones[j].ZoneID })

	// One interval, two measurements: crossings (throughput) and presence
	// (occupancy). The catalog separates them into two services, so the differ
	// emits two events and each maps to its own ce-type -- the emitter
	// contract is one ce-type per event, and fanning out here rather than in
	// internal/wire keeps that true. See model.ZoneOccupancyIntervalReport for
	// why the split is real and not a wire artefact.
	return []model.Event{
		model.ZoneIntervalReport{
			Base:             base,
			IntervalStart:    c.IntervalStart,
			IntervalDuration: c.IntervalDuration,
			Zones:            zones,
		},
		model.ZoneOccupancyIntervalReport{
			Base:             base,
			IntervalStart:    c.IntervalStart,
			IntervalDuration: c.IntervalDuration,
			Zones:            zones,
		},
	}
}
