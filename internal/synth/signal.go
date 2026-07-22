package synth

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// NewSignalDiffer diffs the signal-status facet: a status report every
// poll, plus transition events when a previous value exists.
func NewSignalDiffer() Differ { return signalDiffer{} }

type signalDiffer struct{}

func (signalDiffer) Kind() model.Kind { return model.KindSignalStatus }

func (signalDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.SignalStatus)
	events := []model.Event{model.OperationalStatusReport{
		Base: base, Mode: c.Mode, InConflictFlash: c.InConflictFlash, ActivePlanID: c.ActivePlanID,
	}}
	if prev == nil {
		return events
	}
	p := prev.(model.SignalStatus)
	if p.Mode != c.Mode {
		events = append(events, model.ModeChanged{Base: base, From: p.Mode, To: c.Mode})
	}
	if p.ActivePlanID != c.ActivePlanID {
		events = append(events, model.PlanChanged{Base: base, FromPlanID: p.ActivePlanID, ToPlanID: c.ActivePlanID})
	}
	if !p.PreemptionActive && c.PreemptionActive {
		events = append(events, model.PreemptionActivated{Base: base, Source: c.PreemptionSource})
	}
	if p.PreemptionActive && !c.PreemptionActive {
		events = append(events, model.PreemptionCleared{Base: base})
	}
	return events
}
