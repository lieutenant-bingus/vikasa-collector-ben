package model

// PhaseLogCode is an Indiana high-resolution phase event code the domain
// understands. Unknown controller codes stay on UnmappedControllerLogEvent
// rather than being forced into a neighbour.
type PhaseLogCode uint16

const (
	PhaseLogUnknown           PhaseLogCode = iota
	PhaseLogOn                             // Indiana 0
	PhaseLogBeginGreen                     // 1
	PhaseLogCheck                          // 2
	PhaseLogMinComplete                    // 3
	PhaseLogGapOut                         // 4
	PhaseLogMaxOut                         // 5
	PhaseLogForceOff                       // 6
	PhaseLogGreenTermination               // 7
	PhaseLogBeginYellow                    // 8
	PhaseLogEndYellow                      // 9
	PhaseLogBeginRedClearance              // 10
	PhaseLogEndRedClearance                // 11
	PhaseLogInactive                       // 12
	PhaseLogSkipped                        // 14
	PhaseLogHoldActive                     // 41
	PhaseLogHoldReleased                   // 42
	PhaseLogCallRegistered                 // 43
	PhaseLogCallDropped                    // 44
)

func (c PhaseLogCode) String() string {
	switch c {
	case PhaseLogOn:
		return "phase-on"
	case PhaseLogBeginGreen:
		return "phase-begin-green"
	case PhaseLogCheck:
		return "phase-check"
	case PhaseLogMinComplete:
		return "phase-min-complete"
	case PhaseLogGapOut:
		return "phase-gap-out"
	case PhaseLogMaxOut:
		return "phase-max-out"
	case PhaseLogForceOff:
		return "phase-force-off"
	case PhaseLogGreenTermination:
		return "phase-green-termination"
	case PhaseLogBeginYellow:
		return "phase-begin-yellow"
	case PhaseLogEndYellow:
		return "phase-end-yellow"
	case PhaseLogBeginRedClearance:
		return "phase-begin-red-clearance"
	case PhaseLogEndRedClearance:
		return "phase-end-red-clearance"
	case PhaseLogInactive:
		return "phase-inactive"
	case PhaseLogSkipped:
		return "phase-skipped"
	case PhaseLogHoldActive:
		return "phase-hold-active"
	case PhaseLogHoldReleased:
		return "phase-hold-released"
	case PhaseLogCallRegistered:
		return "phase-call-registered"
	case PhaseLogCallDropped:
		return "phase-call-dropped"
	default:
		return "unknown"
	}
}

// OverlapLogCode is an Indiana overlap event code.
type OverlapLogCode uint16

const (
	OverlapLogUnknown         OverlapLogCode = iota
	OverlapLogBeginGreen                     // 61
	OverlapLogBeginTrailGreen                // 62
	OverlapLogBeginYellow                    // 63
	OverlapLogBeginRedClear                  // 64
	OverlapLogInactive                       // 65
)

func (c OverlapLogCode) String() string {
	switch c {
	case OverlapLogBeginGreen:
		return "overlap-begin-green"
	case OverlapLogBeginTrailGreen:
		return "overlap-begin-trail-green"
	case OverlapLogBeginYellow:
		return "overlap-begin-yellow"
	case OverlapLogBeginRedClear:
		return "overlap-begin-red-clearance"
	case OverlapLogInactive:
		return "overlap-inactive"
	default:
		return "unknown"
	}
}
