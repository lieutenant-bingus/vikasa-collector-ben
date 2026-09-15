package openits

import (
	typesv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/types/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// phaseLogIdentity maps a domain Indiana phase log code to its upstream
// identityref. ok=false means the wire has no leaf for this code.
func phaseLogIdentity(c model.PhaseLogCode) (string, bool) {
	switch c {
	case model.PhaseLogOn:
		return scTypes + "phase-on", true
	case model.PhaseLogBeginGreen:
		return scTypes + "phase-begin-green", true
	case model.PhaseLogCheck:
		return scTypes + "phase-check", true
	case model.PhaseLogMinComplete:
		return scTypes + "phase-min-complete", true
	case model.PhaseLogGapOut:
		return scTypes + "phase-gap-out", true
	case model.PhaseLogMaxOut:
		return scTypes + "phase-max-out", true
	case model.PhaseLogForceOff:
		return scTypes + "phase-force-off", true
	case model.PhaseLogGreenTermination:
		return scTypes + "phase-green-termination", true
	case model.PhaseLogBeginYellow:
		return scTypes + "phase-begin-yellow", true
	case model.PhaseLogEndYellow:
		return scTypes + "phase-end-yellow", true
	case model.PhaseLogBeginRedClearance:
		return scTypes + "phase-begin-red-clearance", true
	case model.PhaseLogEndRedClearance:
		return scTypes + "phase-end-red-clearance", true
	case model.PhaseLogInactive:
		return scTypes + "phase-inactive", true
	case model.PhaseLogSkipped:
		return scTypes + "phase-skipped", true
	case model.PhaseLogHoldActive:
		return scTypes + "phase-hold-active", true
	case model.PhaseLogHoldReleased:
		return scTypes + "phase-hold-released", true
	case model.PhaseLogCallRegistered:
		return scTypes + "phase-call-registered", true
	case model.PhaseLogCallDropped:
		return scTypes + "phase-call-dropped", true
	default:
		return "", false
	}
}

// phaseLogIndianaCode reverses domain → Indiana EventTypeID for wire-source.
func phaseLogIndianaCode(c model.PhaseLogCode) (uint32, bool) {
	switch c {
	case model.PhaseLogOn:
		return 0, true
	case model.PhaseLogBeginGreen:
		return 1, true
	case model.PhaseLogCheck:
		return 2, true
	case model.PhaseLogMinComplete:
		return 3, true
	case model.PhaseLogGapOut:
		return 4, true
	case model.PhaseLogMaxOut:
		return 5, true
	case model.PhaseLogForceOff:
		return 6, true
	case model.PhaseLogGreenTermination:
		return 7, true
	case model.PhaseLogBeginYellow:
		return 8, true
	case model.PhaseLogEndYellow:
		return 9, true
	case model.PhaseLogBeginRedClearance:
		return 10, true
	case model.PhaseLogEndRedClearance:
		return 11, true
	case model.PhaseLogInactive:
		return 12, true
	case model.PhaseLogSkipped:
		return 14, true
	case model.PhaseLogHoldActive:
		return 41, true
	case model.PhaseLogHoldReleased:
		return 42, true
	case model.PhaseLogCallRegistered:
		return 43, true
	case model.PhaseLogCallDropped:
		return 44, true
	default:
		return 0, false
	}
}

func overlapLogIdentity(c model.OverlapLogCode) (string, bool) {
	switch c {
	case model.OverlapLogBeginGreen:
		return scTypes + "overlap-begin-green", true
	case model.OverlapLogBeginTrailGreen:
		return scTypes + "overlap-begin-trailing-green-extension", true
	case model.OverlapLogBeginYellow:
		return scTypes + "overlap-begin-yellow", true
	case model.OverlapLogBeginRedClear:
		return scTypes + "overlap-begin-red-clearance", true
	case model.OverlapLogInactive:
		return scTypes + "overlap-off", true
	default:
		return "", false
	}
}

func overlapLogIndianaCode(c model.OverlapLogCode) (uint32, bool) {
	switch c {
	case model.OverlapLogBeginGreen:
		return 61, true
	case model.OverlapLogBeginTrailGreen:
		return 62, true
	case model.OverlapLogBeginYellow:
		return 63, true
	case model.OverlapLogBeginRedClear:
		return 64, true
	case model.OverlapLogInactive:
		return 65, true
	default:
		return 0, false
	}
}

// detectorTransitionIdentity maps call and presence edges to detector identities.
func detectorTransitionIdentity(k model.DetectorTransitionKind) (string, bool) {
	switch k {
	case model.DetectorTransitionCallOn:
		return scTypes + "vehicle-detector-on", true
	case model.DetectorTransitionCallOff:
		return scTypes + "vehicle-detector-off", true
	case model.DetectorTransitionPresenceOn:
		return scTypes + "vehicle-detector-presence-on", true
	case model.DetectorTransitionPresenceOff:
		return scTypes + "vehicle-detector-presence-off", true
	default:
		return "", false
	}
}

func indicationColorIdentity(c model.SignalColor) (string, bool) {
	switch c {
	case model.SignalColorOff:
		return scTypes + "indication-color-off", true
	case model.SignalColorRed:
		return scTypes + "indication-color-red", true
	case model.SignalColorYellow:
		return scTypes + "indication-color-yellow", true
	case model.SignalColorGreen:
		return scTypes + "indication-color-green", true
	case model.SignalColorUnknown:
		return scTypes + "indication-color-unknown", true
	default:
		return "", false
	}
}

// coordinationAxisIdentity maps a poll-diffed coordination axis to the nearest
// Indiana coordination-change leaf. ok=false declines the event.
func coordinationAxisIdentity(a model.CoordinationAxis) (string, bool) {
	switch a {
	case model.CoordAxisSelectedPattern, model.CoordAxisOperationalMode:
		return scTypes + "coord-pattern-change", true
	case model.CoordAxisCycleLength:
		return scTypes + "coord-cycle-length-change", true
	case model.CoordAxisActualOffset:
		return scTypes + "coord-offset-change", true
	case model.CoordAxisCoordinationMode:
		return scTypes + "coord-cycle-state-change", true
	default:
		return "", false
	}
}

func indianaWireSource(code, param uint32) *typesv1.WireSource {
	return &typesv1.WireSource{
		Decoder: "indiana",
		Tag: &typesv1.WireSource_Indiana_{
			Indiana: &typesv1.WireSource_Indiana{
				IndianaCode:  code,
				IndianaParam: param,
			},
		},
	}
}
