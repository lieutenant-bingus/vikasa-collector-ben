package synth

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// NewDMSDiffer diffs the dms-status facet into transition events.
//
// This facet emits on transitions only. The catalog's periodic DMS report
// (openits.dms.sign-status-report.v1, added upstream in openits-models
// v0.4.0 on its own merits, per ADR 0016's route) covers the environment
// sensor cluster, which is the separate dms-environment facet with its own
// differ — not a per-poll re-report of this facet's state.
//
// The consequence worth knowing: after a collector restart a sign's
// control mode, display state, and face message are not re-announced until
// they next change. The fix for that is remembering across restarts
// (ADR 0017), not inventing a transition that did not happen.
func NewDMSDiffer() Differ { return dmsDiffer{} }

type dmsDiffer struct{}

func (dmsDiffer) Kind() model.Kind { return model.KindDMSStatus }

func (dmsDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.DMSStatus)
	if prev == nil {
		return nil // nothing transitioned; we just learned the state
	}
	p := prev.(model.DMSStatus)

	// Order is fixed by construction, so events are deterministic without a
	// sort: control mode, then display state, then face message, then
	// activation failure.
	var events []model.Event
	if p.ControlMode != c.ControlMode {
		events = append(events, model.DMSControlModeChanged{
			Base: base, From: p.ControlMode, To: c.ControlMode,
		})
	}
	if p.DisplayState != c.DisplayState {
		events = append(events, model.DMSDisplayStateChanged{
			Base: base, From: p.DisplayState, To: c.DisplayState,
		})
	}
	// The face message is its own axis: a sign can swap messages without the
	// display state moving (normal -> normal), and going blank moves both
	// axes — two events, one per axis. CRC and text participate so an
	// in-place rewrite of the same slot still reports; the trigger does NOT —
	// it says why the current message arrived, and a changed why with an
	// unchanged what is not a new message.
	if p.ActiveMemoryType != c.ActiveMemoryType || p.ActiveSlot != c.ActiveSlot ||
		p.MessageCRC != c.MessageCRC || p.MessageText != c.MessageText {
		events = append(events, model.DMSMessageChanged{
			Base:           base,
			FromMemoryType: p.ActiveMemoryType, FromSlot: p.ActiveSlot,
			FromText: p.MessageText, FromCRC: p.MessageCRC,
			ToMemoryType: c.ActiveMemoryType, ToSlot: c.ActiveSlot,
			ToText: c.MessageText, ToCRC: c.MessageCRC,
			Trigger: c.ActivationTrigger,
		})
	}
	// Only the TRANSITION into error reports. A sign sitting broken would
	// otherwise re-report every poll — a storm, not information.
	if p.MessageStatus != model.StatusError && c.MessageStatus == model.StatusError {
		events = append(events, model.DMSMessageActivationFailed{
			Base: base, MemoryType: c.ActiveMemoryType, Slot: c.ActiveSlot,
			Error: c.SyntaxError, ErrorPosition: c.SyntaxErrorPos,
		})
	}
	return events
}
