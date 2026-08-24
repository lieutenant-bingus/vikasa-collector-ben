package ntcip

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// Fixture transcribed from SNMPv1 GET against a Ledstar CTL24 VMS
// controller, firmware V6.9n (2026-08-21). Integer OIDs and the 5-byte
// MessageIDCode (changeable slot 3, CRC 10526) are the recorded values.
// currentBuffer MULTI is the face copy from that same poll. This is not a
// walk — walks genError on this agent. A later live Read (2026-08-24)
// against the same controller confirmed the mapping still decodes
// (control=central, display=normal, changeable slot occupied).
var healthyDMSInts = map[string]int64{
	".1.3.6.1.4.1.1206.4.2.3.6.1.0":  4, // dmsControlMode: central
	".1.3.6.1.4.1.1206.4.2.3.6.17.0": 2, // dmsActivateMsgError: none
	".1.3.6.1.4.1.1206.4.2.3.6.18.0": 2, // dmsMultiSyntaxError: none
	".1.3.6.1.4.1.1206.4.2.3.6.19.0": 0, // syntax position
}

var healthyDMSOctets = map[string][]byte{
	".1.3.6.1.4.1.1206.4.2.3.6.5.0":       {3, 0, 3, 0x29, 0x2e}, // MessageIDCode: changeable #3
	".1.3.6.1.4.1.1206.4.2.3.5.8.1.3.5.1": []byte("[fo2][jl3]LEFT[nl]LANE[nl]CLOSED"),
}

func TestDMSReadGolden(t *testing.T) {
	a := NewDMS("dms-1", &snmptest.Static{Values: healthyDMSInts, Octets: healthyDMSOctets})
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.DeviceID != "dms-1" || snap.SampledAt.IsZero() {
		t.Fatalf("bad header: %+v", snap)
	}
	f, ok := snap.Facet(model.KindDMSStatus)
	if !ok {
		t.Fatal("missing dms-status facet")
	}
	want := model.DMSStatus{
		ControlMode:      model.ControlCentral,
		DisplayState:     model.DisplayNormal,
		ActiveMemoryType: model.MemoryChangeable,
		ActiveSlot:       3,
		MessageStatus:    model.StatusValid,
		SyntaxError:      model.SyntaxErrorNone,
	}
	if got := f.(model.DMSStatus); !reflect.DeepEqual(got, want) {
		t.Fatalf("DMSStatus = %+v, want %+v", got, want)
	}
	if snap.FacetFailed(model.KindDMSStatus) {
		t.Fatalf("unexpected dms-status facet error: %+v", snap.Errors)
	}
}

func TestDMSUnansweredControlModeIsFacetError(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyDMSInts {
		if k == oidDMSControlMode {
			continue
		}
		fx[k] = v
	}
	snap, err := NewDMS("dms-1", &snmptest.Static{Values: fx, Octets: healthyDMSOctets}).Read(context.Background())
	if err != nil {
		t.Fatalf("partial data must not be a hard error: %v", err)
	}
	if _, ok := snap.Facet(model.KindDMSStatus); ok {
		t.Fatal("incomplete facet must not be present")
	}
	if !snap.FacetFailed(model.KindDMSStatus) {
		t.Fatal("expected dms-status FacetError")
	}
}

func TestDMSReadTransportErrorIsHardError(t *testing.T) {
	_, err := NewDMS("dms-1", &snmptest.Static{Err: errors.New("timeout")}).Read(context.Background())
	if err == nil {
		t.Fatal("transport failure must be a hard Read error")
	}
}

func TestDMSActivateErrorTwoIsNone(t *testing.T) {
	// 2 = none on this firmware. Treating it as an error would fire
	// message-activation-failed every poll.
	a := NewDMS("dms-1", &snmptest.Static{Values: healthyDMSInts, Octets: healthyDMSOctets})
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := snap.Facet(model.KindDMSStatus)
	if st := got.(model.DMSStatus); st.MessageStatus != model.StatusValid {
		t.Fatalf("MessageStatus = %v, want valid (activate error 2 is none)", st.MessageStatus)
	}
}

func TestDMSBlankMemoryTypeIsDisplayBlank(t *testing.T) {
	oct := map[string][]byte{
		oidDMSMsgTableSource:     {7, 0, 0, 0, 0},
		oidDMSCurrentBufferMULTI: {},
	}
	snap, err := NewDMS("dms-1", &snmptest.Static{Values: healthyDMSInts, Octets: oct}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, ok := snap.Facet(model.KindDMSStatus)
	if !ok {
		t.Fatal("missing dms-status facet")
	}
	st := got.(model.DMSStatus)
	if st.DisplayState != model.DisplayBlank {
		t.Fatalf("DisplayState = %v, want blank", st.DisplayState)
	}
	if st.ActiveMemoryType != model.MemoryBlank {
		t.Fatalf("ActiveMemoryType = %v, want blank", st.ActiveMemoryType)
	}
}

func TestDMSOutOfRangeControlModeIsUnknown(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyDMSInts {
		fx[k] = v
	}
	fx[oidDMSControlMode] = 99
	snap, err := NewDMS("dms-1", &snmptest.Static{Values: fx, Octets: healthyDMSOctets}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := snap.Facet(model.KindDMSStatus)
	if st := got.(model.DMSStatus); st.ControlMode != model.ControlUnknown {
		t.Fatalf("ControlMode = %v, want unknown", st.ControlMode)
	}
}

func TestDMSUnmappedSyntaxErrorIsOther(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyDMSInts {
		fx[k] = v
	}
	fx[oidDMSActivateMsgErr] = 3
	fx[oidDMSMultiSyntaxErr] = 99
	snap, err := NewDMS("dms-1", &snmptest.Static{Values: fx, Octets: healthyDMSOctets}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := snap.Facet(model.KindDMSStatus)
	st := got.(model.DMSStatus)
	if st.MessageStatus != model.StatusError {
		t.Fatalf("MessageStatus = %v, want error", st.MessageStatus)
	}
	if st.SyntaxError != model.SyntaxErrorOther {
		t.Fatalf("SyntaxError = %v, want other (unmapped must not render as none)", st.SyntaxError)
	}
}

func TestDMSDescriptorCaps(t *testing.T) {
	a := NewDMS("dms-1", &snmptest.Static{})
	d := a.Descriptor()
	if d.Vendor != "ntcip" || d.DeviceKind != "dms" {
		t.Fatalf("Descriptor = %+v", d)
	}
	if !d.Caps.Has(adapter.CapState) || d.Caps.Has(adapter.CapEvents) || d.Caps.Has(adapter.CapCommand) {
		t.Fatalf("Caps = %v; want CapState only", d.Caps)
	}
}

func TestParseSNMPBlockRejectsMissingAddress(t *testing.T) {
	_, err := parseSNMPBlock(map[string]any{"snmp": map[string]any{}})
	if err == nil {
		t.Fatal("missing address must be rejected at factory time")
	}
	_, err = parseSNMPBlock(map[string]any{})
	if err == nil {
		t.Fatal("missing snmp block must be rejected")
	}
}

func TestParseSNMPBlockRejectsBadVersion(t *testing.T) {
	_, err := parseSNMPBlock(map[string]any{
		"snmp": map[string]any{"address": "10.0.0.20:161", "version": "v3"},
	})
	if err == nil {
		t.Fatal("unsupported SNMP version must be rejected at factory time")
	}
}

func TestParseSNMPBlockAcceptsV1(t *testing.T) {
	cfg, err := parseSNMPBlock(map[string]any{
		"snmp": map[string]any{"address": "10.0.0.20:161", "version": "v1"},
	})
	if err != nil {
		t.Fatalf("parseSNMPBlock: %v", err)
	}
	if cfg.Version != "v1" || cfg.Address != "10.0.0.20:161" || cfg.Community != "public" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestParseMessageIDCode(t *testing.T) {
	mem, slot, ok := parseMessageIDCode([]byte{3, 0, 3, 0x29, 0x2e})
	if !ok || mem != 3 || slot != 3 {
		t.Fatalf("got mem=%d slot=%d ok=%v", mem, slot, ok)
	}
	if _, _, ok := parseMessageIDCode([]byte{3, 0}); ok {
		t.Fatal("short buffer must not parse")
	}
}

func TestControlModeFromNTCIP(t *testing.T) {
	cases := map[int64]model.DMSControlMode{
		1:  model.ControlOther,
		2:  model.ControlLocal,
		3:  model.ControlExternal,
		4:  model.ControlCentral,
		5:  model.ControlCentralOverride,
		99: model.ControlUnknown,
	}
	for in, want := range cases {
		if got := controlModeFromNTCIP(in); got != want {
			t.Errorf("controlModeFromNTCIP(%d) = %v, want %v", in, got, want)
		}
	}
}
