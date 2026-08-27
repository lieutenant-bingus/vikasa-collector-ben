package openits

import (
	"fmt"
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
	enc, ok, err := New(collectorID).Encode(ev)
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
	em := New("cabinet-poller-1")

	enc, ok, err := em.Encode(model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFlash,
		To:   model.ModeNormal,
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
	// ModeNormal maps to mode-FREE, not mode-normal: upstream collapsed
	// "normal" into "free" because NTCIP and signal technicians treat
	// uncoordinated-actuated operation as one mode. A `mode-normal` identity
	// does exist, but it belongs to openits-dms-types and means something else.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeFlash,
		To:   model.ModeNormal,
	}, "cabinet-poller-1")

	if want := "openits-signal-control-types:mode-flash"; got.GetPrior() != want {
		t.Errorf("prior = %q, want %q", got.GetPrior(), want)
	}
	if want := "openits-signal-control-types:mode-free"; got.GetCurrent() != want {
		t.Errorf("current = %q, want %q", got.GetCurrent(), want)
	}
}

func TestEncode_ModeChangedToStandby_IsNotClaimed(t *testing.T) {
	// ModeStandby has no controller-mode identity upstream (the set is
	// coordinated/free/flash/preempt/priority/manual/off). Encoding it would
	// mean inventing a near-neighbour, so the event is not claimed and the
	// caller's loud-drop path fires. A visible drop beats a wrong mode.
	enc, ok, err := New("cabinet-poller-1").Encode(model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeNormal,
		To:   model.ModeStandby,
	})

	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if ok {
		t.Errorf("emitter claimed a mode with no upstream identity; payload=%v", enc)
	}
}

func TestEncode_ModeChangedFromUnmappable_StillClaimedWithPriorAbsent(t *testing.T) {
	// The reverse: `current` is mandatory but `prior` is optional ("absent when
	// the device just started up"). An unmappable From must not suppress an
	// otherwise-encodable transition.
	got := decodeModeChanged(t, model.ModeChanged{
		Base: base("asc-main-and-5th", "asc"),
		From: model.ModeStandby,
		To:   model.ModeFlash,
	}, "cabinet-poller-1")

	if got.GetPrior() != "" {
		t.Errorf("prior = %q, want empty for an unmappable From", got.GetPrior())
	}
	if want := "openits-signal-control-types:mode-flash"; got.GetCurrent() != want {
		t.Errorf("current = %q, want %q", got.GetCurrent(), want)
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
		From: model.ModeFlash,
		To:   model.ModeNormal,
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
	em := New("cabinet-poller-1")
	seq := func(deviceID string) uint64 {
		t.Helper()
		enc, ok, err := em.Encode(model.ModeChanged{
			Base: base(deviceID, "asc"), From: model.ModeFlash, To: model.ModeNormal,
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
	em := New("cabinet-poller-1")
	const devices, perDevice = 8, 50

	var wg sync.WaitGroup
	seen := make([][]uint64, devices)
	for d := 0; d < devices; d++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			id := fmt.Sprintf("asc-%d", d)
			for i := 0; i < perDevice; i++ {
				enc, ok, err := em.Encode(model.ModeChanged{
					Base: base(id, "asc"), From: model.ModeFlash, To: model.ModeNormal,
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
	for d := 0; d < devices; d++ {
		got := append([]uint64(nil), seen[d]...)
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
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
	enc, ok, err := New("cabinet-poller-1").Encode(ev)
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
	enc, ok, err := New("cabinet-poller-1").Encode(ev)
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

func TestEncode_BothDMSModeAxes_ShareOneCEType(t *testing.T) {
	// dms-mode-event-kind is documented as spanning BOTH dms-control-mode
	// (who is driving) and the sign-mode display state (off/blank/test/normal).
	// So control-mode and display-state changes are two axes of one ce-type,
	// discriminated by which identity set prior/current are drawn from — not
	// two ce-types, and not one of them dropped.
	ctrl, ctrlType := decodeDMSMode(t, model.DMSControlModeChanged{
		Base: base("dms-i35-mm214", "dms"),
		From: model.ControlCentral,
		To:   model.ControlLocal,
	})
	disp, dispType := decodeDMSMode(t, model.DMSDisplayStateChanged{
		Base: base("dms-i35-mm214", "dms"),
		From: model.DisplayNormal,
		To:   model.DisplayBlank,
	})

	const want = "openits.dms.mode-changed.v1"
	if ctrlType != want || dispType != want {
		t.Errorf("ce-types = %q / %q, want both %q", ctrlType, dispType, want)
	}
	if w := "openits-dms-types:dms-mode-event-kind"; ctrl.GetKind() != w || disp.GetKind() != w {
		t.Errorf("kind = %q / %q, want both %q", ctrl.GetKind(), disp.GetKind(), w)
	}
	if w := "openits-dms-types:dms-control-local"; ctrl.GetCurrent() != w {
		t.Errorf("control current = %q, want %q", ctrl.GetCurrent(), w)
	}
	if w := "openits-dms-types:dms-control-central"; ctrl.GetPrior() != w {
		t.Errorf("control prior = %q, want %q", ctrl.GetPrior(), w)
	}
	if w := "openits-dms-types:mode-blank"; disp.GetCurrent() != w {
		t.Errorf("display current = %q, want %q", disp.GetCurrent(), w)
	}
	if w := "openits-dms-types:mode-normal"; disp.GetPrior() != w {
		t.Errorf("display prior = %q, want %q", disp.GetPrior(), w)
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
	if w := "openits-dms-types:mode-unknown"; got.GetCurrent() != w {
		t.Errorf("current = %q, want %q", got.GetCurrent(), w)
	}
}

func TestEncode_DMSControlUnknown_IsNotClaimed(t *testing.T) {
	// dms-control-mode has no "unknown" member: local/external/central/
	// central-override/simulation/other. dms-control-other means "a
	// vendor-specific mode not covered above", which is NOT the same claim as
	// "we don't know", so an unknown control mode is declined.
	_, ok, err := New("cabinet-poller-1").Encode(model.DMSControlModeChanged{
		Base: base("dms-i35-mm214", "dms"),
		From: model.ControlCentral,
		To:   model.ControlUnknown,
	})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if ok {
		t.Error("emitter claimed a control mode with no upstream identity")
	}
}

// encodeOK is the generic "claim it and hand me the bytes" helper.
func encodeOK(t *testing.T, ev model.Event, into proto.Message) string {
	t.Helper()
	enc, ok, err := New("cabinet-poller-1").Encode(ev)
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

func TestEncode_OperationalStatusReport_MapsModeAndFlash(t *testing.T) {
	var got scv1.OperationalStatusReport
	ceType := encodeOK(t, model.OperationalStatusReport{
		Base: base("asc-1", "asc"), Mode: model.ModeFlash,
		InConflictFlash: true, ActivePlanID: 3,
	}, &got)

	if want := "openits.signal-control.operational-status-report.v1"; ceType != want {
		t.Errorf("ce-type = %q, want %q", ceType, want)
	}
	if want := "openits-signal-control-types:mode-flash"; got.GetMode() != want {
		t.Errorf("mode = %q, want %q", got.GetMode(), want)
	}
	if !got.GetFlashActive() {
		t.Error("flash_active = false, want true")
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
	if got.GetErrorType() != dmsv1.ErrorType_ERROR_TYPE_FONT_NOT_FOUND {
		t.Errorf("error_type = %v", got.GetErrorType())
	}
	if got.GetErrorPosition() != 12 {
		t.Errorf("error_position = %d, want 12", got.GetErrorPosition())
	}
}

func TestCETypes_IsCompleteSortedAndDeduped(t *testing.T) {
	got := New("cabinet-poller-1").CETypes()

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
	for _, ct := range New("cabinet-poller-1").CETypes() {
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
		model.ModeChanged{Base: base("asc-1", "asc"), From: model.ModeFlash, To: model.ModeNormal},
		"https://schemas.open-its.org/openits-common-mode-events/2026-07-21/",
	}, {
		"dms mode-changed shares that same defining module",
		model.DMSDisplayStateChanged{Base: base("dms-1", "dms"), From: model.DisplayNormal, To: model.DisplayBlank},
		"https://schemas.open-its.org/openits-common-mode-events/2026-07-21/",
	}, {
		"fault-raised is defined by common-fault-events",
		model.FaultRaised{Base: base("asc-1", "asc"), FaultID: "f1"},
		"https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
	}, {
		"plan-applied is defined by signal-control-events",
		model.PlanChanged{Base: base("asc-1", "asc"), ToPlanID: 1},
		"https://schemas.open-its.org/openits-signal-control-events/2026-07-21/",
	}, {
		"message-activation-failed is defined by dms-events, at its own revision",
		model.DMSMessageActivationFailed{Base: base("dms-1", "dms")},
		"https://schemas.open-its.org/openits-dms-events/2026-08-27/",
	}, {
		"message-changed is defined by dms-events, at the v0.4.0 revision",
		model.DMSMessageChanged{Base: base("dms-1", "dms"), ToMemoryType: model.MemoryChangeable, ToSlot: 1},
		"https://schemas.open-its.org/openits-dms-events/2026-08-27/",
	}, {
		"sign-status-report is defined by dms-events, at the v0.4.0 revision",
		model.DMSSignStatusReport{Base: base("dms-1", "dms")},
		"https://schemas.open-its.org/openits-dms-events/2026-08-27/",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			enc, ok, err := New("cabinet-poller-1").Encode(tc.ev)
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
	for _, ceType := range New("x").CETypes() {
		if dataSchemaFor[ceType] == "" {
			t.Errorf("ce-type %q has no ce-dataschema constant", ceType)
		}
	}
}

func TestEncode_IdentityExcludesProducerAssignedLeaves(t *testing.T) {
	// ce-id is derived from Identity, not Data. Two encodings of the SAME
	// occurrence differ in sequence (the counter advances) and would otherwise
	// produce different ids, destroying restart-invariance and replay dedup.
	em := New("cabinet-poller-1")
	ev := model.ModeChanged{
		Base: base("asc-1", "asc"), From: model.ModeFlash, To: model.ModeNormal,
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
	if id.GetCurrent() != "openits-signal-control-types:mode-free" {
		t.Errorf("identity lost the event's substance: current = %q", id.GetCurrent())
	}
	if id.GetSequence() != 0 || id.GetObservedBy() != "" {
		t.Errorf("identity retained producer-assigned leaves: sequence=%d observed_by=%q",
			id.GetSequence(), id.GetObservedBy())
	}
}

func TestEncode_IdentityIsSensitiveToRealChanges(t *testing.T) {
	// The flip side: clearing bookkeeping must not blur genuinely different
	// events into one id.
	em := New("cabinet-poller-1")
	a, _, _ := em.Encode(model.ModeChanged{
		Base: base("asc-1", "asc"), From: model.ModeFlash, To: model.ModeNormal,
	})
	b, _, _ := em.Encode(model.ModeChanged{
		Base: base("asc-1", "asc"), From: model.ModeFlash, To: model.ModeOff,
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
	for _, tc := range []struct{ deviceKind, raised, cleared string }{
		{"asc", "openits.signal-control.fault-raised.v1", "openits.signal-control.fault-cleared.v1"},
		{"dms", "openits.dms.fault-raised.v1", "openits.dms.fault-cleared.v1"},
		{"cctv", "openits.cctv.fault-raised.v1", "openits.cctv.fault-cleared.v1"},
		{"traffic-sensor", "openits.traffic-sensor.fault-raised.v1", "openits.traffic-sensor.fault-cleared.v1"},
		{"perception", "openits.perception.fault-raised.v1", "openits.perception.fault-cleared.v1"},
	} {
		t.Run(tc.deviceKind, func(t *testing.T) {
			var raised commonv1.FaultRaised
			if ct := encodeOK(t, model.FaultRaised{
				Base: base("dev-1", tc.deviceKind), FaultID: "f1",
				Severity: model.SeverityMajor, Category: model.CategoryCommunication,
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
		got, ok := faultKindIdentity(model.CategoryConflict, deviceKind)
		if !ok {
			t.Errorf("%s: no fault identity at all", deviceKind)
			continue
		}
		if got != want {
			t.Errorf("%s: kind = %q, want %q", deviceKind, got, want)
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
			ClassCounts: []model.ZoneClassCount{{Class: model.ObjectTruck, Count: 4}},
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
	if len(cc) != 1 || cc[0].GetClass() != "openits-types:object-truck" || cc[0].GetCount() != 4 {
		t.Errorf("class counts = %+v", cc)
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
			ClassCounts: []model.ZoneClassCount{{Class: model.ObjectTruck, Count: 4}},
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
	if len(oc) != 1 || oc[0].GetClass() != "openits-types:object-truck" || oc[0].GetCount() != 4 {
		t.Errorf("observed classes = %+v", oc)
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
	if want := "openits-cctv-types:cctv-control-central"; got.GetPrior() != want {
		t.Errorf("prior = %q, want %q", got.GetPrior(), want)
	}
	if want := "openits-cctv-types:cctv-control-local"; got.GetCurrent() != want {
		t.Errorf("current = %q, want %q", got.GetCurrent(), want)
	}
}

func TestEncode_CCTVControlUnknownIsNotClaimed(t *testing.T) {
	// cctv-control-mode has no "unknown" member, exactly like dms-control-mode.
	// cctv-control-other means "a vendor mode not covered above" — a positive
	// claim about the camera, not an admission of ignorance.
	_, ok, err := New("cabinet-poller-1").Encode(model.CCTVControlModeChanged{
		Base: base("cam-03", "cctv"),
		From: model.CCTVControlCentral, To: model.CCTVControlUnknown,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if ok {
		t.Error("emitter claimed a control mode with no upstream identity")
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

func TestEncode_CCTVTourUnknownStateIsNotClaimed(t *testing.T) {
	// TourRunState's wire zero is STOPPED with no unspecified, so an unknown
	// state would encode as a positive claim that the tour is stopped.
	_, ok, err := New("cabinet-poller-1").Encode(model.CCTVTourStateChanged{
		Base: base("cam-03", "cctv"), TourID: 4,
		From: model.TourRunning, To: model.TourUnknown,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if ok {
		t.Error("emitter claimed an unknown tour state; the wire would render it as stopped")
	}
}
