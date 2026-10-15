package model

// KindGateBank is the set of barrier / warning gates on one access-control
// cabinet (ACS) at a single poll. One facet covers the whole cabinet: the
// PLC is the device, and each gate is a row inside it — same shape as
// detector channels on a signal controller.
const KindGateBank Kind = "gate-bank"

// GateKind is the physical role of a gate leaf.
type GateKind uint8

const (
	GateKindUnknown GateKind = iota
	GateKindWarning          // warning gate (WG)
	GateKindBarrier          // barrier gate (BG)
)

func (k GateKind) String() string {
	switch k {
	case GateKindWarning:
		return "warning"
	case GateKindBarrier:
		return "barrier"
	default:
		return "unknown"
	}
}

// GateMode is the Cameleon / PLC operating mode encoded in DSW bits 0–1.
// Unknown is zero and means the word was not read. Decoding a word never
// produces it: all four 2-bit values are valid modes.
type GateMode uint8

const (
	GateModeUnknown GateMode = iota
	GateModeAuto
	GateModeManual
	GateModeOffline
	// GateModeManualNonInterlocked is appended so the values above stay put.
	GateModeManualNonInterlocked
)

func (m GateMode) String() string {
	switch m {
	case GateModeAuto:
		return "auto"
	case GateModeManual:
		return "manual"
	case GateModeOffline:
		return "offline"
	case GateModeManualNonInterlocked:
		return "manual-non-interlocked"
	default:
		return "unknown"
	}
}

// GatePosition is the motion / rest state encoded in DSW bits 4–6.
// Zero is Unknown: values outside 0–4 clamp here rather than inventing a
// position the PLC did not report.
type GatePosition uint8

const (
	GatePositionUnknown GatePosition = iota
	GatePositionClosing
	GatePositionClosed
	GatePositionOpening
	GatePositionOpened
)

func (p GatePosition) String() string {
	switch p {
	case GatePositionClosing:
		return "closing"
	case GatePositionClosed:
		return "closed"
	case GatePositionOpening:
		return "opening"
	case GatePositionOpened:
		return "opened"
	default:
		return "unknown"
	}
}

// GateReading is one gate's decoded DSW word. Alarm bits that are raised are
// also mirrored into the shared fault-set facet by the adapter; they stay
// here so a consumer of the bank facet can see position and alarms together
// without joining events.
//
// Zero-value semantics: an all-zero GateReading is "not read" (mode unknown,
// position unknown, no alarms). It is never "absent" — absence of a gate
// means the adapter omitted that ID from Gates. Decoding raw word 0 is a
// different value: Table 5 mode 0 is Offline.
type GateReading struct {
	ID       string
	Kind     GateKind
	Mode     GateMode
	Position GatePosition
	// LockOpen / LockClose are guide flags from bits 2–3; interpret cautiously.
	LockOpen  bool
	LockClose bool
	// Alarm bits 10–14. Bit 15 is spare and is not decoded. Bits 7–9
	// (open/close request and a spare) are not alarms.
	FailedClose    bool
	FailedOpen     bool
	Estop          bool
	CommandRefused bool
	InvalidInputs  bool
	// Raw is the verbatim %R word for lossless provenance / fixtures.
	Raw uint16
}

// GateBank is every configured gate on the ACS at one poll. Gates are sorted
// by ID so consecutive snapshots compare deterministically.
type GateBank struct {
	Gates []GateReading
}

func (GateBank) FacetKind() Kind { return KindGateBank }
