package model

import "time"

// Device health events use a collector-owned wire schema (ADR 0007) and so
// are publishable even with no openits-models emitter configured. The
// collector's own instance events (started, stopping, heartbeat) are not
// here: no adapter produces them, so they live with their schema in
// internal/wire/health.

// DeviceStatusChanged fires on reachability transitions (up→down, down→up).
type DeviceStatusChanged struct {
	Base
	Reachable           bool
	Reason              string
	ConsecutiveFailures int
	// LastContact is the last successful poll of this device, copied from
	// stats.Devices.LastSuccess when the transition is recorded. Zero means
	// this process has never had a successful poll. Only comm-lost uses it.
	LastContact time.Time
}

func (DeviceStatusChanged) EventKind() string { return "device-status-changed" }
