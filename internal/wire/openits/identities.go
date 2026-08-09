package openits

import (
	"fmt"

	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Identity module prefixes. Wire identityref leaves are plain strings
// rendered as "defining-module:identity-name", so every mapped value is a
// constant here rather than a generated enum.
const (
	scTypes         = "openits-signal-control-types:"
	dmsTypes        = "openits-dms-types:"
	cctvTypes       = "openits-cctv-types:"
	trafficSenTypes = "openits-traffic-sensor-types:"
	perceptionTypes = "openits-perception-types:"
)

// controllerModeIdentity maps a domain controller mode to its upstream
// identity. ok=false means the wire model has no identity for this mode, which
// is a map-or-drop decision the caller must make explicitly rather than guess.
//
// ModeNormal maps to mode-FREE deliberately: upstream collapsed "normal" into
// "free" because NTCIP and signal technicians treat uncoordinated-actuated
// operation as a single mode. The mode-normal identity that does exist belongs
// to openits-dms-types and is a sign display state, not a controller mode.
//
// ModeStandby and ModeUnknown have no controller-mode identity at all — the
// upstream set is coordinated/free/flash/preempt/priority/manual/off. Adding
// mode-standby upstream is tracked; until then those events are not claimed.
func controllerModeIdentity(m model.ControllerMode) (string, bool) {
	switch m {
	case model.ModeNormal:
		return scTypes + "mode-free", true
	case model.ModeFlash:
		return scTypes + "mode-flash", true
	case model.ModeOff:
		return scTypes + "mode-off", true
	default:
		return "", false
	}
}

// faultKindIdentity maps a domain fault category to the per-service `kind`
// identityref, which is MANDATORY on fault-raised and fault-cleared.
//
// This is where model.FaultCategory lands. The domain kept Category even
// though the catalog has no category field on its fault events, on the theory
// that the domain may be richer than the wire; it turns out the wire wants it
// after all, just spelled as an event-class identity rather than an enum.
//
// Categories with no per-service leaf identity fall back to the service's BASE
// fault identity rather than declining the event. openits-dms-types documents
// that base as exactly this — "the fallback kind when a decoder cannot map a
// more specific leaf identity" — and the signal-control base fills the same
// structural role. This is deliberately unlike the mode mapping: a fault whose
// class we cannot name is still a real fault worth reporting, and the base
// identity says "a fault, class unmapped" honestly. A mode we cannot name has
// no such honest rendering, because `current` must assert a specific state.
//
// CategoryUnknown is the zero value, so it is the common case for any adapter
// that has not classified its faults yet; it must not silently suppress them.
func faultKindIdentity(c model.FaultCategory, deviceKind string) (string, bool) {
	switch deviceKind {
	case "asc":
		switch c {
		case model.CategoryConflict:
			return scTypes + "sc-fault-conflict", true
		case model.CategoryCabinet:
			return scTypes + "sc-fault-cabinet", true
		case model.CategoryPower:
			return scTypes + "sc-fault-power", true
		case model.CategoryCommunication:
			return scTypes + "sc-fault-communication", true
		case model.CategoryDetector:
			return scTypes + "sc-fault-detector", true
		case model.CategoryLamp:
			return scTypes + "sc-fault-lamp", true
		default:
			// Includes the DMS-only categories (pixel, controller,
			// environment): meaningless on a controller, so the base rather
			// than a cross-service identity that would misdescribe the fault.
			return scTypes + "sc-fault-event-kind", true
		}
	case "cctv":
		// The domain's category vocabulary predates this service: it has
		// nothing for video loss, focus, iris, or position feedback, which is
		// most of what a camera actually reports. Only the genuinely shared
		// causes map; the rest fall back, honestly, to the base.
		switch c {
		case model.CategoryCommunication:
			return cctvTypes + "cctv-fault-comms", true
		case model.CategoryEnvironment:
			return cctvTypes + "cctv-fault-enclosure", true
		default:
			return cctvTypes + "cctv-fault-event-kind", true
		}
	case "traffic-sensor":
		switch c {
		case model.CategoryPower:
			return trafficSenTypes + "traffic-sensor-fault-power", true
		case model.CategoryCommunication:
			return trafficSenTypes + "traffic-sensor-fault-communication", true
		case model.CategoryEnvironment:
			return trafficSenTypes + "traffic-sensor-fault-temperature", true
		default:
			// Deliberately NOT mapping CategoryDetector here. The service has
			// stuck-on, chattering and no-activity, which are three distinct
			// detector pathologies; picking one for a category that means
			// "something detector-ish" would assert a specific failure the
			// device never reported.
			return trafficSenTypes + "traffic-sensor-fault-event-kind", true
		}
	case "perception":
		switch c {
		case model.CategoryPower:
			return perceptionTypes + "perception-fault-power", true
		case model.CategoryCommunication:
			return perceptionTypes + "perception-fault-communication", true
		case model.CategoryEnvironment:
			return perceptionTypes + "perception-fault-temperature", true
		default:
			return perceptionTypes + "perception-fault-event-kind", true
		}
	case "dms":
		switch c {
		case model.CategoryLamp:
			return dmsTypes + "dms-fault-lamp", true
		case model.CategoryPixel:
			return dmsTypes + "dms-fault-pixel", true
		case model.CategoryController:
			return dmsTypes + "dms-fault-controller", true
		case model.CategoryEnvironment:
			return dmsTypes + "dms-fault-environment", true
		case model.CategoryPower:
			return dmsTypes + "dms-fault-power", true
		case model.CategoryCommunication:
			return dmsTypes + "dms-fault-communication", true
		default:
			return dmsTypes + "dms-fault-event-kind", true
		}
	default:
		return "", false
	}
}

// dmsControlModeIdentity maps who is driving the sign. There is no "unknown"
// member upstream (local/external/central/central-override/simulation/other),
// and dms-control-other means "a vendor-specific mode not covered above" —
// a different claim from "we do not know" — so ControlUnknown is declined
// rather than folded into it.
func dmsControlModeIdentity(m model.DMSControlMode) (string, bool) {
	switch m {
	case model.ControlLocal:
		return dmsTypes + "dms-control-local", true
	case model.ControlExternal:
		return dmsTypes + "dms-control-external", true
	case model.ControlCentral:
		return dmsTypes + "dms-control-central", true
	case model.ControlCentralOverride:
		return dmsTypes + "dms-control-central-override", true
	case model.ControlSimulation:
		return dmsTypes + "dms-control-simulation", true
	case model.ControlOther:
		return dmsTypes + "dms-control-other", true
	default:
		return "", false
	}
}

// dmsDisplayStateIdentity maps what the sign is showing, onto the sign-mode
// identity set. Unlike controller mode and DMS control mode, sign-mode DOES
// define mode-unknown, so every domain value is expressible and none are
// declined.
func dmsDisplayStateIdentity(s model.DMSDisplayState) (string, bool) {
	switch s {
	case model.DisplayOff:
		return dmsTypes + "mode-off", true
	case model.DisplayBlank:
		return dmsTypes + "mode-blank", true
	case model.DisplayTest:
		return dmsTypes + "mode-test", true
	case model.DisplayNormal:
		return dmsTypes + "mode-normal", true
	case model.DisplayUnknown:
		return dmsTypes + "mode-unknown", true
	default:
		return "", false
	}
}

// occupancyPercent renders tenths-of-a-percent as the wire's decimal64 string.
// The domain carries tenths as an integer precisely so nothing is lost in
// transit; the wire wants "12.5". Always one decimal place, so zero renders
// "0.0" rather than "0" — a decimal64 with fraction-digits 1 is written with
// its fraction present.
func occupancyPercent(tenths uint16) string {
	return fmt.Sprintf("%d.%d", tenths/10, tenths%10)
}

// memoryTypeFor maps the domain memory bank to the wire enum. Explicit switch,
// not a cast: MemoryUnknown is the domain zero but UNSPECIFIED is the wire
// zero, and those agreeing today is a coincidence worth not depending on.
func memoryTypeFor(m model.MessageMemoryType) dmsv1.MessageMemoryType {
	switch m {
	case model.MemoryPermanent:
		return dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_PERMANENT
	case model.MemoryChangeable:
		return dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_CHANGEABLE
	case model.MemoryVolatile:
		return dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_VOLATILE
	case model.MemorySchedule:
		return dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_SCHEDULE
	case model.MemoryBlank:
		return dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_BLANK
	default:
		return dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_UNSPECIFIED
	}
}

// errorTypeFor maps the domain MULTI error to the wire enum.
//
// SyntaxErrorNone is the awkward one. dmsv1.ErrorType has no "none" member and
// its zero is SYNTAX, so a domain None would encode as a *syntax error* if
// this were a cast — asserting a specific failure the sign never reported. It
// maps to OTHER instead: the event only exists because activation failed, so
// "failed, cause unreported" is true where "syntax error" is not. Same shape
// of decision as the fault-category fallback.
func errorTypeFor(e model.MultiSyntaxError) dmsv1.ErrorType {
	switch e {
	case model.SyntaxErrorSyntax:
		return dmsv1.ErrorType_ERROR_TYPE_SYNTAX
	case model.SyntaxErrorUnsupportedTag:
		return dmsv1.ErrorType_ERROR_TYPE_UNSUPPORTED_TAG
	case model.SyntaxErrorFontNotFound:
		return dmsv1.ErrorType_ERROR_TYPE_FONT_NOT_FOUND
	case model.SyntaxErrorGraphicNotFound:
		return dmsv1.ErrorType_ERROR_TYPE_GRAPHIC_NOT_FOUND
	case model.SyntaxErrorTooLong:
		return dmsv1.ErrorType_ERROR_TYPE_TOO_LONG
	case model.SyntaxErrorHardware:
		return dmsv1.ErrorType_ERROR_TYPE_HARDWARE
	default:
		return dmsv1.ErrorType_ERROR_TYPE_OTHER
	}
}

// registryBase is the schema-registry root the profile's ce-dataschema URLs
// resolve against.
const registryBase = "https://schemas.open-its.org/"

// dataSchemaFor maps each ce-type to its ce-dataschema URL.
//
// The URL keys on the module that DEFINES the notification and that module's
// revision — never on a base or types module the payload happens to compose.
// That is why signal-control's mode-changed and fault-raised point at
// openits-common-*-events: those modules define the notifications, and
// signal-control merely reuses them. It reads like a mistake and is not.
//
// Revisions are per-module, so they do NOT move in lockstep: dms-events is at
// a later revision than the others because it changed and they did not.
// Upstream deliberately refuses to cut no-op revisions just to keep a set of
// constants aligned.
//
// Hard-coded because openits-models ships no Go catalog API. That is the
// intended trade: a pin bump shows up as a reviewable diff of these constants
// rather than as a silent behaviour change, and the goldens lock them.
var dataSchemaFor = map[string]string{
	"openits.signal-control.mode-changed.v1": registryBase + "openits-common-mode-events/2026-07-21/",
	"openits.dms.mode-changed.v1":            registryBase + "openits-common-mode-events/2026-07-21/",

	"openits.signal-control.fault-raised.v1":  registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.signal-control.fault-cleared.v1": registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.dms.fault-raised.v1":             registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.dms.fault-cleared.v1":            registryBase + "openits-common-fault-events/2026-07-21/",

	// Same defining module for every service: fault-raised/cleared are
	// declared once in openits-common-fault-events and reused, which is why
	// six different services share one ce-dataschema.
	"openits.cctv.fault-raised.v1":            registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.cctv.fault-cleared.v1":           registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.traffic-sensor.fault-raised.v1":  registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.traffic-sensor.fault-cleared.v1": registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.perception.fault-raised.v1":      registryBase + "openits-common-fault-events/2026-07-21/",
	"openits.perception.fault-cleared.v1":     registryBase + "openits-common-fault-events/2026-07-21/",

	"openits.signal-control.plan-applied.v1":              registryBase + "openits-signal-control-events/2026-07-21/",
	"openits.signal-control.operational-status-report.v1": registryBase + "openits-signal-control-events/2026-07-21/",
	"openits.signal-control.preemption-activated.v1":      registryBase + "openits-signal-control-events/2026-07-21/",
	"openits.signal-control.preemption-cleared.v1":        registryBase + "openits-signal-control-events/2026-07-21/",
	"openits.signal-control.detector-report.v1":           registryBase + "openits-signal-control-events/2026-07-21/",

	"openits.dms.message-activation-failed.v1": registryBase + "openits-dms-events/2026-07-23/",
}
