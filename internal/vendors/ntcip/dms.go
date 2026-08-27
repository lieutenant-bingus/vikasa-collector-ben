package ntcip

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// NTCIP 1203 v03 leaves polled per cycle. GET only — this agent family
// wraps or genErrors on prefix walks (Ledstar CTL24 firmware V6.9n,
// recorded 2026-08-21). Do not add GetNext/GetBulk here.
//
// Root: .1.3.6.1.4.1.1206.4.2.3
const (
	oidDMSControlMode        = ".1.3.6.1.4.1.1206.4.2.3.6.1.0"
	oidDMSMsgTableSource     = ".1.3.6.1.4.1.1206.4.2.3.6.5.0"
	oidDMSMsgSourceMode      = ".1.3.6.1.4.1.1206.4.2.3.6.7.0"
	oidDMSActivateMsgErr     = ".1.3.6.1.4.1.1206.4.2.3.6.17.0"
	oidDMSMultiSyntaxErr     = ".1.3.6.1.4.1.1206.4.2.3.6.18.0"
	oidDMSMultiSyntaxPos     = ".1.3.6.1.4.1.1206.4.2.3.6.19.0"
	oidDMSCurrentBufferMULTI = ".1.3.6.1.4.1.1206.4.2.3.5.8.1.3.5.1"

	// illum group (dms 7)
	oidDMSIllumMaxPhotocell = ".1.3.6.1.4.1.1206.4.2.3.7.2.0"
	oidDMSIllumPhotocell    = ".1.3.6.1.4.1.1206.4.2.3.7.3.0"
	oidDMSIllumNumBright    = ".1.3.6.1.4.1.1206.4.2.3.7.4.0"
	oidDMSIllumBrightLevel  = ".1.3.6.1.4.1.1206.4.2.3.7.5.0"

	// dmsStatus scalars / known table cells (GET by OID; no walk)
	oidDMSStatDoorOpen = ".1.3.6.1.4.1.1206.4.2.3.9.6.0"
	// Temp/humidity tables under statError: index 1 → cabinet, index 2 → face.
	// Vendor description strings would refine that mapping; until then this is
	// the conventional two-sensor layout.
	oidDMSTempCabinet = ".1.3.6.1.4.1.1206.4.2.3.9.7.36.1.3.1"
	oidDMSTempFace    = ".1.3.6.1.4.1.1206.4.2.3.9.7.36.1.3.2"
	oidDMSHumidity1   = ".1.3.6.1.4.1.1206.4.2.3.9.7.33.1.3.1"
)

// NTCIP 1203 error objects on this firmware use 2 = none, not 0.
const ntcipNone = 2

// Message-table memoryType numbers as used in MessageIDCode on 1203 v03
// (permanent=2, changeable=3, volatile=4, currentBuffer=5, schedule=6, blank=7).
const (
	ntcipMemPermanent     = 2
	ntcipMemChangeable    = 3
	ntcipMemVolatile      = 4
	ntcipMemCurrentBuffer = 5
	ntcipMemSchedule      = 6
	ntcipMemBlank         = 7
)

var dmsStatusOIDs = []string{
	oidDMSControlMode,
	oidDMSMsgTableSource,
	oidDMSMsgSourceMode,
	oidDMSActivateMsgErr,
	oidDMSMultiSyntaxErr,
	oidDMSMultiSyntaxPos,
	oidDMSCurrentBufferMULTI,
}

var dmsEnvOIDs = []string{
	oidDMSIllumMaxPhotocell,
	oidDMSIllumPhotocell,
	oidDMSIllumNumBright,
	oidDMSIllumBrightLevel,
	oidDMSStatDoorOpen,
	oidDMSTempCabinet,
	oidDMSTempFace,
	oidDMSHumidity1,
}

var dmsDescriptor = adapter.Descriptor{
	Vendor: "ntcip", DeviceKind: "dms", Caps: adapter.CapState,
}

type dms struct {
	deviceID string
	client   snmp.Client
	now      func() time.Time
}

// NewDMS wraps an SNMP client as the ntcip-dms StateReader. Exported so
// fixture tests can inject a client.
func NewDMS(deviceID string, client snmp.Client) adapter.StateReader {
	return &dms{deviceID: deviceID, client: client, now: time.Now}
}

func (a *dms) Descriptor() adapter.Descriptor { return dmsDescriptor }
func (a *dms) Close() error                   { return a.client.Close() }

func (a *dms) Read(ctx context.Context) (*model.Snapshot, error) {
	statusVals, err := a.client.GetAll(ctx, dmsStatusOIDs)
	if err != nil {
		// The whole status Get failed: the device is unreachable. That is a
		// hard Read error which the runner turns into a health event — not a
		// fault. Environment is a separate Get so a dead illum group cannot
		// take the face-status path down with it.
		return nil, fmt.Errorf("ntcip-dms %s: %w", a.deviceID, err)
	}
	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}
	a.readDMSStatus(snap, statusVals)

	envVals, err := a.client.GetAll(ctx, dmsEnvOIDs)
	if err != nil {
		// Environment read failed independently. Leave the facet off this
		// poll — a gap in the series, never a fabricated repeat, never a
		// hard Read error that would also drop dms-status.
		return snap, nil
	}
	a.readDMSEnvironment(snap, envVals)
	return snap, nil
}

func (a *dms) readDMSStatus(snap *model.Snapshot, vals snmp.Values) {
	mode, ok := vals.Ints[oidDMSControlMode]
	if !ok {
		// Mandatory OID unanswered: report the facet failed rather than
		// fabricating state (absence of evidence is never a state change).
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDMSStatus, Err: "dmsControlMode OID unanswered",
		})
		return
	}

	st := model.DMSStatus{ControlMode: controlModeFromNTCIP(mode)}

	if src, ok := vals.Octets[oidDMSMsgTableSource]; ok {
		mem, slot, crc, parsed := parseMessageIDCode(src)
		if parsed {
			st.ActiveMemoryType = memoryTypeFromNTCIP(int64(mem))
			st.ActiveSlot = uint32(slot)
			st.MessageCRC = uint32(crc)
			if mem == ntcipMemBlank {
				st.DisplayState = model.DisplayBlank
			} else {
				st.DisplayState = model.DisplayNormal
			}
		}
	}
	if multi, ok := vals.Octets[oidDMSCurrentBufferMULTI]; ok {
		// Verbatim MULTI on the face. Empty means blank, never "could not
		// read" — an unanswered OID is omitted from Octets entirely.
		st.MessageText = string(multi)
		if len(multi) == 0 {
			st.DisplayState = model.DisplayBlank
		} else if st.DisplayState == model.DisplayUnknown {
			st.DisplayState = model.DisplayNormal
		}
	}

	if errCode, ok := vals.Ints[oidDMSActivateMsgErr]; ok {
		if errCode == ntcipNone {
			st.MessageStatus = model.StatusValid
		} else {
			st.MessageStatus = model.StatusError
		}
	}

	if syn, ok := vals.Ints[oidDMSMultiSyntaxErr]; ok {
		st.SyntaxError = syntaxErrorFromNTCIP(syn)
	}
	if pos, ok := vals.Ints[oidDMSMultiSyntaxPos]; ok && pos > 0 {
		st.SyntaxErrorPos = uint32(pos)
	}

	// dmsMsgSourceMode conflates authority (already on ControlMode) with WHY
	// the face message is there. Only the WHY half lands on ActivationTrigger.
	if srcMode, ok := vals.Ints[oidDMSMsgSourceMode]; ok {
		st.ActivationTrigger = activationTriggerFromNTCIP(srcMode)
	}

	snap.Facets = append(snap.Facets, st)
}

func (a *dms) readDMSEnvironment(snap *model.Snapshot, vals snmp.Values) {
	env := model.DMSEnvironment{}

	maxPC, hasMax := vals.Ints[oidDMSIllumMaxPhotocell]
	pc, hasPC := vals.Ints[oidDMSIllumPhotocell]
	if hasMax && hasPC && maxPC > 0 {
		env.AmbientLightPercent = percentOf(pc, maxPC)
		env.AmbientLightReported = true
	}

	numBright, hasNum := vals.Ints[oidDMSIllumNumBright]
	level, hasLevel := vals.Ints[oidDMSIllumBrightLevel]
	if hasNum && hasLevel && numBright > 0 {
		env.BrightnessPercent = percentOf(level, numBright)
		env.BrightnessReported = true
	}

	if door, ok := vals.Ints[oidDMSStatDoorOpen]; ok {
		env.DoorOpen = door != 0
		env.DoorReported = true
	}

	if t, ok := vals.Ints[oidDMSTempCabinet]; ok {
		env.CabinetTempDeciC = int16(t * 10)
		env.CabinetTempReported = true
	}
	if t, ok := vals.Ints[oidDMSTempFace]; ok {
		env.FaceTempDeciC = int16(t * 10)
		env.FaceTempReported = true
	}
	if h, ok := vals.Ints[oidDMSHumidity1]; ok {
		if h < 0 {
			h = 0
		}
		if h > 100 {
			h = 100
		}
		env.HumidityPercent = uint8(h)
		env.HumidityReported = true
	}

	// Only emit the facet when at least one sensor answered. An all-absent
	// env would otherwise produce empty sign-status-report spam every poll.
	if !(env.BrightnessReported || env.AmbientLightReported || env.IlluminanceReported ||
		env.CabinetTempReported || env.FaceTempReported || env.HumidityReported ||
		env.DoorReported || env.FanReported || env.HeaterReported) {
		return
	}
	snap.Facets = append(snap.Facets, env)
}

// percentOf scales raw/max into 0–100. Used for both photocell and brightness
// level normalization against the device's own reported maximum.
func percentOf(raw, max int64) uint8 {
	if max <= 0 {
		return 0
	}
	if raw <= 0 {
		return 0
	}
	if raw >= max {
		return 100
	}
	return uint8((raw * 100) / max)
}

func controlModeFromNTCIP(v int64) model.DMSControlMode {
	// NTCIP 1203:2011 v03.04 as implemented on Ledstar CTL24: 2 local,
	// 4 central, 5 centralOverride. 1 other / 3 external are the v03
	// numbers not observed live; simulation is not this firmware's 5.
	switch v {
	case 1:
		return model.ControlOther
	case 2:
		return model.ControlLocal
	case 3:
		return model.ControlExternal
	case 4:
		return model.ControlCentral
	case 5:
		return model.ControlCentralOverride
	default:
		return model.ControlUnknown
	}
}

// activationTriggerFromNTCIP maps the WHY half of dmsMsgSourceMode.
// Authority values (local/external/central) mean "someone commanded this
// onto the face" → TriggerCommand; scheduler and fallbacks get their own
// identities. ControlMode (dmsControlMode) remains the authority axis.
func activationTriggerFromNTCIP(v int64) model.DMSActivationTrigger {
	switch v {
	case 1: // other
		return model.TriggerOther
	case 2, 3, 8: // local, external, central
		return model.TriggerCommand
	case 9: // timebasedScheduler
		return model.TriggerSchedule
	case 10: // powerRecovery
		return model.TriggerPowerRecovery
	case 11: // reset
		return model.TriggerReset
	case 12: // commLoss
		return model.TriggerCommLoss
	case 13: // powerLoss
		return model.TriggerPowerLoss
	case 14: // endDuration
		return model.TriggerEndOfDuration
	default:
		return model.TriggerUnknown
	}
}

func memoryTypeFromNTCIP(v int64) model.MessageMemoryType {
	switch v {
	case ntcipMemPermanent:
		return model.MemoryPermanent
	case ntcipMemChangeable:
		return model.MemoryChangeable
	case ntcipMemVolatile:
		return model.MemoryVolatile
	case ntcipMemCurrentBuffer:
		// Working copy on the face, not a library bank the domain models.
		return model.MemoryUnknown
	case ntcipMemSchedule:
		return model.MemorySchedule
	case ntcipMemBlank:
		return model.MemoryBlank
	default:
		return model.MemoryUnknown
	}
}

func syntaxErrorFromNTCIP(v int64) model.MultiSyntaxError {
	// 2 = none on this firmware. Remaining codes are NTCIP 1203 v03
	// dmsMultiSyntaxError; unmapped non-none values MUST become Other
	// (sdk/model/dms.go: leaving them unmapped renders as "none").
	switch v {
	case ntcipNone:
		return model.SyntaxErrorNone
	case 3: // syntax
		return model.SyntaxErrorSyntax
	case 4, 5: // unsupportedTag, unsupportedTagValue
		return model.SyntaxErrorUnsupportedTag
	case 6: // textTooBig
		return model.SyntaxErrorTooLong
	case 7: // fontNotDefined
		return model.SyntaxErrorFontNotFound
	case 10: // fieldDeviceError
		return model.SyntaxErrorHardware
	case 16: // graphicNotDefined
		return model.SyntaxErrorGraphicNotFound
	default:
		if v == 0 {
			return model.SyntaxErrorNone
		}
		return model.SyntaxErrorOther
	}
}

// parseMessageIDCode decodes NTCIP 1203 MessageIDCode: 1 byte memoryType,
// 2 byte messageNumber (big-endian), 2 byte CRC-16 (big-endian).
func parseMessageIDCode(b []byte) (memType byte, slot uint16, crc uint16, ok bool) {
	if len(b) < 5 {
		return 0, 0, 0, false
	}
	return b[0], binary.BigEndian.Uint16(b[1:3]), binary.BigEndian.Uint16(b[3:5]), true
}
