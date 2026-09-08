package model

// KindDetectorChannelStatuses is per-channel call and presence bits at one poll.
const KindDetectorChannelStatuses Kind = "detector-channel-statuses"

// DetectorChannelStatus is one detector channel's call/presence state.
type DetectorChannelStatus struct {
	Channel        uint32
	CallActive     bool
	PresenceActive bool
}

// DetectorChannelStatuses holds one entry per configured or observed channel,
// sorted by Channel.
type DetectorChannelStatuses struct{ Channels []DetectorChannelStatus }

func (DetectorChannelStatuses) FacetKind() Kind { return KindDetectorChannelStatuses }

// DetectorTransitionKind names a detector-side edge the differ observed.
type DetectorTransitionKind uint8

const (
	DetectorTransitionUnknown DetectorTransitionKind = iota
	DetectorTransitionCallOn
	DetectorTransitionCallOff
	DetectorTransitionPresenceOn
	DetectorTransitionPresenceOff
)

func (k DetectorTransitionKind) String() string {
	switch k {
	case DetectorTransitionCallOn:
		return "call-on"
	case DetectorTransitionCallOff:
		return "call-off"
	case DetectorTransitionPresenceOn:
		return "presence-on"
	case DetectorTransitionPresenceOff:
		return "presence-off"
	default:
		return "unknown"
	}
}
