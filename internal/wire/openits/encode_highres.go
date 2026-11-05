package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	typesv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/types/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// highResBase is the identity base each domain family's codes must derive
// from in the pinned catalog.
var highResBase = map[model.HighResClass]string{
	model.HighResPhase:        "sc-phase-event-kind",
	model.HighResPedestrian:   "sc-pedestrian-event-kind",
	model.HighResDetector:     "sc-detector-event-kind",
	model.HighResOverlap:      "sc-overlap-event-kind",
	model.HighResPreemption:   "sc-preemption-event-kind",
	model.HighResCoordination: "sc-coordination-event-kind",
}

// cycleStates is Indiana 150's param, in the pinned coordination-cycle-state
// identities (0-4 persistent states, 5-7 instantaneous markers).
var cycleStates = [...]string{
	"cycle-free", "cycle-in-step", "cycle-transition-add", "cycle-transition-subtract",
	"cycle-transition-dwell", "cycle-local-zero", "cycle-begin-pickup", "cycle-master-cycle-zero",
}

// encodeHighRes builds the notification for one high-resolution log row.
// Every encoding carries the row's raw code and param in source (decoder
// "indiana"), so a consumer can always recover exactly what the controller
// logged — including the param of a code whose notification has no leaf for
// it (a pedestrian-overlap row's overlap, a preempt force-off's phase).
func (e *emitter) encodeHighRes(v model.HighResEvent) (proto.Message, bool) {
	src := &typesv1.WireSource{
		Decoder: "indiana",
		Tag: &typesv1.WireSource_Indiana_{Indiana: &typesv1.WireSource_Indiana{
			IndianaCode: proto.Uint32(uint32(v.Code)), IndianaParam: proto.Uint32(uint32(v.Param)),
		}},
	}
	at := timestamppb.New(v.OccurredAt.UTC())
	param := uint32(v.Param)

	class := v.Class()
	if class == model.HighResUnmapped {
		return &scv1.UnmappedEvent{
			Kind: scTypes + "sc-unmapped-event", SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			Source: src,
		}, true
	}
	id, ok := indianaIdentity[v.Code]
	if !ok || id.base != highResBase[class] {
		// The domain routed the code to a family the pinned catalog does not
		// put it in. TestHighResClassesMatchCatalog makes this unreachable;
		// declining here keeps a future mismatch from publishing a wrong kind.
		return nil, false
	}
	kind := scTypes + id.name

	switch class {
	case model.HighResPhase:
		return &scv1.PhaseStateChange{
			Kind: kind, SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			PhaseNumber: param, Source: src,
		}, true
	case model.HighResPedestrian:
		m := &scv1.PedestrianEvent{
			Kind: kind, SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			Source: src,
		}
		switch c := v.Code; {
		case c >= 89 && c <= 92: // ped detector on/off/failed/restored: the param is the detector
			m.DetectorChannel = proto.Uint32(param)
		case c >= 67 && c <= 70: // ped overlap: the param is an overlap, which has no leaf here
		default: // walk, change, don't-walk, dark, call, omit: the param is the phase
			m.PhaseNumber = proto.Uint32(param)
		}
		return m, true
	case model.HighResDetector:
		// phase-served is what the adapter joined from the call-phase
		// assignment in force when the row was logged; the row itself does
		// not say. Absent when the detector called no phase or it is unknown.
		m := &scv1.DetectorTransition{
			Kind: kind, SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			Channel: param, Source: src,
		}
		if v.PhaseServed != 0 {
			m.PhaseServed = proto.Uint32(uint32(v.PhaseServed))
		}
		return m, true
	case model.HighResOverlap:
		return &scv1.OverlapChange{
			Kind: kind, SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			OverlapNumber: param, Source: src,
		}, true
	case model.HighResPreemption:
		m := &scv1.PreemptionEvent{
			Kind: kind, SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			Source: src,
		}
		if v.Code != 116 { // 116's param is the phase forced off, not a preempt
			m.PreemptNumber = proto.Uint32(param)
		}
		return m, true
	case model.HighResCoordination:
		m := &scv1.CoordinationChange{
			Kind: kind, SourceDeviceId: v.DeviceID, OccurredAt: at,
			ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
			Source: src,
		}
		// The log row carries only the new value; previous-* stay absent.
		switch c := v.Code; {
		case c >= 50 && c <= 54, c == 151:
			m.PhaseNumber = proto.Uint32(param)
		case c == 131:
			// 0, 254 and 255 are free, free and flash in NTCIP's pattern
			// space, not plans; the raw param stays in source.
			if param >= 1 && param <= 253 {
				m.Pattern = proto.Uint32(param)
			}
		case c == 132:
			m.CycleLengthS = proto.Uint32(param)
		case c == 133:
			m.OffsetS = proto.Uint32(param)
		case c >= 134 && c <= 149: // split N change: N is the phase, the param its split
			m.PhaseNumber = proto.Uint32(uint32(c) - 133)
			m.SplitS = proto.Uint32(param)
		case c == 150:
			m.CycleState = proto.String(scTypes + cycleStates[param])
		}
		return m, true
	}
	return nil, false
}
