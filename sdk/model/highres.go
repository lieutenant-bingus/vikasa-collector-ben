package model

// HighResEvent is one row of a signal controller's high-resolution event
// log, in the ATSPM "Indiana" enumeration: an event code and its parameter
// (a phase, detector channel, overlap or preempt number, or a value,
// depending on the code), timestamped by the controller.
//
// It is the one domain event an EventReader adapter produces for a signal
// controller's log. The enumeration is vendor-neutral — Econolite, MaxTime,
// Trafficware and Yunex controllers all log in it — so an adapter decodes its
// vendor's file format into (code, param) and nothing more; which catalog
// notification and identity a code becomes is the emitter's decision, made
// once for every vendor.
//
// OccurredAt is the controller's timestamp for the row, converted to UTC by
// the adapter: the time the phase went green, not the time it was read.
type HighResEvent struct {
	Base
	Code  uint16
	Param uint16
}

// Stamped is e with the device identity the core owns (LogEvent).
func (e HighResEvent) Stamped(deviceID, deviceKind string) Event {
	e.DeviceID, e.DeviceKind = deviceID, deviceKind
	return e
}

// HighResClass is the family a code belongs to, which picks the catalog
// notification it is published as.
type HighResClass uint8

const (
	HighResUnmapped   HighResClass = iota // no typed notification yet; published raw
	HighResPhase                          // phase-state-change
	HighResPedestrian                     // pedestrian-event
	HighResDetector                       // detector-transition
	HighResOverlap                        // overlap-change
	HighResPreemption                     // preemption-event
)

// Class is the family of e's code. Codes outside every family — and every
// family's code whose parameter could not be a valid number for it — are
// HighResUnmapped, which still reaches the bus with the raw code and param.
func (e HighResEvent) Class() HighResClass {
	c, p := e.Code, e.Param
	phase := p >= 1 && p <= 40 // NEMA phase-number range
	id := p >= 1 && p <= 255   // overlap, preempt and detector numbers on the wire
	switch {
	case c <= 12, c == 14, c == 31, c >= 41 && c <= 44, c == 46, c == 47:
		return pick(phase, HighResPhase)
	case c >= 21 && c <= 26, c == 45, c == 48, c == 49:
		return pick(phase, HighResPedestrian)
	case c >= 67 && c <= 70, c >= 89 && c <= 92:
		return pick(id, HighResPedestrian)
	case c >= 81 && c <= 88:
		return pick(p >= 1, HighResDetector)
	case c >= 61 && c <= 66, c == 32, c == 33:
		return pick(id, HighResOverlap)
	case c >= 101 && c <= 111:
		return pick(id, HighResPreemption)
	case c == 116: // preempt force-off: the param is the phase forced off
		return pick(phase, HighResPreemption)
	default:
		return HighResUnmapped
	}
}

func pick(valid bool, c HighResClass) HighResClass {
	if valid {
		return c
	}
	return HighResUnmapped
}

// EventKind routes by family, so each family reaches its own ce-type.
func (e HighResEvent) EventKind() string {
	switch e.Class() {
	case HighResPhase:
		return "hr-phase"
	case HighResPedestrian:
		return "hr-pedestrian"
	case HighResDetector:
		return "hr-detector"
	case HighResOverlap:
		return "hr-overlap"
	case HighResPreemption:
		return "hr-preemption"
	default:
		return "hr-unmapped"
	}
}
