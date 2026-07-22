package synth

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// NewDMSDiffer diffs the dms-status facet into transition events.
//
// DMS emits on transitions only — the catalog has no periodic DMS state
// report, so there is no per-poll event to produce. A consequence worth
// knowing: after a collector restart a sign's current state is not
// re-announced until it next changes. That is a wire-mapping gap (5 of 8
// services lack a status-report ce-type), not something the differ should
// paper over by inventing a transition that did not happen.
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
	// sort: control mode, then display state, then activation failure.
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
