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

// EventKind is one kind for every row: each is published raw, as the
// catalog's unmapped-event, with its code and param, until the emitter
// maps the Indiana families to their typed notifications.
func (e HighResEvent) EventKind() string { return "hr-unmapped" }
