// Package openits maps collector domain events to openits-models wire
// payloads. It is one of the two emitter families described in ADR 0002,
// and the only place in the collector that imports openits-models.
package openits

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"
	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	tsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/traffic_sensor/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// contentType is the profile's per-event protobuf encoding.
const contentType = "application/protobuf"

// New returns the openits-models emitter. collectorID identifies this
// collector as the OBSERVER of events it synthesizes from polling.
func New(collectorID string) wire.Emitter { return &emitter{collectorID: collectorID} }

type emitter struct {
	collectorID string

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
	{"fault-raised", "asc"}: "openits.signal-control.fault-raised.v1",
	{"fault-raised", "dms"}: "openits.dms.fault-raised.v1",

	// Both DMS mode axes share ONE ce-type. dms-mode-event-kind is documented
	// as spanning dms-control-mode and the sign-mode display state alike, so
	// the two are discriminated by which identity set prior/current are drawn
	// from, not by separate ce-types.
	{"control-mode-changed", "dms"}:  "openits.dms.mode-changed.v1",
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

	{"traffic-interval-report", "traffic-sensor"}: "openits.traffic-sensor.traffic-interval-report.v1",

	{"plan-changed", "asc"}:              "openits.signal-control.plan-applied.v1",
	{"operational-status-report", "asc"}: "openits.signal-control.operational-status-report.v1",
	{"preemption-activated", "asc"}:      "openits.signal-control.preemption-activated.v1",
	{"preemption-cleared", "asc"}:        "openits.signal-control.preemption-cleared.v1",
	{"detector-report", "asc"}:           "openits.signal-control.detector-report.v1",

	{"message-activation-failed", "dms"}: "openits.dms.message-activation-failed.v1",
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
// Deduping is required, not cosmetic: the two DMS mode axes deliberately share
// one ce-type, so the routing table has more entries than there are ce-types.
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
	case model.DMSControlModeChanged:
		current, ok := dmsControlModeIdentity(v.To)
		if !ok {
			return nil, false, nil
		}
		prior, _ := dmsControlModeIdentity(v.From)
		msg = e.modeChanged(v.Base, dmsTypes+"dms-mode-event-kind", prior, current)

	case model.DMSDisplayStateChanged:
		current, ok := dmsDisplayStateIdentity(v.To)
		if !ok {
			return nil, false, nil
		}
		prior, _ := dmsDisplayStateIdentity(v.From)
		msg = e.modeChanged(v.Base, dmsTypes+"dms-mode-event-kind", prior, current)

	case model.FaultRaised:
		// Unlike modes, an unmappable category does NOT decline the event —
		// faultKindIdentity falls back to the service base identity. It only
		// fails for a device kind this emitter does not serve, which the
		// ce-type lookup above has already excluded.
		kind, ok := faultKindIdentity(v.Category, v.DeviceKind)
		if !ok {
			return nil, false, nil
		}
		msg = &commonv1.FaultRaised{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			FaultId:        v.FaultID,
			Severity:       severityFor(v.Severity),
			Description:    v.Description,
		}
	case model.FaultCleared:
		kind, ok := faultKindIdentity(model.CategoryUnknown, v.DeviceKind)
		if !ok {
			return nil, false, nil
		}
		msg = &commonv1.FaultCleared{
			// A clear carries no category of its own — the domain FaultCleared
			// has only the id, because the differ identifies it by set
			// difference. The service base identity is the honest `kind`:
			// consumers correlate raise and clear on (source-device-id,
			// fault-id), not on kind.
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			FaultId:        v.FaultID,
		}

	case model.PlanChanged:
		// PlanApplied names only the plan now in force. FromPlanID is DROPPED:
		// there is no wire field for it, and the transition is recoverable from
		// consecutive events. Recorded here as the map-or-drop decision it is.
		msg = &scv1.PlanApplied{
			Kind:           scTypes + "sc-coordination-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			PlanId:         v.ToPlanID,
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
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			Mode:           mode,
			FlashActive:    v.InConflictFlash,
		}

	case model.PreemptionActivated:
		// preempt_number and type have no domain source yet; left unset rather
		// than invented. The adapter that learns them can fill them later
		// without a wire change, since both are additive-optional.
		msg = &scv1.PreemptionActivated{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			SourceId:       v.Source,
		}

	case model.PreemptionCleared:
		msg = &scv1.PreemptionCleared{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
		}

	case model.DetectorReport:
		dets := make([]*scv1.DetectorReportDetector, 0, len(v.Readings))
		for _, r := range v.Readings {
			dets = append(dets, &scv1.DetectorReportDetector{
				DetectorId: r.Channel,
				Volume:     r.VolumeDelta,
				Occupancy:  occupancyPercent(r.OccupancyTenths),
			})
		}
		msg = &scv1.DetectorReport{
			Kind:           scTypes + "sc-detector-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			IntervalStart:  timestamppb.New(v.IntervalStart.UTC()),
			// The domain carries true elapsed time; the wire constrains the
			// interval to whole seconds. Round ONCE, here at the edge, rather
			// than pre-breaking the domain to match the wire.
			IntervalDurationS: uint32((v.IntervalDuration + 500*time.Millisecond) / time.Second),
			Detector:          dets,
		}

	case model.TrafficIntervalReport:
		lanes := make([]*tsv1.TrafficIntervalReportLane, 0, len(v.Lanes))
		for _, l := range v.Lanes {
			classes := make([]*tsv1.TrafficIntervalReportLaneClassVolume, 0, len(l.ClassVolumes))
			for _, cv := range l.ClassVolumes {
				classes = append(classes, &tsv1.TrafficIntervalReportLaneClassVolume{
					ClassId: cv.ClassID, Volume: cv.Volume,
				})
			}
			lane := &tsv1.TrafficIntervalReportLane{
				LaneId:      l.LaneID,
				Volume:      l.Volume,
				Occupancy:   occupancyPercent(l.OccupancyTenths),
				ClassVolume: classes,
				DataQuality: dataQualityFor(l.Quality),
				// The interval belongs to the DEVICE, and the wire repeats it
				// per lane. Both come from the report, not from poll timing.
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: uint32((v.IntervalDuration + 500*time.Millisecond) / time.Second),
			}
			// Left EMPTY when the sensor did not report speed. Rendering
			// "0.00" would turn "we don't know" into "stopped traffic", and a
			// consumer computing travel time cannot tell those apart after
			// the fact.
			if l.SpeedReported {
				lane.SpeedAverageKmh = hundredths(l.SpeedAvgHundredthsKPH)
			}
			lanes = append(lanes, lane)
		}
		msg = &tsv1.TrafficIntervalReport{
			Kind:           trafficSenTypes + "ts-traffic-interval-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			Lane:           lanes,
		}

	case model.DMSMessageActivationFailed:
		msg = &dmsv1.MessageActivationFailed{
			Kind:                dmsTypes + "dms-message-activation-failed",
			SourceDeviceId:      v.DeviceID,
			OccurredAt:          timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:          e.collectorID,
			Sequence:            e.nextSequence(v.DeviceID),
			AttemptedMemoryType: memoryTypeFor(v.MemoryType),
			AttemptedSlotNumber: v.Slot,
			ErrorType:           errorTypeFor(v.Error),
			ErrorPosition:       v.ErrorPosition,
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
	return &commonv1.ModeChanged{
		Kind:           kind,
		SourceDeviceId: b.DeviceID,
		OccurredAt:     timestamppb.New(b.OccurredAt.UTC()),
		ObservedBy:     e.collectorID,
		Sequence:       e.nextSequence(b.DeviceID),
		Prior:          prior,
		Current:        current,
	}
}

// producerAssigned names the event-header leaves that describe the OBSERVATION
// rather than the occurrence, and so must not feed the deterministic ce-id.
var producerAssigned = []string{"sequence", "observed_by"}

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
