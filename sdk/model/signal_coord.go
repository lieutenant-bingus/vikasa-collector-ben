package model

// KindCoordinationStatus is timing/coordination scalars at one poll.
const KindCoordinationStatus Kind = "coordination-status"

// CoordinationStatus carries coordination-relevant scalars the adapter could
// read this poll. Values are stored at device precision; the wire
// coordination-change event names which axis moved via a separate kind
// identity mapped in internal/wire.
type CoordinationStatus struct {
	OperationalMode  int64 // MAXTIME CoordOpp
	SelectedPattern  int64 // MAXTIME CoordSel
	ActualOffset     int64 // MAXTIME actualOffset
	CycleLength      int64 // MAXTIME coordCycleLengthStatus
	CoordinationMode int64 // MAXTIME coordCoordinationModeStatus
}

func (CoordinationStatus) FacetKind() Kind { return KindCoordinationStatus }

// CoordinationAxis names which scalar changed between polls.
type CoordinationAxis uint8

const (
	CoordAxisUnknown CoordinationAxis = iota
	CoordAxisOperationalMode
	CoordAxisSelectedPattern
	CoordAxisActualOffset
	CoordAxisCycleLength
	CoordAxisCoordinationMode
)

func (a CoordinationAxis) String() string {
	switch a {
	case CoordAxisOperationalMode:
		return "operational-mode"
	case CoordAxisSelectedPattern:
		return "selected-pattern"
	case CoordAxisActualOffset:
		return "actual-offset"
	case CoordAxisCycleLength:
		return "cycle-length"
	case CoordAxisCoordinationMode:
		return "coordination-mode"
	default:
		return "unknown"
	}
}
