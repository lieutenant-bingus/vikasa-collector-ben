package openits

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

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
