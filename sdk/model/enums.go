package model

// ControllerMode is the collector-owned controller mode enum. Wire enums
// live in wire versions; this one never changes because upstream renames.
type ControllerMode uint8

const (
	ModeUnknown ControllerMode = iota
	ModeNormal
	ModeFlash
	ModeStandby
	ModeOff
)

func (m ControllerMode) String() string {
	switch m {
	case ModeNormal:
		return "normal"
	case ModeFlash:
		return "flash"
	case ModeStandby:
		return "standby"
	case ModeOff:
		return "off"
	default:
		return "unknown"
	}
}

// FaultSeverity is the collector-owned severity enum. Its order mirrors the
// catalog's FAULT_SEVERITY_* (INFO=0..CRITICAL=4) so the wire mapping is a
// straight table — but the type is ours and does not move when upstream
// renumbers, which is the whole point of ADR 0002.
type FaultSeverity uint8

const (
	SeverityInfo FaultSeverity = iota
	SeverityWarning
	SeverityMinor
	SeverityMajor
	SeverityCritical
)

func (s FaultSeverity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityMinor:
		return "minor"
	case SeverityMajor:
		return "major"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// FaultCategory groups faults by cause. The catalog has NO category on its
// fault events — it folded category into a free-form `kind` string, keeping
// Category only on the state Fault message. We keep it anyway: the domain
// model may be richer than any wire version, and mapping-or-dropping it is a
// single emitter's decision (ADR 0002) rather than a collection blocker.
type FaultCategory uint8

const (
	CategoryUnknown FaultCategory = iota
	CategoryConflict
	CategoryCabinet
	CategoryPower
	CategoryCommunication
	CategoryDetector
	CategoryLamp
	CategoryPixel       // dms-fault-pixel
	CategoryController  // dms-fault-controller
	CategoryEnvironment // dms-fault-environment
)

func (c FaultCategory) String() string {
	switch c {
	case CategoryConflict:
		return "conflict"
	case CategoryCabinet:
		return "cabinet"
	case CategoryPower:
		return "power"
	case CategoryCommunication:
		return "communication"
	case CategoryDetector:
		return "detector"
	case CategoryLamp:
		return "lamp"
	case CategoryPixel:
		return "pixel"
	case CategoryController:
		return "controller"
	case CategoryEnvironment:
		return "environment"
	default:
		return "unknown"
	}
}
