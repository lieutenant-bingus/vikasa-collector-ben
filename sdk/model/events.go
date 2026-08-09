package model

import "time"

// Event is a discrete domain occurrence — produced by synth (from
// consecutive Snapshots) or returned directly by EventReader adapters.
// Emitters (internal/wire) are the only consumers that turn Events into
// wire payloads.
type Event interface {
	EventKind() string
	EventDeviceID() string
	EventDeviceKind() string
	EventOccurredAt() time.Time
}

// Base carries the fields every event has; embed it.
type Base struct {
	DeviceID string
	// DeviceKind is the emitting device's kind ("asc", "dms", ...). It exists
	// because the catalog defines fault-raised/fault-cleared and mode-changed
	// ONCE and reuses them across every service: the same proto is published
	// as dms.fault-raised.v1 or signal-control.fault-raised.v1 depending only
	// on the device. Without it an emitter cannot route a shared event.
	// Stamped by the runner from the adapter's Descriptor — never by adapters.
	DeviceKind string
	OccurredAt time.Time
}

func (b Base) EventDeviceID() string      { return b.DeviceID }
func (b Base) EventDeviceKind() string    { return b.DeviceKind }
func (b Base) EventOccurredAt() time.Time { return b.OccurredAt }

// OperationalStatusReport is the periodic current-state report for a
// signal controller (emitted every poll, not just on change).
type OperationalStatusReport struct {
	Base
	Mode            ControllerMode
	InConflictFlash bool
	ActivePlanID    uint32
}

func (OperationalStatusReport) EventKind() string { return "operational-status-report" }

// ModeChanged fires when the controller mode transitions.
type ModeChanged struct {
	Base
	From, To ControllerMode
}

func (ModeChanged) EventKind() string { return "mode-changed" }

// PlanChanged fires when the active timing plan transitions.
type PlanChanged struct {
	Base
	FromPlanID, ToPlanID uint32
}

func (PlanChanged) EventKind() string { return "plan-changed" }

// PreemptionActivated fires when preemption becomes active.
type PreemptionActivated struct {
	Base
	Source string
}

func (PreemptionActivated) EventKind() string { return "preemption-activated" }

// PreemptionCleared fires when preemption ends.
type PreemptionCleared struct{ Base }

func (PreemptionCleared) EventKind() string { return "preemption-cleared" }

// FaultRaised fires when a fault appears that was not raised on the previous
// poll. Its OccurredAt IS the first observation — the facet carries no
// timestamp of its own, because the adapter has no memory to know one.
type FaultRaised struct {
	Base
	FaultID     string
	Severity    FaultSeverity
	Category    FaultCategory
	Description string
}

func (FaultRaised) EventKind() string { return "fault-raised" }

// FaultCleared fires when a previously raised fault is absent from a
// SUCCESSFUL poll. A failed read never produces this: synth suspends a facet
// it could not read, so absence of evidence is never a clear.
type FaultCleared struct {
	Base
	FaultID string
}

func (FaultCleared) EventKind() string { return "fault-cleared" }

// DetectorReading is one channel's contribution to a report. VolumeDelta is
// the count over the report's interval, not a cumulative total.
type DetectorReading struct {
	Channel         uint32
	VolumeDelta     uint32
	OccupancyTenths uint16
}

// DetectorReport is the per-interval detector summary, emitted every poll
// after the first. There is no report on the first poll: with no previous
// sample there is no interval to attribute counts to.
type DetectorReport struct {
	Base
	IntervalStart time.Time
	// IntervalDuration is the true elapsed time since IntervalStart, carried
	// losslessly — same reason OccupancyTenths is tenths rather than percent.
	// A sub-second poll interval is common and legitimate; rounding it here
	// to whole seconds would collide it with zero while VolumeDelta stays
	// non-zero, corrupting any rate a consumer computes. The wire's
	// interval-duration-s field constrains the interval to whole seconds >=1;
	// that constraint belongs to the emitter, which rounds once at the edge,
	// not to the domain, which must not pre-break itself to satisfy it.
	IntervalDuration time.Duration
	Readings         []DetectorReading // sorted by Channel
}

func (DetectorReport) EventKind() string { return "detector-report" }

// DMSControlModeChanged fires when control of the sign moves between local,
// central, override, etc. Separate from DMSDisplayStateChanged: they are
// independent axes and an operator taking local control is a different
// occurrence from the display going blank.
type DMSControlModeChanged struct {
	Base
	From, To DMSControlMode
}

func (DMSControlModeChanged) EventKind() string { return "control-mode-changed" }

// DMSDisplayStateChanged fires when what the sign is showing changes state
// (off / blank / test / normal).
type DMSDisplayStateChanged struct {
	Base
	From, To DMSDisplayState
}

func (DMSDisplayStateChanged) EventKind() string { return "display-state-changed" }

// DMSMessageActivationFailed fires when a message slot transitions INTO an
// error state — once per transition, not once per poll while it stays broken.
type DMSMessageActivationFailed struct {
	Base
	MemoryType    MessageMemoryType
	Slot          uint32
	Error         MultiSyntaxError
	ErrorPosition uint32 // character offset into the MULTI string
}

func (DMSMessageActivationFailed) EventKind() string { return "message-activation-failed" }

// TrafficIntervalReport is one completed measurement interval from a traffic
// sensor, emitted when the device presents a NEW interval — not once per poll.
//
// It carries the device's own interval bounds rather than poll timing, and so
// differs from DetectorReport in where the interval comes from: an NTCIP
// controller exposes cumulative counters the collector must difference, while
// these sensors bin internally and hand over a finished interval.
type TrafficIntervalReport struct {
	Base
	IntervalStart    time.Time
	IntervalDuration time.Duration
	Lanes            []LaneMeasurement // sorted by LaneID
}

func (TrafficIntervalReport) EventKind() string { return "traffic-interval-report" }

// ZoneIncidentDetected fires when an incident appears that was not active on
// the previous poll. Like FaultRaised, its OccurredAt IS the first
// observation: the facet carries no timing of its own.
type ZoneIncidentDetected struct {
	Base
	ZoneIncident
}

func (ZoneIncidentDetected) EventKind() string { return "zone-incident-detected" }

// ZoneIncidentUpdated fires when an ACTIVE incident's assessment changes —
// severity, speed, or confidence. It deliberately does not fire for changes to
// identity or classification: an incident whose zone or object class changed is
// a different incident, and reporting it as an update would hide that.
type ZoneIncidentUpdated struct {
	Base
	IncidentID         string
	ZoneID             string
	Severity           IncidentSeverity
	SpeedHundredthsKPH uint32
	SpeedReported      bool
	ConfidencePercent  uint8
}

func (ZoneIncidentUpdated) EventKind() string { return "zone-incident-updated" }

// ZoneIncidentCleared fires when a previously active incident is absent from a
// SUCCESSFUL poll. A failed read never produces this — synth suspends a facet
// it could not read, so absence of evidence is never a clear.
type ZoneIncidentCleared struct {
	Base
	IncidentID string
	ZoneID     string
}

func (ZoneIncidentCleared) EventKind() string { return "zone-incident-cleared" }

// ZoneIntervalReport is one completed aggregate interval from a perception
// sensor, emitted when the device presents a NEW interval. Same contract as
// TrafficIntervalReport: the device does the binning, the collector does not
// invent a window.
type ZoneIntervalReport struct {
	Base
	IntervalStart    time.Time
	IntervalDuration time.Duration
	Zones            []ZoneMeasurement // sorted by ZoneID
}

func (ZoneIntervalReport) EventKind() string { return "zone-interval-report" }
