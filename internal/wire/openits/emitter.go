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

	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// contentType is the profile's per-event protobuf encoding.
const contentType = "application/protobuf"

// ModelVersion is the collector.yaml model_version that selects this emitter.
const ModelVersion = "openits/v1"

// New returns the openits-models emitter. collectorID identifies this
// collector as the OBSERVER of events it synthesizes from polling. epoch is
// the sequence-epoch every event carries: it names this process's sequence
// stream, so it must change whenever the counters below restart — the
// collector passes its boot id.
func New(collectorID, epoch string) wire.Emitter {
	return &emitter{collectorID: collectorID, epoch: epoch}
}

type emitter struct {
	collectorID string
	epoch       string

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
// The counter is in-memory, so it restarts at 1 when the collector does. Each
// restart is a new sequence-epoch (the boot id), which is how a consumer tells
// a restart from loss. That is also why sequence and epoch must not
// participate in the ce-id digest: a restart would otherwise change the id of
// an event whose content never changed.
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

	// DMS mode-changed carries sign mode (display state) only: v0.8.0 bound
	// dms-mode-event-kind to sign-mode. A control-mode change (central to
	// local) has no ce-type until upstream moves it to control-source, so it
	// is deliberately absent here and drops loudly (ADR 0024).
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

	{"zone-incident-detected", "perception"}: "openits.perception.zone-incident-detected.v1",
	{"zone-incident-updated", "perception"}:  "openits.perception.zone-incident-updated.v1",
	{"zone-incident-cleared", "perception"}:  "openits.perception.zone-incident-cleared.v1",
	{"zone-interval-report", "perception"}:   "openits.perception.zone-interval-report.v1",

	// The presence half of the same interval. openits-models v0.3.0 moved
	// observed-count and occupancy-percent off the perception report into
	// their own service, so one poll's interval now routes to two ce-types
	// across two services.
	{"zone-occupancy-interval-report", "perception"}: "openits.zone-occupancy.zone-occupancy-interval-report.v1",

	{"cctv-control-mode-changed", "cctv"}: "openits.cctv.mode-changed.v1",
	{"cctv-tour-state-changed", "cctv"}:   "openits.cctv.tour-state-changed.v1",

	{"cabinet-output-changed", "asc"}:    "openits.signal-control.cabinet-output-changed.v1",
	{"plan-changed", "asc"}:              "openits.signal-control.plan-applied.v1",
	{"operational-status-report", "asc"}: "openits.signal-control.operational-status-report.v1",
	{"preemption-activated", "asc"}:      "openits.signal-control.preemption-activated.v1",
	{"preemption-cleared", "asc"}:        "openits.signal-control.preemption-cleared.v1",
	{"detector-report", "asc"}:           "openits.signal-control.detector-report.v1",

	// High-resolution log rows (ADR 0025), routed by Indiana family.
	{"hr-phase", "asc"}:      "openits.signal-control.phase-state-change.v1",
	{"hr-pedestrian", "asc"}: "openits.signal-control.pedestrian-event.v1",
	{"hr-detector", "asc"}:   "openits.signal-control.detector-transition.v1",
	{"hr-overlap", "asc"}:    "openits.signal-control.overlap-change.v1",
	{"hr-preemption", "asc"}: "openits.signal-control.preemption-event.v1",
	{"hr-unmapped", "asc"}:   "openits.signal-control.unmapped-event.v1",

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

	msg, ok := e.payload(ev)
	if !ok {
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

// payload builds ev's wire message. Each service family has its own
// function; ok is false when no family claims ev, or when the family declines
// it because the value has no honest wire identity.
func (e *emitter) payload(ev model.Event) (proto.Message, bool) {
	for _, family := range []func(model.Event) (proto.Message, bool){
		e.encodeSignalControl, e.encodeDMS, e.encodeShared, e.encodeCCTV,
		e.encodePerception, e.encodeZoneOccupancy, e.encodeTrafficSensor,
	} {
		if msg, ok := family(ev); ok {
			return msg, true
		}
	}
	return nil, false
}

// wholeSeconds rounds an interval to the wire's whole seconds, half up.
func wholeSeconds(d time.Duration) uint32 {
	return uint32((d + 500*time.Millisecond) / time.Second)
}

// optString is presence for an optional text or identity leaf: empty means
// the domain has nothing to report, so the leaf is absent rather than an
// empty string (openits-models v0.6.0 gave every optional scalar explicit
// presence, so absent and zero are different claims on the wire).
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// optTime is presence for an optional timestamp: zero means not known.
func optTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t.UTC())
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
		ObservedBy:     proto.String(e.collectorID),
		SequenceEpoch:  e.epoch,
		Sequence:       e.nextSequence(b.DeviceID),
		PreviousMode:   optString(prior),
		Mode:           current,
	}
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
