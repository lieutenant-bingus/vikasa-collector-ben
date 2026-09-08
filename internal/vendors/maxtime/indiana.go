package maxtime

import (
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// indianaPhase maps ATSPM Indiana EventTypeID values to domain phase codes.
// Codes absent here are not guessed — they become UnmappedControllerLogEvent.
var indianaPhase = map[uint32]model.PhaseLogCode{
	0:  model.PhaseLogOn,
	1:  model.PhaseLogBeginGreen,
	2:  model.PhaseLogCheck,
	3:  model.PhaseLogMinComplete,
	4:  model.PhaseLogGapOut,
	5:  model.PhaseLogMaxOut,
	6:  model.PhaseLogForceOff,
	7:  model.PhaseLogGreenTermination,
	8:  model.PhaseLogBeginYellow,
	9:  model.PhaseLogEndYellow,
	10: model.PhaseLogBeginRedClearance,
	11: model.PhaseLogEndRedClearance,
	12: model.PhaseLogInactive,
	14: model.PhaseLogSkipped,
	41: model.PhaseLogHoldActive,
	42: model.PhaseLogHoldReleased,
	43: model.PhaseLogCallRegistered,
	44: model.PhaseLogCallDropped,
}

var indianaOverlap = map[uint32]model.OverlapLogCode{
	61: model.OverlapLogBeginGreen,
	62: model.OverlapLogBeginTrailGreen,
	63: model.OverlapLogBeginYellow,
	64: model.OverlapLogBeginRedClear,
	65: model.OverlapLogInactive,
}

func decodeIndiana(logID uint64, code, parameter uint32, at model.Base) model.Event {
	if phase, ok := indianaPhase[code]; ok {
		return model.PhaseLogEvent{
			Base: at, LogID: logID, PhaseNumber: parameter, Code: phase,
		}
	}
	if overlap, ok := indianaOverlap[code]; ok {
		return model.OverlapLogEvent{
			Base: at, LogID: logID, OverlapNumber: parameter, Code: overlap,
		}
	}
	switch code {
	case 81: // vehicle-detector-off
		return model.DetectorTransition{
			Base: at, Channel: parameter, Kind: model.DetectorTransitionCallOff,
		}
	case 82: // vehicle-detector-on
		return model.DetectorTransition{
			Base: at, Channel: parameter, Kind: model.DetectorTransitionCallOn,
		}
	}
	return model.UnmappedControllerLogEvent{
		Base: at, LogID: logID, RawCode: code, Parameter: parameter,
	}
}
