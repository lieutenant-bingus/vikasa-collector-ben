// Package openits maps collector domain events to openits-models wire
// payloads. It is one of the two emitter families described in ADR 0002,
// and the only place in the collector that imports openits-models.
package openits

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	cctvv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/cctv/v1"
	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"
	pcpv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/perception/v1"
	rlv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/reversible_lane/v1"
	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	tsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/traffic_sensor/v1"
	zocv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/zone_occupancy/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// contentType is the profile's per-event protobuf encoding.
const contentType = "application/protobuf"

// ModelVersion is the collector.yaml model_version that selects this emitter.
const ModelVersion = "openits/v1"

// New returns the openits-models emitter. collectorID identifies this
// collector as the OBSERVER of events it synthesizes from polling.
func New(collectorID string) wire.Emitter {
	return newWithEpoch(collectorID, newSequenceEpoch())
}

// newWithEpoch is New with a caller-chosen sequence-epoch. Goldens use it
// so the pinned bytes do not depend on a random epoch.
func newWithEpoch(collectorID, epoch string) *emitter {
	return &emitter{collectorID: collectorID, sequenceEpoch: epoch}
}

func newSequenceEpoch() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("epoch-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type emitter struct {
	collectorID string
	// sequenceEpoch names this process's sequence stream. A collector
	// restart mints a new one. YANG constrains it to an opaque string of
	// length 1..64 with no internal structure; consumers compare it for
	// equality only. It is excluded from ce-id.
	sequenceEpoch string

	// seq holds the mandatory per-source-device event counter. It lives here
	// rather than in the runner because `sequence` is an event-header leaf —
	// it exists only because the wire model demands it, and sdk/model must
	// stay ignorant of wire concerns (ADR 0002).
	//
	// The cost is that Encode is not a pure function. Runners call it
	// concurrently, one goroutine per device, so the map is mutex-guarded.
	mu  sync.Mutex
	seq map[string]uint64
}

// nextSequence returns the next counter value for deviceID.
//
// Numbering starts at 1, not 0. proto3 omits zero values from the wire, so a
// first event numbered 0 would serialize identically to the field being
// absent, and a consumer could not tell "first event from this device" from
// "producer never set sequence". Starting at 1 keeps every emitted sequence
// present on the wire.
//
// The counter is in-memory, so it restarts at 1 when the collector does. That
// is permitted — upstream says sequence MAY reset on restart and that a
// DECREASE signals a restart rather than a gap — and it is why sequence must
// not participate in the ce-id digest: a restart would otherwise change the id
// of an event whose content never changed.
func (e *emitter) nextSequence(deviceID string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.seq == nil {
		e.seq = make(map[string]uint64)
	}
	e.seq[deviceID]++
	return e.seq[deviceID]
}

// key is the emitter's dispatch key. It is the TUPLE (event kind, device
// kind), never the event kind alone: the catalog defines mode-changed and
// fault-raised ONCE and reuses them across services, so the same domain
// shape publishes as signal-control.mode-changed or dms.mode-changed
// depending only on the device. Keying on EventKind alone appears to work
// until a second device kind reuses a shared event, then silently resolves
// to whichever entry was registered last.
type key struct{ event, deviceKind string }

// ceTypeFor is the complete domain→ce-type routing table.
var ceTypeFor = map[key]string{
	{"mode-changed", "asc"}: "openits.signal-control.mode-changed.v1",
	// Device reachability is a catalog fact (ADR 0019 Scope). Only
	// signal-control has a comm-health channel in the pinned catalog; other
	// kinds are declined here and fall back to the collector-owned
	// health.device-status-changed until upstream fans it out.
	{"device-status-changed", "asc"}: "openits.signal-control.comm-health-event.v1",

	{"fault-raised", "asc"}: "openits.signal-control.fault-raised.v1",
	{"fault-raised", "dms"}: "openits.dms.fault-raised.v1",

	// dms-mode-event-kind's mode-value-base is sign-mode. Control mode
	// (dms-control-mode) does not derive from that base, so a control-mode
	// change is not a mode-changed.
	{"display-state-changed", "dms"}: "openits.dms.mode-changed.v1",

	{"fault-cleared", "asc"}: "openits.signal-control.fault-cleared.v1",
	{"fault-cleared", "dms"}: "openits.dms.fault-cleared.v1",

	// The shared fault family extends to every device kind the collector
	// serves, without any new domain surface: model.FaultSet and the fault
	// differ carry no device kind, so this is routing alone.
	{"fault-raised", "cctv"}:            "openits.cctv.fault-raised.v1",
	{"fault-cleared", "cctv"}:           "openits.cctv.fault-cleared.v1",
	{"fault-raised", "traffic-sensor"}:  "openits.traffic-sensor.fault-raised.v1",
	{"fault-cleared", "traffic-sensor"}: "openits.traffic-sensor.fault-cleared.v1",
	{"fault-raised", "perception"}:      "openits.perception.fault-raised.v1",
	{"fault-cleared", "perception"}:     "openits.perception.fault-cleared.v1",

	{"fault-raised", "acs"}:  "openits.reversible-lane.fault-raised.v1",
	{"fault-cleared", "acs"}: "openits.reversible-lane.fault-cleared.v1",

	{"gate-status-changed", "acs"}: "openits.reversible-lane.gate-status-changed.v1",

	{"traffic-interval-report", "traffic-sensor"}: "openits.traffic-sensor.traffic-interval-report.v1",

	{"zone-incident-detected", "perception"}: "openits.perception.zone-incident-detected.v1",
	{"zone-incident-updated", "perception"}:  "openits.perception.zone-incident-updated.v1",
	{"zone-incident-cleared", "perception"}:  "openits.perception.zone-incident-cleared.v1",
	{"zone-interval-report", "perception"}:   "openits.perception.zone-interval-report.v1",

	// The presence half of the same interval. openits-models v0.3.0 moved
	// observed-count and occupancy-percent off the perception report into
	// their own service, so one poll's interval now routes to two ce-types
	// across two services.
	{"zone-occupancy-interval-report", "perception"}: "openits.zone-occupancy.zone-occupancy-interval-report.v1",

	// cctv-mode-event-kind has no mode-value-base: cctv-control-mode is not
	// an operational-mode, so those values cannot ride mode-changed.
	{"cctv-tour-state-changed", "cctv"}: "openits.cctv.tour-state-changed.v1",

	{"plan-changed", "asc"}:              "openits.signal-control.plan-applied.v1",
	{"operational-status-report", "asc"}: "openits.signal-control.operational-status-report.v1",
	{"preemption-activated", "asc"}:      "openits.signal-control.preemption-activated.v1",
	{"preemption-cleared", "asc"}:        "openits.signal-control.preemption-cleared.v1",
	{"detector-report", "asc"}:           "openits.signal-control.detector-report.v1",

	{"message-activation-failed", "dms"}: "openits.dms.message-activation-failed.v1",
	{"message-changed", "dms"}:           "openits.dms.message-changed.v1",
	{"sign-status-report", "dms"}:        "openits.dms.sign-status-report.v1",
}

// CETypes returns every ce-type this emitter can produce, sorted and deduped.
//
// Derived from ceTypeFor rather than maintained alongside it. The two cannot
// drift, which matters more than it looks: boot validation renders this list
// through the operator's subject template to prove every ce-type routes to a
// legal subject, so a hand-kept list that omitted an entry would leave that
// ce-type unchecked and surface as an unroutable event at 3am instead of a
// refusal to start.
//
// Deduping is required, not cosmetic: several device kinds share one ce-type
// (fault-raised, for example), so the routing table has more entries than
// there are ce-types.
func (e *emitter) CETypes() []string {
	seen := make(map[string]bool, len(ceTypeFor))
	out := make([]string, 0, len(ceTypeFor))
	for _, ceType := range ceTypeFor {
		if !seen[ceType] {
			seen[ceType] = true
			out = append(out, ceType)
		}
	}
	sort.Strings(out)
	return out
}

func (e *emitter) Encode(ev model.Event) (*wire.Encoded, bool, error) {
	ceType, ok := ceTypeFor[key{ev.EventKind(), ev.EventDeviceKind()}]
	if !ok {
		return nil, false, nil
	}

	var msg proto.Message
	switch v := ev.(type) {
	case model.ModeChanged:
		// `current` is a mandatory identityref. A domain mode with no
		// controller-mode identity upstream cannot be encoded faithfully, so
		// the event is NOT claimed and falls through to the loud-drop path —
		// the same rule as an unknown DeviceKind. Never substitute a
		// near-neighbour identity to make a warning go away.
		current, ok := controllerModeIdentity(v.To)
		if !ok {
			return nil, false, nil
		}
		// `prior` is optional ("absent when the device just started up"), so an
		// unmappable From is left empty rather than blocking the event.
		prior, _ := controllerModeIdentity(v.From)
		msg = e.modeChanged(v.Base, scTypes+"sc-mode-event-kind", prior, current)
	case model.DMSDisplayStateChanged:
		current, ok := dmsDisplayStateIdentity(v.To)
		if !ok {
			return nil, false, nil
		}
		prior, _ := dmsDisplayStateIdentity(v.From)
		msg = e.modeChanged(v.Base, dmsTypes+"dms-mode-event-kind", prior, current)

	case model.DeviceStatusChanged:
		kind := commHealth + "comm-lost"
		if v.Reachable {
			kind = commHealth + "comm-restored"
		}
		// last-contact is mandatory on comm-lost and is the last valid
		// response. A zero time means this process has never heard from the
		// device. Decline so the collector-owned health.device-status-changed
		// emitter publishes the fallback instead of an invalid comm-lost.
		if !v.Reachable && v.LastContact.IsZero() {
			return nil, false, nil
		}
		// Attempt counters belong to comm-attempt-window, and the YANG
		// forbids them on comm-lost. The read error stays in the log and
		// /status. last-contact is forbidden on comm-restored.
		ev := &commonv1.CommHealthEvent{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
		}
		if !v.Reachable && !v.LastContact.IsZero() {
			ev.LastContact = timestamppb.New(v.LastContact.UTC())
		}
		msg = ev

	case model.FaultRaised:
		// Unlike modes, an unmappable category does NOT decline the event —
		// faultKindIdentity falls back to the service base identity. It only
		// fails for a device kind this emitter does not serve, which the
		// ce-type lookup above has already excluded.
		kind, ok := faultKindIdentity(v.Category, v.DeviceKind, v.FaultID)
		if !ok {
			return nil, false, nil
		}
		raised := &commonv1.FaultRaised{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			FaultId:        v.FaultID,
			Severity:       severityFor(v.Severity),
			FirstObserved:  timestamppb.New(v.OccurredAt.UTC()),
			Component:      gateComponent(v.FaultID),
		}
		if v.Description != "" {
			desc := v.Description
			raised.Description = &desc
		}
		msg = raised
	case model.FaultCleared:
		kind, ok := faultKindIdentity(v.Category, v.DeviceKind, v.FaultID)
		if !ok {
			return nil, false, nil
		}
		msg = &commonv1.FaultCleared{
			// kind matches the raise: the clear carries the fault's category
			// and id, which is how faultKindIdentity selects the leaf.
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			FaultId:        v.FaultID,
			FirstObserved:  whenFirst(v.FirstObserved, v.OccurredAt),
			Component:      gateComponent(v.FaultID),
		}

	case model.PlanChanged:
		// PlanApplied names only the plan now in force. FromPlanID is DROPPED:
		// there is no wire field for it, and the transition is recoverable from
		// consecutive events. Recorded here as the map-or-drop decision it is.
		msg = &scv1.PlanApplied{
			Kind:           scTypes + "sc-coordination-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			PlanId:         u32p(v.ToPlanID),
		}

	case model.OperationalStatusReport:
		// Same decline rule as ModeChanged: `mode` asserts a specific state, so
		// a mode with no upstream identity cannot be reported honestly.
		mode, ok := controllerModeIdentity(v.Mode)
		if !ok {
			return nil, false, nil
		}
		// ActivePlanID is DROPPED: the wire OperationalStatusReport has no plan
		// field. It is not lost to consumers — plan-applied carries it.
		msg = &scv1.OperationalStatusReport{
			Kind:           scTypes + "sc-mode-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			Mode:           strp(mode),
			FlashActive:    boolp(v.InConflictFlash),
		}

	case model.PreemptionActivated:
		// preempt_number and type have no domain source yet; left unset rather
		// than invented. The adapter that learns them can fill them later
		// without a wire change, since both are additive-optional.
		msg = &scv1.PreemptionActivated{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			SourceId:       strp(v.Source),
		}

	case model.PreemptionCleared:
		msg = &scv1.PreemptionCleared{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
		}

	case model.DetectorReport:
		dets := make([]*scv1.DetectorReportDetector, 0, len(v.Readings))
		for _, r := range v.Readings {
			dets = append(dets, &scv1.DetectorReportDetector{
				DetectorId: u32p(r.Channel),
				Volume:     u32p(r.VolumeDelta),
				Occupancy:  strp(occupancyPercent(r.OccupancyTenths)),
			})
		}
		msg = &scv1.DetectorReport{
			Kind:           scTypes + "sc-detector-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			IntervalStart:  timestamppb.New(v.IntervalStart.UTC()),
			// The domain carries true elapsed time; the wire constrains the
			// interval to whole seconds. Round ONCE, here at the edge, rather
			// than pre-breaking the domain to match the wire.
			IntervalDurationS: u32p(uint32((v.IntervalDuration + 500*time.Millisecond) / time.Second)),
			Detector:          dets,
		}

	case model.CCTVTourStateChanged:
		to, ok := tourRunStateFor(v.To)
		if !ok {
			return nil, false, nil
		}
		// previous_state has no "unknown" either, so a tour whose prior state
		// we never observed encodes as STOPPED. That is only reachable if a
		// differ emitted a transition from an unknown state, which this one
		// does not — new tours stay silent until they have a known prior.
		from, _ := tourRunStateFor(v.From)
		msg = &cctvv1.TourStateChanged{
			Kind:           cctvTypes + "cctv-tour-state-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			TourId:         v.TourID,
			PreviousState:  tourp(from),
			CurrentState:   to,
		}

	case model.ZoneIncidentDetected:
		d := &pcpv1.ZoneIncidentDetected{
			Kind:           perceptionTypes + "pcp-zone-incident-detected",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			IncidentId:     strp(v.IncidentID),
			ZoneId:         strp(v.ZoneID),
			Type:           strp(incidentTypeIdentity(v.Type)),
			Severity:       sevp(incidentSeverityFor(v.Severity)),
			ObjectClass:    strp(objectClassIdentity(v.ObjectClass)),
			Confidence:     u32p(uint32(v.ConfidencePercent)),
			TrackId:        u32p(v.TrackID),
			TrackEpoch:     u32p(v.TrackEpoch),
			FirstObserved:  timestamppb.New(v.OccurredAt.UTC()),
		}
		// Zero is meaningful here — a stopped-vehicle incident IS zero km/h —
		// so an unreported speed stays absent rather than rendering as stopped.
		if v.SpeedReported {
			d.SpeedKmh = strp(hundredths(v.SpeedHundredthsKPH))
		}
		msg = d

	case model.ZoneIncidentUpdated:
		u := &pcpv1.ZoneIncidentUpdated{
			Kind:           perceptionTypes + "pcp-zone-incident-updated",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			IncidentId:     strp(v.IncidentID),
			ZoneId:         strp(v.ZoneID),
			Severity:       sevp(incidentSeverityFor(v.Severity)),
			Confidence:     u32p(uint32(v.ConfidencePercent)),
		}
		if v.SpeedReported {
			u.SpeedKmh = strp(hundredths(v.SpeedHundredthsKPH))
		}
		msg = u

	case model.ZoneIncidentCleared:
		msg = &pcpv1.ZoneIncidentCleared{
			Kind:           perceptionTypes + "pcp-zone-incident-cleared",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			IncidentId:     strp(v.IncidentID),
			ZoneId:         strp(v.ZoneID),
		}

	case model.ZoneIntervalReport:
		zones := make([]*pcpv1.ZoneIntervalReportZone, 0, len(v.Zones))
		for _, z := range v.Zones {
			counts := make([]*pcpv1.ClassCount, 0, len(z.ClassCounts))
			for _, cc := range z.ClassCounts {
				counts = append(counts, &pcpv1.ClassCount{
					Class: strp(objectClassIdentity(cc.Class)), Count: u32p(cc.Count),
				})
			}
			// ObservedCount and OccupancyPercent are deliberately absent:
			// v0.3.0 removed both leaves from this notification and moved
			// them to zone-occupancy-interval-report, which the same poll
			// also emits. The values are not dropped, they are published
			// under the ce-type that now defines them.
			zone := &pcpv1.ZoneIntervalReportZone{
				ZoneId:            strp(z.ZoneID),
				CrossedVolume:     u32p(z.CrossedVolume),
				ClassCount:        counts,
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: u32p(uint32((v.IntervalDuration + 500*time.Millisecond) / time.Second)),
			}
			if z.SpeedReported {
				zone.AverageSpeedKmh = strp(hundredths(z.SpeedAvgHundredthsKPH))
			}
			zones = append(zones, zone)
		}
		msg = &pcpv1.ZoneIntervalReport{
			Kind:           perceptionTypes + "pcp-zone-interval-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			Zone:           zones,
		}

	case model.ZoneOccupancyIntervalReport:
		zones := make([]*zocv1.Zone, 0, len(v.Zones))
		for _, z := range v.Zones {
			classes := make([]*zocv1.ObservedClass, 0, len(z.ClassCounts))
			for _, cc := range z.ClassCounts {
				// MeanConfidence is left unset: the domain carries no
				// per-class confidence, and zero is the wire's "not stated"
				// for a uint32 count of confidence, not a claim of none.
				classes = append(classes, &zocv1.ObservedClass{
					Class: strp(objectClassIdentity(cc.Class)), Count: u32p(cc.Count),
				})
			}
			// PeakOccupancyCount is left unset for the same reason: no
			// adapter observes a within-interval peak today, and inventing
			// one from the mean would be a wrong value rather than a missing
			// one.
			zones = append(zones, &zocv1.Zone{
				ZoneId:            strp(z.ZoneID),
				ObservedCount:     u32p(z.ObservedCount),
				OccupancyPercent:  strp(occupancyPercent(z.OccupancyTenths)),
				ObservedClass:     classes,
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: u32p(uint32((v.IntervalDuration + 500*time.Millisecond) / time.Second)),
			})
		}
		msg = &zocv1.ZoneOccupancyIntervalReport{
			Kind:           zoneOccTypes + "zoc-zone-occupancy-interval-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			Zone:           zones,
		}

	case model.TrafficIntervalReport:
		lanes := make([]*tsv1.TrafficIntervalReportLane, 0, len(v.Lanes))
		for _, l := range v.Lanes {
			classes := make([]*tsv1.TrafficIntervalReportLaneClassVolume, 0, len(l.ClassVolumes))
			for _, cv := range l.ClassVolumes {
				classes = append(classes, &tsv1.TrafficIntervalReportLaneClassVolume{
					ClassId: u32p(cv.ClassID), Volume: u32p(cv.Volume),
				})
			}
			lane := &tsv1.TrafficIntervalReportLane{
				LaneId:      u32p(l.LaneID),
				Volume:      u32p(l.Volume),
				Occupancy:   strp(occupancyPercent(l.OccupancyTenths)),
				ClassVolume: classes,
				DataQuality: qualp(dataQualityFor(l.Quality)),
				// The interval belongs to the DEVICE, and the wire repeats it
				// per lane. Both come from the report, not from poll timing.
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: u32p(uint32((v.IntervalDuration + 500*time.Millisecond) / time.Second)),
			}
			// Left EMPTY when the sensor did not report speed. Rendering
			// "0.00" would turn "we don't know" into "stopped traffic", and a
			// consumer computing travel time cannot tell those apart after
			// the fact.
			if l.SpeedReported {
				lane.SpeedAverageKmh = strp(hundredths(l.SpeedAvgHundredthsKPH))
			}
			lanes = append(lanes, lane)
		}
		msg = &tsv1.TrafficIntervalReport{
			Kind:           trafficSenTypes + "ts-traffic-interval-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			Lane:           lanes,
		}

	case model.DMSMessageActivationFailed:
		errID := errorTypeFor(v.Error)
		failed := &dmsv1.MessageActivationFailed{
			Kind:                dmsTypes + "dms-message-activation-failed",
			SourceDeviceId:      v.DeviceID,
			OccurredAt:          timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:          e.observedBy(),
			Sequence:            e.nextSequence(v.DeviceID),
			SequenceEpoch:       e.sequenceEpoch,
			AttemptedMemoryType: memp(memoryTypeFor(v.MemoryType)),
			AttemptedSlotNumber: u32p(v.Slot),
			ErrorType:           strp(errID),
		}
		if errID != "" {
			failed.ErrorPosition = u32p(v.ErrorPosition)
		}
		msg = failed

	case model.DMSMessageChanged:
		mc := &dmsv1.MessageChanged{
			Kind:           dmsTypes + "dms-message-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			MemoryType:     memp(memoryTypeFor(v.ToMemoryType)),
			SlotNumber:     u32p(v.ToSlot),
			MultiString:    strp(v.ToText),
			Crc:            u32p(v.ToCRC),
			// FromCRC is DROPPED: the wire carries no prior-crc leaf. The
			// prior message is identified to consumers by (bank, slot, text);
			// an in-place rewrite still reports because the CURRENT crc moved.
			PriorMemoryType:  memp(memoryTypeFor(v.FromMemoryType)),
			PriorSlotNumber:  u32p(v.FromSlot),
			PriorMultiString: strp(v.FromText),
		}
		// activation-trigger is optional: a trigger with no upstream identity
		// (unknown, reset, vendor-specific) leaves the leaf unset rather than
		// declining the event or asserting a near-neighbour cause.
		if trig, ok := activationTriggerIdentity(v.Trigger); ok {
			mc.ActivationTrigger = strp(trig)
		}
		msg = mc

	case model.DMSSignStatusReport:
		// display-state and its brightness sibling live on the dms-status
		// facet; this report is built from the environment facet alone, so
		// display-state stays unset (optional upstream) rather than being
		// fetched across facets the differ cannot see.
		//
		// Per-sensor absence: a sensor the device did not answer for stays
		// unset. Optional proto presence now carries a reported false or
		// zero, so those are encoded when the sensor answered.
		r := &dmsv1.SignStatusReport{
			Kind:           dmsTypes + "dms-sign-status-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
		}
		if v.BrightnessReported {
			r.BrightnessCurrent = u32p(uint32(v.BrightnessPercent))
		}
		if v.AmbientLightReported {
			r.AmbientLightLevel = u32p(uint32(v.AmbientLightPercent))
		}
		if v.IlluminanceReported {
			r.AmbientIlluminanceLux = u32p(v.IlluminanceLux)
		}
		if v.CabinetTempReported {
			r.CabinetTemperatureC = strp(deciCelsius(v.CabinetTempDeciC))
		}
		if v.FaceTempReported {
			r.SignFaceTemperatureC = strp(deciCelsius(v.FaceTempDeciC))
		}
		if v.HumidityReported {
			r.HumidityPercent = u32p(uint32(v.HumidityPercent))
		}
		if v.DoorReported {
			r.DoorOpen = boolp(v.DoorOpen)
		}
		if v.FanReported {
			r.FanActive = boolp(v.FanActive)
		}
		if v.HeaterReported {
			r.HeaterActive = boolp(v.HeaterActive)
		}
		msg = r

	case model.GateStatusChanged:
		prevPos := gatePositionFor(v.PreviousPosition)
		msg = &rlv1.GateStatusChanged{
			Kind:           rlTypes + "rl-gate-status-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.observedBy(),
			Sequence:       e.nextSequence(v.DeviceID),
			SequenceEpoch:  e.sequenceEpoch,
			// The state list names this instant observed-at (renamed from
			// measured-at). The notification keeps the event-header name
			// occurred-at; a twin copies it onto the gate reading.
			GateId:                v.GateID,
			Position:              gatePositionFor(v.Position),
			PreviousPosition:      &prevPos,
			OperatingMode:         strp(gateOperatingModeFor(v.Mode)),
			PreviousOperatingMode: strp(gateOperatingModeFor(v.PreviousMode)),
		}

	default:
		return nil, false, nil
	}

	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return nil, false, fmt.Errorf("openits encode %s: %w", ceType, err)
	}
	identity, err := identityBytes(msg)
	if err != nil {
		return nil, false, fmt.Errorf("openits identity %s: %w", ceType, err)
	}
	return &wire.Encoded{
		CEType:      ceType,
		ContentType: contentType,
		Data:        data,
		DataSchema:  dataSchemaFor[ceType],
		Identity:    identity,
	}, true, nil
}

// severityFor maps the domain severity to the wire enum. Written as an
// explicit switch, not a numeric cast: the values happen to line up 1:1 today
// (commonv1 has no UNSPECIFIED, so INFO=0..CRITICAL=4 matches the domain), but
// a cast would silently follow any upstream renumbering, which is exactly the
// churn ADR 0002 exists to contain.
func severityFor(s model.FaultSeverity) commonv1.FaultSeverity {
	switch s {
	case model.SeverityWarning:
		return commonv1.FaultSeverity_FAULT_SEVERITY_WARNING
	case model.SeverityMinor:
		return commonv1.FaultSeverity_FAULT_SEVERITY_MINOR
	case model.SeverityMajor:
		return commonv1.FaultSeverity_FAULT_SEVERITY_MAJOR
	case model.SeverityCritical:
		return commonv1.FaultSeverity_FAULT_SEVERITY_CRITICAL
	default:
		return commonv1.FaultSeverity_FAULT_SEVERITY_INFO
	}
}

// modeChanged builds the shared common/v1.ModeChanged. Three domain events map
// onto it — controller mode, DMS control mode, DMS display state — differing
// only in `kind` and which identity set prior/current are drawn from.
func (e *emitter) modeChanged(b model.Base, kind, prior, current string) *commonv1.ModeChanged {
	msg := &commonv1.ModeChanged{
		Kind:           kind,
		SourceDeviceId: b.DeviceID,
		OccurredAt:     timestamppb.New(b.OccurredAt.UTC()),
		ObservedBy:     e.observedBy(),
		Sequence:       e.nextSequence(b.DeviceID),
		SequenceEpoch:  e.sequenceEpoch,
		Mode:           current,
	}
	// previous-mode is optional. An unmappable prior stays absent.
	if prior != "" {
		msg.PreviousMode = strp(prior)
	}
	return msg
}

func (e *emitter) observedBy() *string { return strp(e.collectorID) }

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func u32p(v uint32) *uint32 { return &v }

func boolp(v bool) *bool { return &v }

func memp(m dmsv1.MessageMemoryType) *dmsv1.MessageMemoryType { return &m }

func tourp(s cctvv1.TourRunState) *cctvv1.TourRunState { return &s }

func sevp(s pcpv1.IncidentSeverity) *pcpv1.IncidentSeverity { return &s }

func qualp(q tsv1.DataQuality) *tsv1.DataQuality { return &q }

func whenFirst(explicit, occurred time.Time) *timestamppb.Timestamp {
	if explicit.IsZero() {
		explicit = occurred
	}
	return timestamppb.New(explicit.UTC())
}

// producerAssigned names the event-header leaves that describe the OBSERVATION
// rather than the occurrence, and so must not feed the deterministic ce-id.
var producerAssigned = []string{"sequence", "sequence_epoch", "observed_by"}

// identityBytes marshals msg with the producer-assigned leaves cleared.
//
// Done generically via protoreflect rather than as a per-message type switch.
// That is a deliberate exception to this package's no-reflection rule, which
// exists to keep the domain-to-wire MAPPING dumb and reviewable. This is not a
// mapping — it is one projection applied uniformly to every payload, and a
// type switch would be a maintenance trap: adding a message type and
// forgetting its case would silently break restart-invariance, with no failing
// mapping to notice and nothing wrong-looking in the emitted bytes.
func identityBytes(msg proto.Message) ([]byte, error) {
	clone := proto.Clone(msg)
	fields := clone.ProtoReflect().Descriptor().Fields()
	for _, name := range producerAssigned {
		if fd := fields.ByName(protoreflect.Name(name)); fd != nil {
			clone.ProtoReflect().Clear(fd)
		}
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(clone)
}
