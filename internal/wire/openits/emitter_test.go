package openits

import (
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	cctvv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/cctv/v1"
	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"
	pcpv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/perception/v1"
	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	tsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/traffic_sensor/v1"
	zocv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/zone_occupancy/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// decodeModeChanged encodes ev and unmarshals the payload back, so assertions
// are made against what actually goes on the wire rather than against an
// intermediate the emitter happens to hold.
func decodeModeChanged(t *testing.T, ev model.Event, collectorID string) *commonv1.ModeChanged {
	t.Helper()
	enc, ok, err := New(collectorID, testEpoch).Encode(ev)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatalf("emitter did not claim %s on a %s device", ev.EventKind(), ev.EventDeviceKind())
	}
	var got commonv1.ModeChanged
	if err := proto.Unmarshal(enc.Data, &got); err != nil {
		t.Fatalf("payload is not a valid common/v1.ModeChanged: %v", err)
	}
	return &got
}

func base(deviceID, deviceKind string) model.Base {
	return model.Base{
		DeviceID:   deviceID,
		DeviceKind: deviceKind,
		OccurredAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
}

func TestEncode_ModeChangedOnASC_ClaimsSignalControlCEType(t *testing.T) {
	em := New("cabinet-poller-1", testEpoch)

	enc, ok, err := em.Encode(model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFree,
		To:   model.ModeCoordinated,
	})

	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatal("emitter did not claim ModeChanged on an asc device")
	}
	if got, want := enc.CEType, "openits.signal-control.mode-changed.v1"; got != want {
		t.Errorf("ce-type = %q, want %q", got, want)
	}
}

func TestEncode_ModeChangedOnASC_MapsControllerModeIdentities(t *testing.T) {
	// Coordination mode only (openits-models v0.8.0, ADR 0024): flash and dark
	// are cabinet output, reported by cabinet-output-changed instead.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFree,
		To:   model.ModeCoordinated,
	}, "cabinet-poller-1")

	if want := "openits-signal-control-types:mode-free"; got.GetPreviousMode() != want {
		t.Errorf("prior = %q, want %q", got.GetPreviousMode(), want)
	}
	if want := "openits-signal-control-types:mode-coordinated"; got.GetMode() != want {
		t.Errorf("current = %q, want %q", got.GetMode(), want)
	}
}

// A transition to a value with no honest wire identity is not claimed, and
// the caller's loud-drop path fires: a visible drop beats a wrong value.
func TestEncode_UnrepresentableTargetIsNotClaimed(t *testing.T) {
	cases := []struct {
		name string
		ev   model.Event
	}{
		// An unknown coordination mode has no honest identity to move to (the set is
		// coordinated/free/flash/preempt/priority/manual/off); encoding it
		// would mean inventing a near-neighbour.
		{"asc mode standby", model.ModeChanged{
			Base: base("asc-main-and-5th", "asc"), From: model.ModeFree, To: model.ModeUnknown,
		}},
		// dms-control-mode has no "unknown" member. dms-control-other means
		// "a vendor-specific mode not covered above", which is NOT the same
		// claim as "we don't know".
		{"dms control unknown", model.DMSControlModeChanged{
			Base: base("dms-i35-mm214", "dms"), From: model.ControlCentral, To: model.ControlUnknown,
		}},
		// cctv-control-mode has no "unknown" member either; cctv-control-other
		// is a positive claim about the camera, not an admission of ignorance.
		{"cctv control unknown", model.CCTVControlModeChanged{
			Base: base("cam-03", "cctv"), From: model.CCTVControlCentral, To: model.CCTVControlUnknown,
		}},
		// TourRunState's wire zero is STOPPED with no unspecified, so an
		// unknown state would encode as a positive claim the tour is stopped.
		{"cctv tour unknown", model.CCTVTourStateChanged{
			Base: base("cam-03", "cctv"), TourID: 4, From: model.TourRunning, To: model.TourUnknown,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(tc.ev)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if ok {
				t.Errorf("emitter claimed a value with no upstream identity; payload=%v", enc)
			}
		})
	}
}

func TestEncode_ModeChangedFromUnmappable_StillClaimedWithPriorAbsent(t *testing.T) {
	// The reverse: `current` is mandatory but `prior` is optional ("absent when
	// the device just started up"). An unmappable From must not suppress an
	// otherwise-encodable transition.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeUnknown,
		To:   model.ModeCoordinated,
	}, "cabinet-poller-1")

	if got.PreviousMode != nil {
		t.Errorf("prior = %q, want absent for an unknown From", got.GetPreviousMode())
	}
	if want := "openits-signal-control-types:mode-coordinated"; got.GetMode() != want {
		t.Errorf("current = %q, want %q", got.GetMode(), want)
	}
}

func TestEncode_PopulatesMandatoryEventHeader(t *testing.T) {
	// openits-types:event-header makes source-device-id, occurred-at and
	// sequence mandatory, and every notification adds a mandatory `kind`
	// identityref naming the event class. observed-by is optional but carries
	// real meaning here: the collector INFERS mode changes by diffing polls
	// rather than receiving them, which is exactly the case the leaf exists
	// for, so occurred-at is the observer's clock.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFree,
		To:   model.ModeCoordinated,
	}, "cabinet-poller-1")

	if want := "asc-main-and-5th"; got.GetSourceDeviceId() != want {
		t.Errorf("source_device_id = %q, want %q", got.GetSourceDeviceId(), want)
	}
	if want := "openits-signal-control-types:sc-mode-event-kind"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if want := "cabinet-poller-1"; got.GetObservedBy() != want {
		t.Errorf("observed_by = %q, want %q", got.GetObservedBy(), want)
	}
	if want := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC); !got.GetOccurredAt().AsTime().Equal(want) {
		t.Errorf("occurred_at = %v, want %v", got.GetOccurredAt().AsTime(), want)
	}
}

func TestEncode_SequenceIncrementsPerDeviceIndependently(t *testing.T) {
	// sequence is a mandatory per-source-device monotonic counter. Two devices
	// must not share one: consumers detect loss in transit by spotting a gap
	// in a single device's run, so a shared counter would look like constant
	// loss on both.
	em := New("cabinet-poller-1", testEpoch)
	seq := func(deviceID string) uint64 {
		t.Helper()
		enc, ok, err := em.Encode(model.ModeChanged{
			Base: base(deviceID, "asc"), From: model.ModeFree, To: model.ModeCoordinated,
		})
		if err != nil || !ok {
			t.Fatalf("Encode(%s) ok=%v err=%v", deviceID, ok, err)
		}
		var m commonv1.ModeChanged
		if err := proto.Unmarshal(enc.Data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m.GetSequence()
	}

	// Starts at 1, not 0: proto3 omits zero values from the wire, so a first
	// event numbered 0 would be indistinguishable from the field being absent.
	if got := seq("asc-a"); got != 1 {
		t.Errorf("first event on asc-a: sequence = %d, want 1", got)
	}
	if got := seq("asc-a"); got != 2 {
		t.Errorf("second event on asc-a: sequence = %d, want 2", got)
	}
	if got := seq("asc-b"); got != 1 {
		t.Errorf("first event on asc-b: sequence = %d, want 1 (independent counter)", got)
	}
	if got := seq("asc-a"); got != 3 {
		t.Errorf("third event on asc-a: sequence = %d, want 3", got)
	}
}

func TestEncode_SequenceIsSafeUnderConcurrentDevices(t *testing.T) {
	// Runners call Encode concurrently, one goroutine per device. Without
	// guarding, concurrent map writes crash the process outright rather than
	// merely producing a wrong number.
	em := New("cabinet-poller-1", testEpoch)
	const devices, perDevice = 8, 50

	var wg sync.WaitGroup
	seen := make([][]uint64, devices)
	for d := range devices {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			id := fmt.Sprintf("asc-%d", d)
			for range perDevice {
				enc, ok, err := em.Encode(model.ModeChanged{
					Base: base(id, "asc"), From: model.ModeFree, To: model.ModeCoordinated,
				})
				if err != nil || !ok {
					t.Errorf("Encode(%s): ok=%v err=%v", id, ok, err)
					return
				}
				var m commonv1.ModeChanged
				if err := proto.Unmarshal(enc.Data, &m); err != nil {
					t.Errorf("unmarshal: %v", err)
					return
				}
				seen[d] = append(seen[d], m.GetSequence())
			}
		}(d)
	}
	wg.Wait()

	// Each device must see exactly 1..perDevice with no gaps or repeats.
	for d := range devices {
		got := append([]uint64(nil), seen[d]...)
		slices.Sort(got)
		for i, v := range got {
			if v != uint64(i+1) {
				t.Fatalf("device %d: sequence[%d] = %d, want %d (gap or duplicate)", d, i, v, i+1)
			}
		}
	}
}

// decodeFaultRaised encodes ev and unmarshals the wire payload back.
func decodeFaultRaised(t *testing.T, ev model.Event) (*commonv1.FaultRaised, string) {
	t.Helper()
	enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(ev)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatalf("emitter did not claim %s on a %s device", ev.EventKind(), ev.EventDeviceKind())
	}
	var got commonv1.FaultRaised
	if err := proto.Unmarshal(enc.Data, &got); err != nil {
		t.Fatalf("payload is not a valid common/v1.FaultRaised: %v", err)
	}
	return &got, enc.CEType
}

func TestEncode_SharedFaultEvent_RoutesByDeviceKind(t *testing.T) {
	// THE reason dispatch is keyed on the tuple. One domain type, one proto
	// message, two ce-types — discriminated only by DeviceKind. Keying on
	// EventKind alone would collapse these two rows into one.
	fault := func(deviceKind string) model.FaultRaised {
		return model.FaultRaised{
			Base:     base("dev-1", deviceKind),
			FaultID:  "mmu-fault",
			Severity: model.SeverityCritical,
			Category: model.CategoryConflict,
		}
	}

	if _, ceType := decodeFaultRaised(t, fault("asc")); ceType != "openits.signal-control.fault-raised.v1" {
		t.Errorf("asc fault ce-type = %q, want openits.signal-control.fault-raised.v1", ceType)
	}
	if _, ceType := decodeFaultRaised(t, fault("dms")); ceType != "openits.dms.fault-raised.v1" {
		t.Errorf("dms fault ce-type = %q, want openits.dms.fault-raised.v1", ceType)
	}
}

func TestEncode_FaultRaised_MapsSeverityAndCategory(t *testing.T) {
	got, _ := decodeFaultRaised(t, model.FaultRaised{
		Base:        base("asc-main-and-5th", "asc"),
		FaultID:     "mmu-fault",
		Severity:    model.SeverityCritical,
		Category:    model.CategoryConflict,
		Description: "conflict monitor tripped",
	})

	if got.GetFaultId() != "mmu-fault" {
		t.Errorf("fault_id = %q, want mmu-fault", got.GetFaultId())
	}
	if got.GetSeverity() != commonv1.FaultSeverity_FAULT_SEVERITY_CRITICAL {
		t.Errorf("severity = %v, want CRITICAL", got.GetSeverity())
	}
	// Category is where FaultCategory lands: `kind` is a mandatory identityref
	// naming the fault class, per-service.
	if want := "openits-signal-control-types:sc-fault-conflict"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if got.GetDescription() != "conflict monitor tripped" {
		t.Errorf("description = %q", got.GetDescription())
	}
}

func decodeDMSMode(t *testing.T, ev model.Event) (*commonv1.ModeChanged, string) {
	t.Helper()
	enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(ev)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatalf("emitter did not claim %s on a %s device", ev.EventKind(), ev.EventDeviceKind())
	}
	var got commonv1.ModeChanged
	if err := proto.Unmarshal(enc.Data, &got); err != nil {
		t.Fatalf("payload is not a valid common/v1.ModeChanged: %v", err)
	}
	return &got, enc.CEType
}

// openits-models v0.8.0 binds DMS mode-changed to sign mode, so only the
// display axis rides it. A control-mode change has no ce-type until upstream
// moves it to control-source: declined, so it drops loudly (ADR 0024).
func TestEncode_DMSModeChangedCarriesDisplayStateOnly(t *testing.T) {
	disp, dispType := decodeDMSMode(t, model.DMSDisplayStateChanged{
		Base: base("dms-i35-mm214", "dms"),
		From: model.DisplayNormal,
		To:   model.DisplayBlank,
	})
	if want := "openits.dms.mode-changed.v1"; dispType != want {
		t.Errorf("display ce-type = %q, want %q", dispType, want)
	}
	if w := "openits-dms-types:dms-mode-event-kind"; disp.GetKind() != w {
		t.Errorf("kind = %q, want %q", disp.GetKind(), w)
	}
	if w := "openits-dms-types:mode-blank"; disp.GetMode() != w {
		t.Errorf("display current = %q, want %q", disp.GetMode(), w)
	}
	if w := "openits-dms-types:mode-normal"; disp.GetPreviousMode() != w {
		t.Errorf("display prior = %q, want %q", disp.GetPreviousMode(), w)
	}

	if _, ok, _ := New("x", testEpoch).Encode(model.DMSControlModeChanged{
		Base: base("dms-i35-mm214", "dms"), From: model.ControlCentral, To: model.ControlLocal,
	}); ok {
		t.Error("a DMS control-mode change was claimed; v0.8.0 has no ce-type for it")
	}
}

func TestEncode_DMSDisplayUnknown_IsMappable(t *testing.T) {
	// Unlike controller mode, sign-mode HAS a mode-unknown identity, so an
	// unknown display state is expressible and must not be declined.
	got, _ := decodeDMSMode(t, model.DMSDisplayStateChanged{
		Base: base("dms-i35-mm214", "dms"),
		From: model.DisplayNormal,
		To:   model.DisplayUnknown,
	})
	if w := "openits-dms-types:mode-unknown"; got.GetMode() != w {
		t.Errorf("current = %q, want %q", got.GetMode(), w)
	}
}

// encodeOK is the generic "claim it and hand me the bytes" helper.
func encodeOK(t *testing.T, ev model.Event, into proto.Message) string {
	t.Helper()
	enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(ev)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if !ok {
		t.Fatalf("emitter did not claim %s on a %s device", ev.EventKind(), ev.EventDeviceKind())
	}
	if err := proto.Unmarshal(enc.Data, into); err != nil {
		t.Fatalf("payload did not unmarshal: %v", err)
	}
	return enc.CEType
}

func TestEncode_FaultCleared_RoutesByDeviceKindToo(t *testing.T) {
	for deviceKind, want := range map[string]string{
		"asc": "openits.signal-control.fault-cleared.v1",
		"dms": "openits.dms.fault-cleared.v1",
	} {
		var got commonv1.FaultCleared
		ceType := encodeOK(t, model.FaultCleared{
			Base: base("dev-1", deviceKind), FaultID: "mmu-fault",
		}, &got)
		if ceType != want {
			t.Errorf("%s: ce-type = %q, want %q", deviceKind, ceType, want)
		}
		if got.GetFaultId() != "mmu-fault" {
			t.Errorf("%s: fault_id = %q", deviceKind, got.GetFaultId())
		}
	}
}

func TestEncode_PlanChanged_MapsToPlanApplied(t *testing.T) {
	var got scv1.PlanApplied
	ceType := encodeOK(t, model.PlanChanged{
		Base: base("asc-1", "asc"), FromPlanID: 2, ToPlanID: 5,
	}, &got)

	if want := "openits.signal-control.plan-applied.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	// PlanApplied carries only the plan now in force. FromPlanID has no wire
	// home and is dropped — the transition is implicit in the event stream.
	if got.GetPlanId() != 5 {
		t.Errorf("plan_id = %d, want 5", got.GetPlanId())
	}
}

func TestEncode_OperationalStatusReport_CarriesWhatTheDomainKnows(t *testing.T) {
	var got scv1.OperationalStatusReport
	ceType := encodeOK(t, model.OperationalStatusReport{
		Base: base("asc-1", "asc"), CabinetOutput: model.OutputFlash,
		FlashCause: model.FlashMonitor, PreemptionActive: true, ActivePlanID: 3,
	}, &got)

	if want := "openits.signal-control.operational-status-report.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-signal-control-types:sc-operational-status-report"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if got.GetCabinetOutput() != scv1.CabinetOutput_CABINET_OUTPUT_FLASH {
		t.Errorf("cabinet_output = %v, want FLASH", got.GetCabinetOutput())
	}
	if want := "openits-signal-control-types:flash-monitor"; got.GetFlashCause() != want {
		t.Errorf("flash_cause = %q, want %q", got.GetFlashCause(), want)
	}
	if !got.GetPreemptActive() {
		t.Error("preempt_active = false, want true")
	}
	// Coordination was not read, so mode is absent rather than mode-unknown;
	// what no adapter reads (oper-status, monitor, uptime) is absent too.
	if got.Mode != nil || got.OperStatus != nil || got.MonitorStatus != nil || got.UptimeS != nil {
		t.Errorf("unread leaves present: mode=%v oper=%v monitor=%v uptime=%v", got.Mode, got.OperStatus, got.MonitorStatus, got.UptimeS)
	}
}

func TestEncode_CabinetOutputChanged(t *testing.T) {
	var got scv1.CabinetOutputChanged
	ceType := encodeOK(t, model.CabinetOutputChanged{
		Base: base("asc-1", "asc"), From: model.OutputNormal, To: model.OutputFlash, Cause: model.FlashMonitor,
	}, &got)
	if want := "openits.signal-control.cabinet-output-changed.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-signal-control-types:sc-cabinet-output-changed"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if got.GetPreviousCabinetOutput() != scv1.CabinetOutput_CABINET_OUTPUT_NORMAL || got.GetCabinetOutput() != scv1.CabinetOutput_CABINET_OUTPUT_FLASH {
		t.Errorf("output = %v → %v, want NORMAL → FLASH", got.GetPreviousCabinetOutput(), got.GetCabinetOutput())
	}
	if want := "openits-signal-control-types:flash-monitor"; got.GetFlashCause() != want {
		t.Errorf("flash_cause = %q, want %q", got.GetFlashCause(), want)
	}

	// From unknown: previous is absent, not UNKNOWN.
	var fromUnknown scv1.CabinetOutputChanged
	encodeOK(t, model.CabinetOutputChanged{Base: base("asc-1", "asc"), To: model.OutputNormal}, &fromUnknown)
	if fromUnknown.PreviousCabinetOutput != nil || fromUnknown.FlashCause != nil {
		t.Errorf("from unknown: previous=%v cause=%v, want both absent", fromUnknown.PreviousCabinetOutput, fromUnknown.FlashCause)
	}
	// To unknown is never claimed (the differ does not emit it; ADR 0024).
	if _, ok, _ := New("x", testEpoch).Encode(model.CabinetOutputChanged{Base: base("asc-1", "asc"), From: model.OutputFlash}); ok {
		t.Error("a transition to unknown cabinet output was claimed")
	}
}

// comm-lost must carry last-contact (openits-models v0.8.0). With one it goes
// to the catalog; without one (never answered since boot) the catalog
// emitter declines it so the collector-owned fallback reports the loss
// without inventing a time (ADR 0024).
func TestEncode_CommLostNeedsALastContact(t *testing.T) {
	contact := time.Date(2026, 7, 22, 11, 59, 0, 0, time.UTC)
	var got commonv1.CommHealthEvent
	encodeOK(t, model.DeviceStatusChanged{Base: base("asc-1", "asc"), LastContact: contact}, &got)
	if !got.GetLastContact().AsTime().Equal(contact) {
		t.Errorf("last_contact = %v, want %v", got.GetLastContact().AsTime(), contact)
	}
	if _, ok, _ := New("x", testEpoch).Encode(model.DeviceStatusChanged{Base: base("asc-1", "asc")}); ok {
		t.Error("comm-lost with no last contact was claimed; it must fall back to the collector-owned event")
	}
	var restored commonv1.CommHealthEvent
	encodeOK(t, model.DeviceStatusChanged{Base: base("asc-1", "asc"), Reachable: true}, &restored)
	if restored.LastContact != nil {
		t.Error("comm-restored carried last_contact; it is defined only for comm-lost")
	}
}

func TestEncode_FaultsCarryFirstObserved(t *testing.T) {
	began := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	var raised commonv1.FaultRaised
	encodeOK(t, model.FaultRaised{Base: base("asc-1", "asc"), FaultID: "mmu-fault", Severity: model.SeverityCritical,
		Category: model.CategoryConflict, FirstObserved: began}, &raised)
	var cleared commonv1.FaultCleared
	encodeOK(t, model.FaultCleared{Base: base("asc-1", "asc"), FaultID: "mmu-fault", FirstObserved: began}, &cleared)
	if !raised.GetFirstObserved().AsTime().Equal(began) || !cleared.GetFirstObserved().AsTime().Equal(began) {
		t.Errorf("first_observed = %v / %v, want %v on both", raised.GetFirstObserved().AsTime(), cleared.GetFirstObserved().AsTime(), began)
	}
}

func TestEncode_PreemptionPair(t *testing.T) {
	var act scv1.PreemptionActivated
	if ceType := encodeOK(t, model.PreemptionActivated{
		Base: base("asc-1", "asc"), Source: "rail-1",
	}, &act); ceType != "openits.signal-control.preemption-activated.v1" {
		t.Errorf("activated ce-type = %q", ceType)
	}
	if act.GetSourceId() != "rail-1" {
		t.Errorf("source_id = %q, want rail-1", act.GetSourceId())
	}

	var clr scv1.PreemptionCleared
	if ceType := encodeOK(t, model.PreemptionCleared{
		Base: base("asc-1", "asc"),
	}, &clr); ceType != "openits.signal-control.preemption-cleared.v1" {
		t.Errorf("cleared ce-type = %q", ceType)
	}
}

func TestEncode_DetectorReport_RoundsIntervalAndFormatsOccupancy(t *testing.T) {
	var got scv1.DetectorReport
	ceType := encodeOK(t, model.DetectorReport{
		Base:             base("asc-1", "asc"),
		IntervalStart:    time.Date(2026, 7, 22, 11, 59, 30, 0, time.UTC),
		IntervalDuration: 30500 * time.Millisecond,
		Readings: []model.DetectorReading{
			{Channel: 1, VolumeDelta: 42, OccupancyTenths: 125},
			{Channel: 2, VolumeDelta: 0, OccupancyTenths: 0},
		},
	}, &got)

	if want := "openits.signal-control.detector-report.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	// The wire constrains interval to whole seconds; the domain carries the
	// true elapsed time. Rounding happens here, once, at the edge.
	if got.GetIntervalDurationS() != 31 {
		t.Errorf("interval_duration_s = %d, want 31 (30.5s rounded)", got.GetIntervalDurationS())
	}
	if len(got.GetDetector()) != 2 {
		t.Fatalf("detector count = %d, want 2", len(got.GetDetector()))
	}
	// Occupancy is a YANG decimal64, so a string on the wire — 125 tenths is
	// "12.5" percent, not the integer 125.
	if w := "12.5"; got.GetDetector()[0].GetOccupancy() != w {
		t.Errorf("occupancy = %q, want %q", got.GetDetector()[0].GetOccupancy(), w)
	}
	if got.GetDetector()[0].GetVolume() != 42 {
		t.Errorf("volume = %d, want 42", got.GetDetector()[0].GetVolume())
	}
	if w := "0.0"; got.GetDetector()[1].GetOccupancy() != w {
		t.Errorf("zero occupancy = %q, want %q", got.GetDetector()[1].GetOccupancy(), w)
	}
}

func TestEncode_DMSMessageActivationFailed(t *testing.T) {
	var got dmsv1.MessageActivationFailed
	ceType := encodeOK(t, model.DMSMessageActivationFailed{
		Base:          base("dms-1", "dms"),
		MemoryType:    model.MemoryChangeable,
		Slot:          7,
		Error:         model.SyntaxErrorFontNotFound,
		ErrorPosition: 12,
	}, &got)

	if want := "openits.dms.message-activation-failed.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if got.GetAttemptedMemoryType() != dmsv1.MessageMemoryType_MESSAGE_MEMORY_TYPE_CHANGEABLE {
		t.Errorf("memory_type = %v", got.GetAttemptedMemoryType())
	}
	if got.GetAttemptedSlotNumber() != 7 {
		t.Errorf("slot = %d, want 7", got.GetAttemptedSlotNumber())
	}
	if want := "openits-dms-types:activation-error-font-not-defined"; got.GetErrorType() != want {
		t.Errorf("error_type = %q, want %q", got.GetErrorType(), want)
	}
	if got.GetErrorPosition() != 12 {
		t.Errorf("error_position = %d, want 12", got.GetErrorPosition())
	}
}

func TestCETypes_IsCompleteSortedAndDeduped(t *testing.T) {
	got := New("cabinet-poller-1", testEpoch).CETypes()

	want := []string{
		"openits.cctv.fault-cleared.v1",
		"openits.cctv.fault-raised.v1",
		"openits.cctv.mode-changed.v1",
		"openits.cctv.tour-state-changed.v1",
		"openits.dms.fault-cleared.v1",
		"openits.dms.fault-raised.v1",
		"openits.dms.message-activation-failed.v1",
		"openits.dms.message-changed.v1",
		"openits.dms.mode-changed.v1",
		"openits.dms.sign-status-report.v1",
		"openits.perception.fault-cleared.v1",
		"openits.perception.fault-raised.v1",
		"openits.perception.zone-incident-cleared.v1",
		"openits.perception.zone-incident-detected.v1",
		"openits.perception.zone-incident-updated.v1",
		"openits.perception.zone-interval-report.v1",
		"openits.reversible-lane.fault-cleared.v1",
		"openits.reversible-lane.fault-raised.v1",
		"openits.reversible-lane.gate-status-changed.v1",
		"openits.signal-control.cabinet-output-changed.v1",
		"openits.signal-control.comm-health-event.v1",
		"openits.signal-control.detector-report.v1",
		"openits.signal-control.fault-cleared.v1",
		"openits.signal-control.fault-raised.v1",
		"openits.signal-control.mode-changed.v1",
		"openits.signal-control.operational-status-report.v1",
		"openits.signal-control.plan-applied.v1",
		"openits.signal-control.preemption-activated.v1",
		"openits.signal-control.preemption-cleared.v1",
		"openits.traffic-sensor.fault-cleared.v1",
		"openits.traffic-sensor.fault-raised.v1",
		"openits.traffic-sensor.traffic-interval-report.v1",
		"openits.zone-occupancy.zone-occupancy-interval-report.v1",
	}
	if len(got) != len(want) {
		t.Fatalf("CETypes() has %d entries, want %d:\n got: %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CETypes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("CETypes() is not sorted: %q", got)
	}
}

func TestCETypes_CoversEveryRoutableCEType(t *testing.T) {
	// Under-reporting here defeats boot validation SILENTLY: app.Run renders
	// every reported ce-type through the operator's subject template to prove
	// each one routes to a legal subject. A ce-type the emitter can produce but
	// does not report is one that was never checked, and it surfaces at 3am as
	// an unroutable event rather than at boot.
	reported := make(map[string]bool)
	for _, ct := range New("cabinet-poller-1", testEpoch).CETypes() {
		reported[ct] = true
	}
	for k, ceType := range ceTypeFor {
		if !reported[ceType] {
			t.Errorf("ceTypeFor[%v] = %q is routable but absent from CETypes()", k, ceType)
		}
	}
}

func TestEncode_CarriesDataSchemaPinnedToDefiningEventsModule(t *testing.T) {
	// ce-dataschema keys on the module that DEFINES the notification and that
	// module's revision — never a base or types module the payload happens to
	// compose. mode-changed and fault-raised are defined in the shared
	// openits-common-*-events modules, so a signal-control event points at a
	// common module, which looks wrong until you know the rule.
	for _, tc := range []struct {
		name string
		ev   model.Event
		want string
	}{{
		"asc mode-changed is defined by common-mode-events",
		model.ModeChanged{Base: base("asc-1", "asc"), From: model.ModeFree, To: model.ModeCoordinated},
		"https://schemas.open-its.org/openits-common-mode-events/2026-10-17/",
	}, {
		"dms mode-changed shares that same defining module",
		model.DMSDisplayStateChanged{Base: base("dms-1", "dms"), From: model.DisplayNormal, To: model.DisplayBlank},
		"https://schemas.open-its.org/openits-common-mode-events/2026-10-17/",
	}, {
		"fault-raised is defined by common-fault-events",
		model.FaultRaised{Base: base("asc-1", "asc"), FaultID: "f1"},
		"https://schemas.open-its.org/openits-common-fault-events/2026-10-14/",
	}, {
		"plan-applied is defined by signal-control-events",
		model.PlanChanged{Base: base("asc-1", "asc"), ToPlanID: 1},
		"https://schemas.open-its.org/openits-signal-control-events/2026-10-17/",
	}, {
		"message-activation-failed is defined by dms-events, at its own revision",
		model.DMSMessageActivationFailed{Base: base("dms-1", "dms")},
		"https://schemas.open-its.org/openits-dms-events/2026-10-13/",
	}, {
		"message-changed is defined by dms-events, at the v0.4.0 revision",
		model.DMSMessageChanged{Base: base("dms-1", "dms"), ToMemoryType: model.MemoryChangeable, ToSlot: 1},
		"https://schemas.open-its.org/openits-dms-events/2026-10-13/",
	}, {
		"sign-status-report is defined by dms-events, at the v0.4.0 revision",
		model.DMSSignStatusReport{Base: base("dms-1", "dms")},
		"https://schemas.open-its.org/openits-dms-events/2026-10-13/",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(tc.ev)
			if err != nil || !ok {
				t.Fatalf("Encode: ok=%v err=%v", ok, err)
			}
			if enc.DataSchema != tc.want {
				t.Errorf("ce-dataschema = %q, want %q", enc.DataSchema, tc.want)
			}
		})
	}
}

func TestDataSchema_CoversEveryCEType(t *testing.T) {
	// A ce-type with no constant would publish without ce-dataschema, which
	// the profile requires. Catching that here beats catching it per-event.
	for _, ceType := range New("x", testEpoch).CETypes() {
		if dataSchemaFor[ceType] == "" {
			t.Errorf("ce-type %q has no ce-dataschema constant", ceType)
		}
	}
}

func TestEncode_IdentityExcludesProducerAssignedLeaves(t *testing.T) {
	// ce-id is derived from Identity, not Data. Two encodings of the SAME
	// occurrence differ in sequence (the counter advances) and would otherwise
	// produce different ids, destroying restart-invariance and replay dedup.
	em := New("cabinet-poller-1", testEpoch)
	ev := model.ModeChanged{
		Base: base("asc-1", "asc"), From: model.ModeFree, To: model.ModeCoordinated,
	}

	first, _, _ := em.Encode(ev)
	second, _, _ := em.Encode(ev)

	if string(first.Data) == string(second.Data) {
		t.Fatal("precondition: the two encodings should differ, because sequence advanced")
	}
	if string(first.Identity) != string(second.Identity) {
		t.Errorf("identity differs across encodings of one occurrence:\n %x\n %x",
			first.Identity, second.Identity)
	}

	// Identity must still carry the substance — clearing bookkeeping is not
	// licence to hash an empty message.
	var id commonv1.ModeChanged
	if err := proto.Unmarshal(first.Identity, &id); err != nil {
		t.Fatalf("identity is not a valid payload: %v", err)
	}
	if id.GetMode() != "openits-signal-control-types:mode-coordinated" {
		t.Errorf("identity lost the event's substance: current = %q", id.GetMode())
	}
	if id.GetSequence() != 0 || id.GetObservedBy() != "" {
		t.Errorf("identity retained producer-assigned leaves: sequence=%d observed_by=%q",
			id.GetSequence(), id.GetObservedBy())
	}
}

func TestEncode_IdentityIsSensitiveToRealChanges(t *testing.T) {
	// The flip side: clearing bookkeeping must not blur genuinely different
	// events into one id.
	em := New("cabinet-poller-1", testEpoch)
	a, _, _ := em.Encode(model.ModeChanged{
		Base: base("asc-1", "asc"), From: model.ModeFree, To: model.ModeCoordinated,
	})
	b, _, _ := em.Encode(model.ModeChanged{
		Base: base("asc-1", "asc"), From: model.ModeCoordinated, To: model.ModeFree,
	})
	if string(a.Identity) == string(b.Identity) {
		t.Error("different transitions produced the same identity")
	}
}

func TestEncode_SharedFaultsRouteForEveryServedDeviceKind(t *testing.T) {
	// model.FaultSet and the fault differ carry no device kind — it arrives
	// from the snapshot — so fault coverage for a new device kind is a
	// routing-table change, not new domain surface. This is what lets the
	// model->NATS path be complete before any adapter exists.
	for _, tc := range []struct {
		deviceKind, raised, cleared string
		category                    model.FaultCategory
		ft                          model.FaultType
	}{
		{"asc", "openits.signal-control.fault-raised.v1", "openits.signal-control.fault-cleared.v1", model.CategoryCommunication, model.FaultTypeUnknown},
		{"dms", "openits.dms.fault-raised.v1", "openits.dms.fault-cleared.v1", model.CategoryCommunication, model.FaultTypeUnknown},
		{"cctv", "openits.cctv.fault-raised.v1", "openits.cctv.fault-cleared.v1", model.CategoryCommunication, model.FaultTypeUnknown},
		{"traffic-sensor", "openits.traffic-sensor.fault-raised.v1", "openits.traffic-sensor.fault-cleared.v1", model.CategoryCommunication, model.FaultTypeUnknown},
		{"perception", "openits.perception.fault-raised.v1", "openits.perception.fault-cleared.v1", model.CategoryCommunication, model.FaultTypeUnknown},
		// ACS never falls back to the service base kind; pick a declared leaf.
		{"acs", "openits.reversible-lane.fault-raised.v1", "openits.reversible-lane.fault-cleared.v1", model.CategoryCabinet, model.FaultTypePLCNotScanning},
	} {
		t.Run(tc.deviceKind, func(t *testing.T) {
			var raised commonv1.FaultRaised
			if ct := encodeOK(t, model.FaultRaised{
				Base: base("dev-1", tc.deviceKind), FaultID: "f1",
				Severity: model.SeverityMajor, Category: tc.category, Type: tc.ft,
			}, &raised); ct != tc.raised {
				t.Errorf("raised ce-type = %q, want %q", ct, tc.raised)
			}
			// kind is mandatory, so every served kind needs a real identity.
			if raised.GetKind() == "" {
				t.Error("fault-raised carries no kind identity")
			}
			var cleared commonv1.FaultCleared
			if ct := encodeOK(t, model.FaultCleared{
				Base: base("dev-1", tc.deviceKind), FaultID: "f1",
				Category: tc.category, Type: tc.ft,
			}, &cleared); ct != tc.cleared {
				t.Errorf("cleared ce-type = %q, want %q", ct, tc.cleared)
			}
		})
	}
}

func TestFaultKindIdentity_UnmappedCategoryFallsBackPerService(t *testing.T) {
	// The domain's FaultCategory vocabulary was designed for signals and
	// signs — conflict, cabinet, lamp, pixel. It has nothing for video loss,
	// occlusion, or pose drift, so most faults on the new device kinds land on
	// the service BASE identity. That is the honest rendering ("a fault, class
	// unmapped"), not a gap to paper over with a near-neighbour.
	for deviceKind, want := range map[string]string{
		"cctv":           "openits-cctv-types:cctv-fault-event-kind",
		"traffic-sensor": "openits-traffic-sensor-types:traffic-sensor-fault-event-kind",
		"perception":     "openits-perception-types:perception-fault-event-kind",
	} {
		got, ok := faultKindIdentity(model.CategoryConflict, deviceKind, model.FaultTypeUnknown)
		if !ok {
			t.Errorf("%s: no fault identity at all", deviceKind)
			continue
		}
		if got != want {
			t.Errorf("%s: kind = %q, want %q", deviceKind, got, want)
		}
	}
}

func TestFaultKindIdentity_ACSMapsByFaultType(t *testing.T) {
	mapped := map[model.FaultType]string{
		model.FaultTypeGateFailedToClose:     "openits-reversible-lane-types:reversible-lane-fault-gate-failed-to-close",
		model.FaultTypeGateFailedToOpen:      "openits-reversible-lane-types:reversible-lane-fault-gate-failed-to-open",
		model.FaultTypeGateEmergencyStop:     "openits-reversible-lane-types:reversible-lane-fault-gate-emergency-stop",
		model.FaultTypeGateInvalidInputs:     "openits-vendor-cameleon-reversible-lane-types:cameleon-gate-invalid-inputs",
		model.FaultTypeGateCommandRefused:    "openits-reversible-lane-types:reversible-lane-fault-interlock",
		model.FaultTypePLCNotScanning:        "openits-reversible-lane-types:reversible-lane-fault-plc-communication",
		model.FaultTypePLCFault:              "openits-reversible-lane-types:reversible-lane-fault-controller",
		model.FaultTypePLCCabinetIDMismatch:  "openits-reversible-lane-types:reversible-lane-fault-controller-configuration",
		model.FaultTypePLCProgramCRCMismatch: "openits-reversible-lane-types:reversible-lane-fault-controller-configuration",
		model.FaultTypePLCConfigCRCMismatch:  "openits-reversible-lane-types:reversible-lane-fault-controller-configuration",
		model.FaultTypeCabinetLineFilter:     "openits-reversible-lane-types:reversible-lane-fault-power",
		model.FaultTypeCabinetSurgeProtector: "openits-reversible-lane-types:reversible-lane-fault-power",
		model.FaultTypeCabinetDoor:           "openits-reversible-lane-types:reversible-lane-fault-cabinet-door-open",
	}
	for ft, want := range mapped {
		got, ok := faultKindIdentity(model.CategoryCabinet, "acs", ft)
		if !ok || got != want {
			t.Errorf("%s: got (%q, %v), want %q", ft, got, ok, want)
		}
	}
	for _, ft := range []model.FaultType{model.FaultTypeCabinetGateEstop, model.FaultTypePLCCabinetIDChanged, model.FaultTypeUnknown} {
		if got, ok := faultKindIdentity(model.CategoryCabinet, "acs", ft); ok {
			t.Errorf("%s: mapped to %q; want declined", ft, got)
		}
	}
	if got, ok := faultKindIdentity(model.CategoryUnknown, "acs", model.FaultTypeGateFailedToClose); ok {
		t.Errorf("unknown category: got (%q, %v), want declined", got, ok)
	}
}

func TestEncode_GateFaultNamesComponent(t *testing.T) {
	raised, ceType := decodeFaultRaised(t, model.FaultRaised{
		Base:     base("acs-1", "acs"),
		FaultID:  "gate/WG-1/estop",
		Severity: model.SeverityCritical,
		Category: model.CategoryCabinet,
		Type:     model.FaultTypeGateEmergencyStop,
		GateID:   "WG-1",
	})
	if ceType != "openits.reversible-lane.fault-raised.v1" {
		t.Errorf("ce-type = %q", ceType)
	}
	if raised.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-gate-emergency-stop" {
		t.Errorf("kind = %q", raised.GetKind())
	}
	comp := raised.GetComponent()
	if comp.GetComponentClass() != "openits-reversible-lane-types:rl-component-gate" || comp.GetComponentId() != "WG-1" {
		t.Fatalf("component = %+v", comp)
	}

	enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(model.FaultCleared{
		Base: base("acs-1", "acs"), FaultID: "gate/WG-1/estop", Category: model.CategoryCabinet,
		Type: model.FaultTypeGateEmergencyStop, GateID: "WG-1",
	})
	if err != nil || !ok {
		t.Fatalf("clear Encode: ok=%v err=%v", ok, err)
	}
	var cleared commonv1.FaultCleared
	if err := proto.Unmarshal(enc.Data, &cleared); err != nil {
		t.Fatal(err)
	}
	comp = cleared.GetComponent()
	if comp.GetComponentClass() != "openits-reversible-lane-types:rl-component-gate" || comp.GetComponentId() != "WG-1" {
		t.Fatalf("clear component = %+v", comp)
	}

	refused, ceType := decodeFaultRaised(t, model.FaultRaised{
		Base: base("acs-1", "acs"), FaultID: "gate/WG-1/command-refused",
		Severity: model.SeverityWarning, Category: model.CategoryCabinet,
		Type: model.FaultTypeGateCommandRefused, GateID: "WG-1",
	})
	if ceType != "openits.reversible-lane.fault-raised.v1" {
		t.Errorf("command-refused ce-type = %q", ceType)
	}
	if refused.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-interlock" {
		t.Errorf("command-refused kind = %q", refused.GetKind())
	}
	comp = refused.GetComponent()
	if comp.GetComponentClass() != "openits-reversible-lane-types:rl-component-gate" || comp.GetComponentId() != "WG-1" {
		t.Errorf("command-refused component = %+v", comp)
	}

	invalid, ceType := decodeFaultRaised(t, model.FaultRaised{
		Base: base("acs-1", "acs"), FaultID: "gate/WG-1/invalid-inputs",
		Severity: model.SeverityMajor, Category: model.CategoryCabinet,
		Type: model.FaultTypeGateInvalidInputs, GateID: "WG-1",
	})
	if ceType != "openits.reversible-lane.fault-raised.v1" {
		t.Errorf("invalid-inputs ce-type = %q", ceType)
	}
	if invalid.GetKind() != "openits-vendor-cameleon-reversible-lane-types:cameleon-gate-invalid-inputs" {
		t.Errorf("invalid-inputs kind = %q", invalid.GetKind())
	}
	comp = invalid.GetComponent()
	if comp.GetComponentClass() != "openits-reversible-lane-types:rl-component-gate" || comp.GetComponentId() != "WG-1" {
		t.Errorf("invalid-inputs component = %+v", comp)
	}

	door, _ := decodeFaultRaised(t, model.FaultRaised{
		Base: base("acs-1", "acs"), FaultID: "cabinet/door",
		Severity: model.SeverityWarning, Category: model.CategoryCabinet,
		Type: model.FaultTypeCabinetDoor,
	})
	if door.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-cabinet-door-open" {
		t.Errorf("cabinet/door kind = %q", door.GetKind())
	}
	if door.GetComponent() != nil {
		t.Errorf("cabinet/door component = %+v, want nil", door.GetComponent())
	}

	enc, ok, err = New("cabinet-poller-1", testEpoch).Encode(model.FaultRaised{
		Base: base("acs-1", "acs"), FaultID: "cabinet/gate-estop",
		Severity: model.SeverityCritical, Category: model.CategoryCabinet,
		Type: model.FaultTypeCabinetGateEstop,
	})
	if err != nil || ok || enc != nil {
		t.Errorf("cabinet/gate-estop: ok=%v err=%v; want declined", ok, err)
	}
}

func TestACSFaultKindIgnoresFaultIDString(t *testing.T) {
	raised, _ := decodeFaultRaised(t, model.FaultRaised{
		Base:     base("acs-1", "acs"),
		FaultID:  "renamed/not-a-gate-id/failed-to-close",
		Severity: model.SeverityCritical,
		Category: model.CategoryCabinet,
		Type:     model.FaultTypeGateEmergencyStop,
		GateID:   "WG-9",
	})
	if raised.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-gate-emergency-stop" {
		t.Fatalf("kind = %q", raised.GetKind())
	}
	comp := raised.GetComponent()
	if comp.GetComponentClass() != "openits-reversible-lane-types:rl-component-gate" || comp.GetComponentId() != "WG-9" {
		t.Fatalf("component = %+v", comp)
	}
}

func TestACSFaultRaiseAndClearShareKind(t *testing.T) {
	mapped := []struct {
		id     string
		ft     model.FaultType
		gateID string
	}{
		{"gate/WG-1/failed-to-close", model.FaultTypeGateFailedToClose, "WG-1"},
		{"gate/WG-1/failed-to-open", model.FaultTypeGateFailedToOpen, "WG-1"},
		{"gate/WG-1/estop", model.FaultTypeGateEmergencyStop, "WG-1"},
		{"gate/WG-1/command-refused", model.FaultTypeGateCommandRefused, "WG-1"},
		{"gate/WG-1/invalid-inputs", model.FaultTypeGateInvalidInputs, "WG-1"},
		{"cabinet/lf-fault", model.FaultTypeCabinetLineFilter, ""},
		{"cabinet/sp-fault", model.FaultTypeCabinetSurgeProtector, ""},
		{"cabinet/door", model.FaultTypeCabinetDoor, ""},
		{"plc/fault", model.FaultTypePLCFault, ""},
		{"plc/cabinet-id-mismatch", model.FaultTypePLCCabinetIDMismatch, ""},
		{"plc/program-crc-mismatch", model.FaultTypePLCProgramCRCMismatch, ""},
		{"plc/config-crc-mismatch", model.FaultTypePLCConfigCRCMismatch, ""},
		{"plc/not-scanning", model.FaultTypePLCNotScanning, ""},
	}
	for _, tc := range mapped {
		raisedKind, ok := faultKindIdentity(model.CategoryCabinet, "acs", tc.ft)
		if !ok {
			t.Fatalf("%s: raise kind missing", tc.id)
		}
		clearedKind, ok := faultKindIdentity(model.CategoryCabinet, "acs", tc.ft)
		if !ok || clearedKind != raisedKind {
			t.Errorf("%s: raise %q clear %q", tc.id, raisedKind, clearedKind)
		}
		raised, _ := decodeFaultRaised(t, model.FaultRaised{
			Base: base("acs-1", "acs"), FaultID: tc.id,
			Severity: model.SeverityMajor, Category: model.CategoryCabinet,
			Type: tc.ft, GateID: tc.gateID,
		})
		enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(model.FaultCleared{
			Base: base("acs-1", "acs"), FaultID: tc.id, Category: model.CategoryCabinet,
			Type: tc.ft, GateID: tc.gateID,
		})
		if err != nil || !ok {
			t.Fatalf("%s clear: ok=%v err=%v", tc.id, ok, err)
		}
		var cleared commonv1.FaultCleared
		if err := proto.Unmarshal(enc.Data, &cleared); err != nil {
			t.Fatal(err)
		}
		if raised.GetKind() != cleared.GetKind() || raised.GetKind() != raisedKind {
			t.Errorf("%s encoded raise=%q clear=%q want %q", tc.id, raised.GetKind(), cleared.GetKind(), raisedKind)
		}
		if cleared.GetFaultId() != tc.id {
			t.Errorf("%s clear fault-id = %q", tc.id, cleared.GetFaultId())
		}
	}
	declined := []struct {
		id string
		ft model.FaultType
	}{
		{"cabinet/gate-estop", model.FaultTypeCabinetGateEstop},
		{"plc/cabinet-id-changed", model.FaultTypePLCCabinetIDChanged},
	}
	for _, tc := range declined {
		for _, ev := range []model.Event{
			model.FaultRaised{Base: base("acs-1", "acs"), FaultID: tc.id, Severity: model.SeverityMajor, Category: model.CategoryCabinet, Type: tc.ft},
			model.FaultCleared{Base: base("acs-1", "acs"), FaultID: tc.id, Category: model.CategoryCabinet, Type: tc.ft},
		} {
			enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(ev)
			if err != nil || ok || enc != nil {
				t.Errorf("%s %s: ok=%v err=%v; want declined both halves", tc.id, ev.EventKind(), ok, err)
			}
		}
	}
}

func TestEncode_TrafficIntervalReport(t *testing.T) {
	var got tsv1.TrafficIntervalReport
	ceType := encodeOK(t, model.TrafficIntervalReport{
		Base:             base("ts-01", "traffic-sensor"),
		IntervalStart:    time.Date(2026, 8, 9, 11, 59, 0, 0, time.UTC),
		IntervalDuration: 60 * time.Second,
		Lanes: []model.LaneMeasurement{{
			LaneID: 1, Volume: 42, OccupancyTenths: 125,
			SpeedAvgHundredthsKPH: 8850, SpeedReported: true,
			ClassVolumes: []model.LaneClassVolume{{ClassID: 2, Volume: 30}, {ClassID: 5, Volume: 12}},
			Quality:      model.QualityValid,
		}},
	}, &got)

	if want := "openits.traffic-sensor.traffic-interval-report.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-traffic-sensor-types:ts-traffic-interval-report"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if len(got.GetLane()) != 1 {
		t.Fatalf("lane count = %d", len(got.GetLane()))
	}
	l := got.GetLane()[0]
	if l.GetLaneId() != 1 || l.GetVolume() != 42 {
		t.Errorf("lane id/volume = %d/%d", l.GetLaneId(), l.GetVolume())
	}
	// decimal64 leaves are strings on the wire, same as detector occupancy.
	if l.GetOccupancy() != "12.5" {
		t.Errorf("occupancy = %q, want \"12.5\"", l.GetOccupancy())
	}
	if l.GetSpeedAverageKmh() != "88.50" {
		t.Errorf("speed = %q, want \"88.50\"", l.GetSpeedAverageKmh())
	}
	if l.GetIntervalDurationS() != 60 {
		t.Errorf("interval_duration_s = %d, want 60", l.GetIntervalDurationS())
	}
	if len(l.GetClassVolume()) != 2 || l.GetClassVolume()[0].GetClassId() != 2 {
		t.Errorf("class volumes = %+v", l.GetClassVolume())
	}
}

func TestEncode_UnreportedSpeedIsOmittedNotZero(t *testing.T) {
	// Zero km/h is a real reading — a lane of stopped traffic. Emitting "0.00"
	// for a sensor that never reported speed would turn "we don't know" into
	// "gridlock", at exactly the moment someone is looking.
	var got tsv1.TrafficIntervalReport
	encodeOK(t, model.TrafficIntervalReport{
		Base:          base("ts-01", "traffic-sensor"),
		IntervalStart: time.Date(2026, 8, 9, 11, 59, 0, 0, time.UTC),
		Lanes:         []model.LaneMeasurement{{LaneID: 1, Volume: 3, SpeedReported: false}},
	}, &got)
	if s := got.GetLane()[0].GetSpeedAverageKmh(); s != "" {
		t.Errorf("unreported speed rendered as %q, want empty", s)
	}

	var stopped tsv1.TrafficIntervalReport
	encodeOK(t, model.TrafficIntervalReport{
		Base:          base("ts-01", "traffic-sensor"),
		IntervalStart: time.Date(2026, 8, 9, 11, 59, 0, 0, time.UTC),
		Lanes:         []model.LaneMeasurement{{LaneID: 1, Volume: 3, SpeedReported: true}},
	}, &stopped)
	if s := stopped.GetLane()[0].GetSpeedAverageKmh(); s != "0.00" {
		t.Errorf("reported zero speed rendered as %q, want \"0.00\"", s)
	}
}

func TestEncode_ZoneIncidentLifecycle(t *testing.T) {
	incident := model.ZoneIncident{
		IncidentID: "i-1", ZoneID: "zone-a",
		Type: model.IncidentWrongWayVehicle, Severity: model.IncidentMajor,
		ObjectClass: model.ObjectTruck, ConfidencePercent: 92,
		SpeedHundredthsKPH: 4250, SpeedReported: true, TrackID: 7, TrackEpoch: 2,
	}

	var det pcpv1.ZoneIncidentDetected
	if ct := encodeOK(t, model.ZoneIncidentDetected{
		Base: base("lidar-01", "perception"), ZoneIncident: incident,
	}, &det); ct != "openits.perception.zone-incident-detected.v1" {
		t.Errorf("detected ce-type = %q", ct)
	}
	if want := "openits-perception-types:pcp-zone-incident-detected"; det.GetKind() != want {
		t.Errorf("kind = %q, want %q", det.GetKind(), want)
	}
	// Closed identity sets, not free text.
	if want := "openits-perception-types:incident-wrong-way-vehicle"; det.GetType() != want {
		t.Errorf("type = %q, want %q", det.GetType(), want)
	}
	if want := "openits-types:object-truck"; det.GetObjectClass() != want {
		t.Errorf("object_class = %q, want %q", det.GetObjectClass(), want)
	}
	if det.GetSeverity() != pcpv1.IncidentSeverity_INCIDENT_SEVERITY_MAJOR {
		t.Errorf("severity = %v", det.GetSeverity())
	}
	if det.GetSpeedKmh() != "42.50" {
		t.Errorf("speed = %q, want \"42.50\"", det.GetSpeedKmh())
	}
	if det.GetTrackId() != 7 || det.GetTrackEpoch() != 2 {
		t.Errorf("track = %d/%d", det.GetTrackId(), det.GetTrackEpoch())
	}

	var upd pcpv1.ZoneIncidentUpdated
	if ct := encodeOK(t, model.ZoneIncidentUpdated{
		Base: base("lidar-01", "perception"), IncidentID: "i-1", ZoneID: "zone-a",
		Severity: model.IncidentIntermediate, ConfidencePercent: 71,
	}, &upd); ct != "openits.perception.zone-incident-updated.v1" {
		t.Errorf("updated ce-type = %q", ct)
	}
	if upd.GetIncidentId() != "i-1" || upd.GetConfidence() != 71 {
		t.Errorf("updated = %+v", &upd)
	}

	var clr pcpv1.ZoneIncidentCleared
	if ct := encodeOK(t, model.ZoneIncidentCleared{
		Base: base("lidar-01", "perception"), IncidentID: "i-1", ZoneID: "zone-a",
	}, &clr); ct != "openits.perception.zone-incident-cleared.v1" {
		t.Errorf("cleared ce-type = %q", ct)
	}
	if clr.GetIncidentId() != "i-1" || clr.GetZoneId() != "zone-a" {
		t.Errorf("cleared = %+v", &clr)
	}
}

func TestEncode_StoppedVehicleZeroSpeedSurvives(t *testing.T) {
	// A stopped-vehicle incident is exactly where zero km/h is the meaningful
	// reading, so it must render rather than vanish — and an UNREPORTED speed
	// must still stay absent.
	var stopped pcpv1.ZoneIncidentDetected
	encodeOK(t, model.ZoneIncidentDetected{
		Base: base("lidar-01", "perception"),
		ZoneIncident: model.ZoneIncident{
			IncidentID: "i-1", ZoneID: "z", Type: model.IncidentStoppedVehicle,
			SpeedHundredthsKPH: 0, SpeedReported: true,
		},
	}, &stopped)
	if stopped.GetSpeedKmh() != "0.00" {
		t.Errorf("stopped vehicle speed = %q, want \"0.00\"", stopped.GetSpeedKmh())
	}

	var unknown pcpv1.ZoneIncidentDetected
	encodeOK(t, model.ZoneIncidentDetected{
		Base: base("lidar-01", "perception"),
		ZoneIncident: model.ZoneIncident{
			IncidentID: "i-2", ZoneID: "z", Type: model.IncidentStoppedVehicle,
			SpeedReported: false,
		},
	}, &unknown)
	if s := unknown.GetSpeedKmh(); s != "" {
		t.Errorf("unreported speed = %q, want empty", s)
	}
}

func TestEncode_ZoneIntervalReport(t *testing.T) {
	var got pcpv1.ZoneIntervalReport
	ceType := encodeOK(t, model.ZoneIntervalReport{
		Base:             base("lidar-01", "perception"),
		IntervalStart:    time.Date(2026, 8, 9, 11, 59, 0, 0, time.UTC),
		IntervalDuration: 60 * time.Second,
		Zones: []model.ZoneMeasurement{{
			ZoneID: "zone-a", CrossedVolume: 40, ObservedCount: 42,
			OccupancyTenths: 310, SpeedAvgHundredthsKPH: 5125, SpeedReported: true,
			CrossedClassCounts:  []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 36}, {Class: model.ObjectTruck, Count: 4}},
			ObservedClassCounts: []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 38}, {Class: model.ObjectTruck, Count: 4}},
		}},
	}, &got)

	if want := "openits.perception.zone-interval-report.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-perception-types:pcp-zone-interval-report"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	z := got.GetZone()[0]
	if z.GetCrossedVolume() != 40 {
		t.Errorf("crossed = %d, want 40", z.GetCrossedVolume())
	}
	if z.GetAverageSpeedKmh() != "51.25" {
		t.Errorf("speed = %q, want \"51.25\"", z.GetAverageSpeedKmh())
	}
	if z.GetIntervalDurationS() != 60 {
		t.Errorf("interval_duration_s = %d, want 60", z.GetIntervalDurationS())
	}
	// Counted by object-class IDENTITY here, not a numeric bin.
	// Counted by object-class IDENTITY, and that identity moved module in
	// v0.3.0: object-* was hoisted out of openits-perception-types into the
	// openits-types foundation layer. Nothing in Go catches a stale prefix
	// here -- identityrefs are plain strings -- so this assertion is the
	// check.
	cc := z.GetClassCount()
	if len(cc) != 2 || cc[1].GetClass() != "openits-types:object-truck" || cc[1].GetCount() != 4 || cc[0].GetCount() != 36 {
		t.Errorf("class counts = %+v; want the crossed breakdown (36 passenger, 4 truck)", cc)
	}
}

// TestEncode_ZoneOccupancyIntervalReport covers the presence half of the same
// interval. v0.3.0 moved observed-count and occupancy-percent off the
// perception report and into this ce-type; the test above proves they left,
// this one proves they arrived.
func TestEncode_ZoneOccupancyIntervalReport(t *testing.T) {
	var got zocv1.ZoneOccupancyIntervalReport
	ceType := encodeOK(t, model.ZoneOccupancyIntervalReport{
		Base:             base("lidar-01", "perception"),
		IntervalStart:    time.Date(2026, 8, 9, 11, 59, 0, 0, time.UTC),
		IntervalDuration: 60 * time.Second,
		Zones: []model.ZoneMeasurement{{
			ZoneID: "zone-a", CrossedVolume: 40, ObservedCount: 42,
			OccupancyTenths: 310, SpeedAvgHundredthsKPH: 5125, SpeedReported: true,
			CrossedClassCounts:  []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 36}, {Class: model.ObjectTruck, Count: 4}},
			ObservedClassCounts: []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 38}, {Class: model.ObjectTruck, Count: 4}},
		}},
	}, &got)

	if want := "openits.zone-occupancy.zone-occupancy-interval-report.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-zone-occupancy-types:zoc-zone-occupancy-interval-report"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	z := got.GetZone()[0]
	if z.GetObservedCount() != 42 {
		t.Errorf("observed = %d, want 42", z.GetObservedCount())
	}
	if z.GetOccupancyPercent() != "31.0" {
		t.Errorf("occupancy = %q, want \"31.0\"", z.GetOccupancyPercent())
	}
	if z.GetIntervalDurationS() != 60 {
		t.Errorf("interval_duration_s = %d, want 60", z.GetIntervalDurationS())
	}
	// Nothing observes a within-interval peak today, so the leaf stays unset
	// rather than being derived from the mean.
	if z.GetPeakOccupancyCount() != 0 {
		t.Errorf("peak = %d, want 0 (unobserved, not inferred)", z.GetPeakOccupancyCount())
	}
	oc := z.GetObservedClass()
	if len(oc) != 2 || oc[1].GetClass() != "openits-types:object-truck" || oc[1].GetCount() != 4 || oc[0].GetCount() != 38 {
		t.Errorf("observed classes = %+v; want the observed breakdown (38 passenger, 4 truck)", oc)
	}
	// No per-class confidence in the domain; the leaf is "not stated", and
	// zero must not read as a confidence of none.
	if oc[0].GetMeanConfidence() != 0 {
		t.Errorf("mean_confidence = %d, want 0", oc[0].GetMeanConfidence())
	}
}

func TestEncode_CCTVControlModeChanged(t *testing.T) {
	var got commonv1.ModeChanged
	ceType := encodeOK(t, model.CCTVControlModeChanged{
		Base: base("cam-03", "cctv"),
		From: model.CCTVControlCentral, To: model.CCTVControlLocal,
	}, &got)

	if want := "openits.cctv.mode-changed.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-cctv-types:cctv-mode-event-kind"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if want := "openits-cctv-types:cctv-control-central"; got.GetPreviousMode() != want {
		t.Errorf("prior = %q, want %q", got.GetPreviousMode(), want)
	}
	if want := "openits-cctv-types:cctv-control-local"; got.GetMode() != want {
		t.Errorf("current = %q, want %q", got.GetMode(), want)
	}
}

func TestEncode_CCTVTourStateChanged(t *testing.T) {
	var got cctvv1.TourStateChanged
	ceType := encodeOK(t, model.CCTVTourStateChanged{
		Base: base("cam-03", "cctv"), TourID: 4,
		From: model.TourRunning, To: model.TourPaused,
	}, &got)

	if want := "openits.cctv.tour-state-changed.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-cctv-types:cctv-tour-state-changed"; got.GetKind() != want {
		t.Errorf("kind = %q, want %q", got.GetKind(), want)
	}
	if got.GetTourId() != 4 {
		t.Errorf("tour_id = %d, want 4", got.GetTourId())
	}
	if got.GetPreviousState() != cctvv1.TourRunState_TOUR_RUN_STATE_RUNNING ||
		got.GetCurrentState() != cctvv1.TourRunState_TOUR_RUN_STATE_PAUSED {
		t.Errorf("states = %v -> %v", got.GetPreviousState(), got.GetCurrentState())
	}
}

// ADR 0019 Scope: a device's reachability is a catalog fact, published as
// <service>.comm-health-event with kind comm-lost / comm-restored. Asserted
// against the decoded wire payload, field by field.
func TestEncode_DeviceStatusChangedIsCommHealth(t *testing.T) {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		reachable bool
		kind      string
	}{
		{false, "openits-common-comm-health-events:comm-lost"},
		{true, "openits-common-comm-health-events:comm-restored"},
	} {
		enc, ok, err := New("cabinet-poller-1", testEpoch).Encode(model.DeviceStatusChanged{
			Base:      model.Base{DeviceID: "asc-1", DeviceKind: "asc", OccurredAt: at},
			Reachable: tc.reachable, Reason: "read timeout", ConsecutiveFailures: 1,
			LastContact: at.Add(-time.Minute), // comm-lost requires it (ADR 0024)
		})
		if err != nil || !ok {
			t.Fatalf("reachable=%v: ok=%v err=%v", tc.reachable, ok, err)
		}
		if enc.CEType != "openits.signal-control.comm-health-event.v1" {
			t.Fatalf("CEType = %q", enc.CEType)
		}
		var got commonv1.CommHealthEvent
		if err := proto.Unmarshal(enc.Data, &got); err != nil {
			t.Fatal(err)
		}
		if got.GetKind() != tc.kind || got.GetSourceDeviceId() != "asc-1" || got.GetObservedBy() != "cabinet-poller-1" ||
			!got.GetOccurredAt().AsTime().Equal(at) || got.GetSequence() == 0 {
			t.Fatalf("reachable=%v payload = %+v, want kind %s for asc-1 observed by cabinet-poller-1", tc.reachable, &got, tc.kind)
		}
	}
}

// A device kind whose service has no comm-health channel in the pinned
// catalog (DMS today) is declined here, so the collector-owned
// health.device-status-changed fallback claims it instead of the event being
// dropped (ADR 0019 as amended for kinds the catalog has not fanned out to).
func TestEncode_DeviceStatusChangedDeclinedWithoutACatalogChannel(t *testing.T) {
	_, ok, err := New("cabinet-poller-1", testEpoch).Encode(model.DeviceStatusChanged{
		Base: model.Base{DeviceID: "dms-1", DeviceKind: "dms", OccurredAt: time.Now()}, Reachable: false,
	})
	if err != nil || ok {
		t.Fatalf("dms device status: ok=%v err=%v, want declined", ok, err)
	}
}

// testEpoch is the sequence-epoch every test emitter stamps. It is cleared
// from identity bytes, so it shows only in Data.
const testEpoch = "01TESTEPOCH0000000000000000"

// Crossed and observed are different populations — a stopped vehicle is
// observed repeatedly but crosses once — and each report's breakdown must sum
// to its own total (openits-models conformance:
// TestPerceptionEvent_IntervalCrossedReconciles and
// TestZoneOccupancyEvent_ObservedClassReconciles). One shared breakdown could
// satisfy at most one of them; found by the Tier 2 test.
func TestEachIntervalReportCarriesItsOwnClassBreakdown(t *testing.T) {
	zone := model.ZoneMeasurement{
		ZoneID: "zone-a", CrossedVolume: 40, ObservedCount: 42, OccupancyTenths: 310,
		CrossedClassCounts:  []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 36}, {Class: model.ObjectTruck, Count: 4}},
		ObservedClassCounts: []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 38}, {Class: model.ObjectTruck, Count: 4}},
	}
	start := time.Date(2026, 8, 9, 11, 59, 0, 0, time.UTC)

	var crossed pcpv1.ZoneIntervalReport
	encodeOK(t, model.ZoneIntervalReport{Base: base("lidar-01", "perception"), IntervalStart: start,
		IntervalDuration: time.Minute, Zones: []model.ZoneMeasurement{zone}}, &crossed)
	var sum uint32
	for _, cc := range crossed.GetZone()[0].GetClassCount() {
		sum += cc.GetCount()
	}
	if sum != 40 {
		t.Errorf("zone-interval-report class-count sums to %d, want the crossed volume 40", sum)
	}

	var observed zocv1.ZoneOccupancyIntervalReport
	encodeOK(t, model.ZoneOccupancyIntervalReport{Base: base("lidar-01", "perception"), IntervalStart: start,
		IntervalDuration: time.Minute, Zones: []model.ZoneMeasurement{zone}}, &observed)
	sum = 0
	for _, oc := range observed.GetZone()[0].GetObservedClass() {
		sum += oc.GetCount()
	}
	if sum != 42 {
		t.Errorf("zone-occupancy-interval-report observed-class sums to %d, want the observed count 42", sum)
	}

	// A breakdown the device does not give is absent, not borrowed from the
	// other population.
	zone.ObservedClassCounts = nil
	encodeOK(t, model.ZoneOccupancyIntervalReport{Base: base("lidar-01", "perception"), IntervalStart: start,
		IntervalDuration: time.Minute, Zones: []model.ZoneMeasurement{zone}}, &observed)
	if got := observed.GetZone()[0].GetObservedClass(); len(got) != 0 {
		t.Errorf("observed-class = %v with no observed breakdown; want absent", got)
	}
}
