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
	oidDMSActivateMsgErr     = ".1.3.6.1.4.1.1206.4.2.3.6.17.0"
	oidDMSMultiSyntaxErr     = ".1.3.6.1.4.1.1206.4.2.3.6.18.0"
	oidDMSMultiSyntaxPos     = ".1.3.6.1.4.1.1206.4.2.3.6.19.0"
	oidDMSCurrentBufferMULTI = ".1.3.6.1.4.1.1206.4.2.3.5.8.1.3.5.1"
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
	vals, err := a.client.GetAll(ctx, []string{
		oidDMSControlMode,
		oidDMSMsgTableSource,
		oidDMSActivateMsgErr,
		oidDMSMultiSyntaxErr,
		oidDMSMultiSyntaxPos,
		oidDMSCurrentBufferMULTI,
	})
	if err != nil {
		// The whole Get failed: the device is unreachable. That is a hard
		// Read error which the runner turns into a health event — not a fault.
		return nil, fmt.Errorf("ntcip-dms %s: %w", a.deviceID, err)
	}
	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}
	a.readDMSStatus(snap, vals)
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
		mem, slot, parsed := parseMessageIDCode(src)
		if parsed {
			st.ActiveMemoryType = memoryTypeFromNTCIP(int64(mem))
			st.ActiveSlot = uint32(slot)
			if mem == ntcipMemBlank {
				st.DisplayState = model.DisplayBlank
			} else {
				st.DisplayState = model.DisplayNormal
			}
		}
	}
	if multi, ok := vals.Octets[oidDMSCurrentBufferMULTI]; ok {
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

	snap.Facets = append(snap.Facets, st)
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
// 2 byte messageNumber (big-endian), 2 byte CRC. CRC is not a domain field.
func parseMessageIDCode(b []byte) (memType byte, slot uint16, ok bool) {
	if len(b) < 5 {
		return 0, 0, false
	}
	return b[0], binary.BigEndian.Uint16(b[1:3]), true
}
