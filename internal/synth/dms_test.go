package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func dmsSnap(at time.Time, st model.DMSStatus) *model.Snapshot {
	return &model.Snapshot{DeviceID: "dms-1", DeviceKind: "dms", SampledAt: at,
		Facets: []model.Facet{st}}
}

var dmsNormal = model.DMSStatus{
	ControlMode: model.ControlCentral, DisplayState: model.DisplayNormal,
	ActiveMemoryType: model.MemoryChangeable, ActiveSlot: 4,
	MessageStatus: model.StatusValid,
	MessageText:   "[jl3]ROAD WORK[nl]5 MILES AHEAD", MessageCRC: 0xBEEF,
	ActivationTrigger: model.TriggerCommand,
}

// Nothing has transitioned on the first observation; we have merely learned
// the current state.
func TestDMSFirstPollEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	if evs := e.Apply(dmsSnap(t0, dmsNormal)); len(evs) != 0 {
		t.Fatalf("first poll must emit nothing, got %v", kinds(evs))
	}
}

func TestDMSNoChangeEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	if evs := e.Apply(dmsSnap(t0.Add(time.Second), dmsNormal)); len(evs) != 0 {
		t.Fatalf("unchanged status must emit nothing, got %v", kinds(evs))
	}
}

// The two axes are independent: each must fire on its own without dragging
// the other along.
func TestDMSAxesChangeIndependently(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	// Control mode only.
	local := dmsNormal
	local.ControlMode = model.ControlLocal
	evs := e.Apply(dmsSnap(t0.Add(time.Second), local))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 control-mode-changed", kinds(evs))
	}
	cm, ok := evs[0].(model.DMSControlModeChanged)
	if !ok || cm.From != model.ControlCentral || cm.To != model.ControlLocal {
		t.Fatalf("bad control-mode event: %+v", evs[0])
	}

	// Display state only.
	blank := local
	blank.DisplayState = model.DisplayBlank
	evs = e.Apply(dmsSnap(t0.Add(2*time.Second), blank))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 display-state-changed", kinds(evs))
	}
	ds, ok := evs[0].(model.DMSDisplayStateChanged)
	if !ok || ds.From != model.DisplayNormal || ds.To != model.DisplayBlank {
		t.Fatalf("bad display-state event: %+v", evs[0])
	}
}

// The face message is its own axis: swapping messages under an unchanged
// display state fires message-changed alone, and an in-place rewrite of the
// same slot (CRC/text move, identity doesn't) still counts as a change.
func TestDMSMessageAxisFiresAlone(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	// A different slot takes the face; display state stays normal.
	swapped := dmsNormal
	swapped.ActiveSlot = 7
	swapped.MessageText = "[jl3]CRASH AHEAD[nl]USE CAUTION"
	swapped.MessageCRC = 0x1234
	swapped.ActivationTrigger = model.TriggerSchedule
	evs := e.Apply(dmsSnap(t0.Add(time.Second), swapped))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 message-changed", kinds(evs))
	}
	mc, ok := evs[0].(model.DMSMessageChanged)
	if !ok || mc.FromSlot != 4 || mc.ToSlot != 7 ||
		mc.FromText != dmsNormal.MessageText || mc.ToText != swapped.MessageText ||
		mc.FromCRC != 0xBEEF || mc.ToCRC != 0x1234 ||
		mc.Trigger != model.TriggerSchedule {
		t.Fatalf("bad message-changed event: %+v", evs[0])
	}

	// In-place rewrite: same (memory-type, slot), new content.
	rewritten := swapped
	rewritten.MessageText = "[jl3]CRASH CLEARED"
	rewritten.MessageCRC = 0x5678
	evs = e.Apply(dmsSnap(t0.Add(2*time.Second), rewritten))
	if len(evs) != 1 {
		t.Fatalf("in-place rewrite: events = %v, want 1 message-changed", kinds(evs))
	}
	if mc := evs[0].(model.DMSMessageChanged); mc.FromSlot != 7 || mc.ToSlot != 7 || mc.ToCRC != 0x5678 {
		t.Fatalf("bad in-place rewrite event: %+v", evs[0])
	}
}

// The trigger is context on the message axis, not an axis of its own: a
// changed why with an unchanged what is not a new message.
func TestDMSTriggerAloneIsNotAChange(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	retriggered := dmsNormal
	retriggered.ActivationTrigger = model.TriggerSchedule
	if evs := e.Apply(dmsSnap(t0.Add(time.Second), retriggered)); len(evs) != 0 {
		t.Fatalf("trigger-only change must emit nothing, got %v", kinds(evs))
	}
}

// Going blank moves two axes — display state and face message — and each
// reports on its own.
func TestDMSBlankFiresDisplayAndMessageAxes(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	blank := dmsNormal
	blank.DisplayState = model.DisplayBlank
	blank.ActiveMemoryType = model.MemoryBlank
	blank.ActiveSlot = 0
	blank.MessageText = ""
	blank.MessageCRC = 0
	evs := e.Apply(dmsSnap(t0.Add(time.Second), blank))
	if got := kinds(evs); len(got) != 2 || got[0] != "display-state-changed" || got[1] != "message-changed" {
		t.Fatalf("events = %v, want [display-state-changed message-changed] in that order", got)
	}
	if mc := evs[1].(model.DMSMessageChanged); mc.ToMemoryType != model.MemoryBlank || mc.ToText != "" {
		t.Fatalf("blank transition must report the blank bank with empty text, got %+v", mc)
	}
}

func TestDMSBothAxesChangeAtOnce(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	both := dmsNormal
	both.ControlMode = model.ControlLocal
	both.DisplayState = model.DisplayBlank
	evs := e.Apply(dmsSnap(t0.Add(time.Second), both))
	if got := kinds(evs); len(got) != 2 || got[0] != "control-mode-changed" || got[1] != "display-state-changed" {
		t.Fatalf("events = %v, want [control-mode-changed display-state-changed] in that order", got)
	}
}

// Entering the error state reports once. A sign sitting broken must not
// re-report every poll — that would be a fault storm, not information.
func TestDMSActivationFailureReportsOnceOnTransition(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	broken := dmsNormal
	broken.MessageStatus = model.StatusError
	broken.SyntaxError = model.SyntaxErrorFontNotFound
	broken.SyntaxErrorPos = 12

	evs := e.Apply(dmsSnap(t0.Add(time.Second), broken))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 message-activation-failed", kinds(evs))
	}
	f, ok := evs[0].(model.DMSMessageActivationFailed)
	if !ok || f.Error != model.SyntaxErrorFontNotFound || f.ErrorPosition != 12 ||
		f.MemoryType != model.MemoryChangeable || f.Slot != 4 {
		t.Fatalf("bad activation-failed event: %+v", evs[0])
	}

	// Still broken, still the same error: silence.
	if evs := e.Apply(dmsSnap(t0.Add(2*time.Second), broken)); len(evs) != 0 {
		t.Fatalf("sustained error must not re-report, got %v", kinds(evs))
	}

	// Recovers, then breaks again: reports again.
	e.Apply(dmsSnap(t0.Add(3*time.Second), dmsNormal))
	if evs := e.Apply(dmsSnap(t0.Add(4*time.Second), broken)); len(evs) != 1 {
		t.Fatalf("re-entering error must report again, got %v", kinds(evs))
	}
}

// The iron rule, for this facet.
func TestDMSFailedReadEmitsNothing(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))

	failed := &model.Snapshot{DeviceID: "dms-1", DeviceKind: "dms", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindDMSStatus, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}

	// Recovery against the surviving prev: unchanged state, so still silence.
	if evs := e.Apply(dmsSnap(t0.Add(2*time.Second), dmsNormal)); len(evs) != 0 {
		t.Fatalf("post-recovery unchanged state must emit nothing, got %v", kinds(evs))
	}
}

func TestDMSEventsCarryDeviceKind(t *testing.T) {
	e := NewEngine(NewDMSDiffer())
	e.Apply(dmsSnap(t0, dmsNormal))
	local := dmsNormal
	local.ControlMode = model.ControlLocal
	evs := e.Apply(dmsSnap(t0.Add(time.Second), local))
	if len(evs) != 1 || evs[0].EventDeviceKind() != "dms" {
		t.Fatalf("DMS events must carry DeviceKind=dms, got %+v", evs)
	}
}
