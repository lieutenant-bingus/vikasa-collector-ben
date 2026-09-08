package model

// KindPhaseStatuses is per-phase (or per-output-channel) signal state at one poll.
const KindPhaseStatuses Kind = "phase-statuses"

// SignalState is the movement state of one signal output as reported by the
// controller. Values follow MAXTIME/NTCIP-style signal-state enumerations at
// the precision the device returns; the wire phase-state-change event uses a
// separate Indiana identity taxonomy mapped in internal/wire.
type SignalState uint8

const (
	SignalStateUnknown SignalState = iota
	SignalStateDark
	SignalStateStop
	SignalStatePreMovement
	SignalStatePermissiveMovement
	SignalStateProtectedMovement
	SignalStateClearance
)

func (s SignalState) String() string {
	switch s {
	case SignalStateDark:
		return "dark"
	case SignalStateStop:
		return "stop"
	case SignalStatePreMovement:
		return "pre-movement"
	case SignalStatePermissiveMovement:
		return "permissive-movement"
	case SignalStateProtectedMovement:
		return "protected-movement"
	case SignalStateClearance:
		return "clearance"
	default:
		return "unknown"
	}
}

// PhaseStatus is one phase or signal-output channel at a poll.
type PhaseStatus struct {
	PhaseNumber uint32
	State       SignalState
}

// PhaseStatuses holds one entry per answering phase, sorted by PhaseNumber.
// An empty slice means the controller reported no phase table, not a read
// failure — that is a FacetError on this kind instead.
type PhaseStatuses struct{ Phases []PhaseStatus }

func (PhaseStatuses) FacetKind() Kind { return KindPhaseStatuses }
