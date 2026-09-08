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
// Channel. There is no catalog ce-type for raw indication state today, so
// synth does not diff this facet — it is carried for inventory and future
// wire mapping only.
type SignalIndications struct{ Channels []ChannelIndication }

func (SignalIndications) FacetKind() Kind { return KindSignalIndications }
