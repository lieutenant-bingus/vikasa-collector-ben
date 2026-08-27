package model

// KindDMSStatus is a dynamic message sign's operational state at one poll.
const KindDMSStatus Kind = "dms-status"

// DMSControlMode is WHO is driving the sign. It is independent of what the
// sign is displaying (DMSDisplayState) — a sign under central control can be
// blank, and a locally-controlled sign can show a message. NTCIP 1203 reports
// them as separate objects and the catalog models them as separate mode axes;
// conflating them would lose information the sign genuinely provides.
type DMSControlMode uint8

const (
	ControlUnknown         DMSControlMode = iota
	ControlLocal                          // a technician at the sign; central commands are not honoured
	ControlExternal                       // an external system, not the TMC
	ControlCentral                        // the TMC / central system
	ControlCentralOverride                // central has overridden local control
	ControlSimulation                     // off-line test control
	ControlOther                          // vendor-specific
)

func (m DMSControlMode) String() string {
	switch m {
	case ControlLocal:
		return "local"
	case ControlExternal:
		return "external"
	case ControlCentral:
		return "central"
	case ControlCentralOverride:
		return "central-override"
	case ControlSimulation:
		return "simulation"
	case ControlOther:
		return "other"
	default:
		return "unknown"
	}
}

// DMSDisplayState is what the sign is doing with its display.
type DMSDisplayState uint8

const (
	DisplayUnknown DMSDisplayState = iota
	DisplayOff
	DisplayBlank
	DisplayTest
	DisplayNormal
)

func (s DMSDisplayState) String() string {
	switch s {
	case DisplayOff:
		return "off"
	case DisplayBlank:
		return "blank"
	case DisplayTest:
		return "test"
	case DisplayNormal:
		return "normal"
	default:
		return "unknown"
	}
}

// MessageMemoryType is which memory bank the active message lives in.
type MessageMemoryType uint8

const (
	MemoryUnknown MessageMemoryType = iota
	MemoryPermanent
	MemoryChangeable
	MemoryVolatile
	MemorySchedule
	MemoryBlank
)

func (m MessageMemoryType) String() string {
	switch m {
	case MemoryPermanent:
		return "permanent"
	case MemoryChangeable:
		return "changeable"
	case MemoryVolatile:
		return "volatile"
	case MemorySchedule:
		return "schedule"
	case MemoryBlank:
		return "blank"
	default:
		return "unknown"
	}
}

// MessageStatus is the health of the active message slot.
type MessageStatus uint8

const (
	StatusUnknown MessageStatus = iota
	StatusNotUsed
	StatusModifying
	StatusValidating
	StatusValid
	StatusError
)

func (s MessageStatus) String() string {
	switch s {
	case StatusNotUsed:
		return "not-used"
	case StatusModifying:
		return "modifying"
	case StatusValidating:
		return "validating"
	case StatusValid:
		return "valid"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

// MultiSyntaxError is why a MULTI message failed to activate.
//
// Note the zero value is None, NOT Syntax. The catalog's ErrorType puts
// SYNTAX at 0 with no unspecified variant; mirroring that numbering here
// would make a sign that simply does not answer the object report a genuine
// syntax error. Mapping ours onto theirs is the emitter's problem — one
// table — and is cheaper than fabricating errors at the collection boundary.
type MultiSyntaxError uint8

const (
	SyntaxErrorNone MultiSyntaxError = iota
	SyntaxErrorSyntax
	SyntaxErrorUnsupportedTag
	SyntaxErrorFontNotFound
	SyntaxErrorGraphicNotFound
	SyntaxErrorTooLong
	SyntaxErrorHardware
	SyntaxErrorOther
)

func (e MultiSyntaxError) String() string {
	switch e {
	case SyntaxErrorSyntax:
		return "syntax"
	case SyntaxErrorUnsupportedTag:
		return "unsupported-tag"
	case SyntaxErrorFontNotFound:
		return "font-not-found"
	case SyntaxErrorGraphicNotFound:
		return "graphic-not-found"
	case SyntaxErrorTooLong:
		return "too-long"
	case SyntaxErrorHardware:
		return "hardware"
	case SyntaxErrorOther:
		return "other"
	default:
		// Zero renders as "none" for the same reason the type's zero value is
		// None and not Syntax (see the type doc): an unanswered object must
		// not fabricate an error. That reasoning does NOT extend to values
		// above the mapped range — a value this package does not recognize is
		// not "no error", it's an unmapped one. Adapters must map any vendor
		// value they don't recognize to SyntaxErrorOther, not leave it as-is;
		// otherwise it silently reports here as "none" and reads downstream
		// as "the sign is fine."
		return "none"
	}
}

// DMSActivationTrigger is WHY the active message is on the face. NTCIP 1203
// conflates two orthogonal axes into one dmsMsgSourceMode enumeration: WHICH
// AUTHORITY is driving the sign (local / central / external — that axis is
// DMSControlMode) and WHY the current message got there (schedule, comm-loss
// fallback, power recovery, duration expiry). An adapter splits the object
// across the two fields; this type is the WHY half. The upstream state model
// draws the same line at sign/control/state/active/activation-trigger.
//
// The zero value is Unknown, not Command: a sign that does not answer the
// source object must not read as "an operator commanded this."
type DMSActivationTrigger uint8

const (
	TriggerUnknown       DMSActivationTrigger = iota
	TriggerCommand                            // an operator / central / external command activated it
	TriggerSchedule                           // the sign's time-based scheduler
	TriggerCommLoss                           // the sign entered its comm-loss fallback
	TriggerPowerRecovery                      // the sign entered a power-recovery fallback
	TriggerPowerLoss                          // legacy power-loss reporting, kept distinct from PowerRecovery
	TriggerReset                              // the sign entered its reset fallback
	TriggerEndOfDuration                      // the previous activation's duration expired
	TriggerOther                              // vendor-specific
)

func (t DMSActivationTrigger) String() string {
	switch t {
	case TriggerCommand:
		return "command"
	case TriggerSchedule:
		return "schedule"
	case TriggerCommLoss:
		return "comm-loss"
	case TriggerPowerRecovery:
		return "power-recovery"
	case TriggerPowerLoss:
		return "power-loss"
	case TriggerReset:
		return "reset"
	case TriggerEndOfDuration:
		return "end-of-duration"
	case TriggerOther:
		return "other"
	default:
		return "unknown"
	}
}

// DMSStatus is what a sign reports about what it is doing: who is driving
// it, what the face shows, and which stored message is on it. Brightness and
// the environment sensor cluster are a separate facet (DMSEnvironment) with
// a separate read path; pixel/lamp diagnostics are modeled upstream but
// nothing has asked for them, so they are omitted until something does
// (adding them is additive).
type DMSStatus struct {
	ControlMode      DMSControlMode
	DisplayState     DMSDisplayState
	ActiveMemoryType MessageMemoryType
	ActiveSlot       uint32
	MessageStatus    MessageStatus
	SyntaxError      MultiSyntaxError // meaningful only when MessageStatus == StatusError
	SyntaxErrorPos   uint32           // character offset into the MULTI string

	// MessageText is the active message's MULTI string, verbatim at the
	// device's own encoding. Empty means a blank face (ActiveMemoryType ==
	// MemoryBlank), never "could not read" — an adapter that cannot read the
	// active message's text fails the facet instead of leaving this empty.
	MessageText string
	// MessageCRC is the sign's CRC-16 of the active message (NTCIP 1203
	// dmsMessageCRC), carried in a uint32; a conforming sign never reports
	// above 65535. It changes when a slot's content is rewritten in place,
	// which the (memory-type, slot) pair alone cannot show.
	MessageCRC uint32
	// ActivationTrigger is why the active message is on the face.
	ActivationTrigger DMSActivationTrigger
}

func (DMSStatus) FacetKind() Kind { return KindDMSStatus }
