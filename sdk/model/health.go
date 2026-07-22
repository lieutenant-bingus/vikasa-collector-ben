package model

// Health events use a collector-owned wire schema (ADR 0007) and so are
// publishable even with no openits-models emitter configured.

// DeviceStatusChanged fires on reachability transitions (up→down, down→up).
type DeviceStatusChanged struct {
	Base
	Reachable           bool
	Reason              string
	ConsecutiveFailures int
}

func (DeviceStatusChanged) EventKind() string { return "device-status-changed" }

// CollectorStarted fires once at boot. DeviceID is empty: the subject of
// the event is the collector itself.
type CollectorStarted struct {
	Base
	Version string
}

func (CollectorStarted) EventKind() string { return "collector-started" }
