// Package openits maps collector domain events to openits-models wire
// payloads. It is one of the two emitter families described in ADR 0002,
// and the only place in the collector that imports openits-models.
package openits

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

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
}

func (e *emitter) CETypes() []string { return nil }

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
		msg = &commonv1.ModeChanged{
			Kind:           scTypes + "sc-mode-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			Prior:          prior,
			Current:        current,
		}
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
	default:
		return nil, false, nil
	}

	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return nil, false, fmt.Errorf("openits encode %s: %w", ceType, err)
	}
	return &wire.Encoded{CEType: ceType, ContentType: contentType, Data: data}, true, nil
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
