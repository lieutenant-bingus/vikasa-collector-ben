package model

// KindSignalStatus is the signal-controller operational-state facet.
const KindSignalStatus Kind = "signal-status"

// SignalStatus is the operational state of a signal controller at one poll.
type SignalStatus struct {
	Mode             ControllerMode
	InConflictFlash  bool
	ActivePlanID     uint32
	PreemptionActive bool
	PreemptionSource string
}

func (SignalStatus) FacetKind() Kind { return KindSignalStatus }
