package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeSignalControl builds the payload for a signal-control event.
func (e *emitter) encodeSignalControl(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.HighResEvent:
		return e.encodeHighRes(v)

	case model.ModeChanged:
		// Coordination mode (ADR 0024). `mode` is mandatory, so a move to an
		// unknown mode is not claimed; the differ does not emit one anyway.
		// `previous-mode` is optional, so an unknown From is simply absent.
		current, ok := controllerModeIdentity(v.To)
		if !ok {
			return nil, false
		}
		prior, _ := controllerModeIdentity(v.From)
		msg = e.modeChanged(v.Base, scTypes+"sc-mode-event-kind", prior, current)

	case model.CabinetOutputChanged:
		to, ok := cabinetOutputFor(v.To)
		if !ok {
			return nil, false
		}
		co := &scv1.CabinetOutputChanged{
			Kind:           scTypes + "sc-cabinet-output-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			CabinetOutput:  to,
		}
		if from, ok := cabinetOutputFor(v.From); ok {
			co.PreviousCabinetOutput = from.Enum()
		}
		if cause, ok := flashCauseIdentity(v.Cause); ok && v.To == model.OutputFlash {
			co.FlashCause = proto.String(cause)
		}
		msg = co

	case model.PlanChanged:
		// PlanApplied names only the plan now in force. FromPlanID is DROPPED:
		// there is no wire field for it, and the transition is recoverable from
		// consecutive events. Recorded here as the map-or-drop decision it is.
		msg = &scv1.PlanApplied{
			Kind:           scTypes + "sc-plan-applied",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			PlanId:         proto.Uint32(v.ToPlanID),
		}

	case model.OperationalStatusReport:
		// Rebuilt on the shared status summary in openits-models v0.8.0. The
		// report carries what the domain knows; oper-status, monitor status,
		// control source, uptime and the -since times are left absent until
		// an adapter reads them, rather than guessed (ADR 0024).
		// ActivePlanID is DROPPED: the report has no plan leaf, and
		// plan-applied carries it.
		r := &scv1.OperationalStatusReport{
			Kind:           scTypes + "sc-operational-status-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			PreemptActive:  proto.Bool(v.PreemptionActive),
		}
		if mode, ok := controllerModeIdentity(v.Mode); ok {
			r.Mode = proto.String(mode)
		}
		if out, ok := cabinetOutputFor(v.CabinetOutput); ok {
			r.CabinetOutput = out.Enum()
		}
		if cause, ok := flashCauseIdentity(v.FlashCause); ok && v.CabinetOutput == model.OutputFlash {
			r.FlashCause = proto.String(cause)
		}
		msg = r

	case model.PreemptionActivated:
		// preempt_number and type have no domain source yet; left unset rather
		// than invented. The adapter that learns them can fill them later
		// without a wire change, since both are additive-optional.
		msg = &scv1.PreemptionActivated{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			SourceId:       optString(v.Source),
		}

	case model.PreemptionCleared:
		msg = &scv1.PreemptionCleared{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
		}

	case model.DetectorReport:
		dets := make([]*scv1.DetectorReportDetector, 0, len(v.Readings))
		for _, r := range v.Readings {
			dets = append(dets, &scv1.DetectorReportDetector{
				DetectorId: proto.Uint32(r.Channel),
				Volume:     proto.Uint32(r.VolumeDelta),
				Occupancy:  proto.String(occupancyPercent(r.OccupancyTenths)),
			})
		}
		msg = &scv1.DetectorReport{
			Kind:           scTypes + "sc-detector-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			IntervalStart:  timestamppb.New(v.IntervalStart.UTC()),
			// The domain carries true elapsed time; the wire constrains the
			// interval to whole seconds. Round ONCE, here at the edge, rather
			// than pre-breaking the domain to match the wire.
			IntervalDurationS: proto.Uint32(wholeSeconds(v.IntervalDuration)),
			Detector:          dets,
		}
	default:
		return nil, false
	}
	return msg, true
}
