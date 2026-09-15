package model

// KindSignalIndications is per-channel color indication at one poll.
const KindSignalIndications Kind = "signal-indications"

// SignalColor is the illuminated indication on one output channel.
type SignalColor uint8

const (
	SignalColorUnknown SignalColor = iota
	SignalColorOff
	SignalColorRed
	SignalColorYellow
	SignalColorGreen
)

func (c SignalColor) String() string {
	switch c {
	case SignalColorOff:
		return "off"
	case SignalColorRed:
		return "red"
	case SignalColorYellow:
		return "yellow"
	case SignalColorGreen:
		return "green"
	default:
		return "unknown"
	}
}

// ChannelIndication is one output channel's displayed color.
type ChannelIndication struct {
	Channel uint32
	Color   SignalColor
}

// SignalIndications holds one entry per channel the adapter read, sorted by
// Channel. Synth diffs this into SignalIndicationChanged events.
type SignalIndications struct{ Channels []ChannelIndication }

func (SignalIndications) FacetKind() Kind { return KindSignalIndications }

// KindSiteInventory is ASC/CV site labels and optional J2735 MAP/SPaT hex.
const KindSiteInventory Kind = "site-inventory"

// PhaseApproach is one NEMA phase → approach label.
type PhaseApproach struct {
	Phase    uint32
	Approach string
}

// SiteInventory is intersection geography/labels for ATSPM enrichment.
// PhaseApproaches is sorted by Phase.
type SiteInventory struct {
	MainStreet     string
	SecondStreet   string
	Description    string
	LatitudeE7     int64
	LongitudeE7    int64
	MapHex         string
	SpatHex        string
	MapMsgID       uint32
	SpatMsgID      uint32
	PhaseApproaches []PhaseApproach
}

func (SiteInventory) FacetKind() Kind { return KindSiteInventory }
