package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewPhaseDiffer diffs phase-status facets: one event per phase whose movement
// state changed between polls.
func NewPhaseDiffer() Differ { return phaseDiffer{} }

type phaseDiffer struct{}

func (phaseDiffer) Kind() model.Kind { return model.KindPhaseStatuses }

func (phaseDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	if prev == nil {
		return nil
	}
	prevByPhase := indexPhases(prev.(model.PhaseStatuses))
	currPhases := curr.(model.PhaseStatuses)

	var events []model.Event
	for _, p := range currPhases.Phases {
		old, ok := prevByPhase[p.PhaseNumber]
		if !ok || old.State == p.State {
			continue
		}
		events = append(events, model.PhaseStateChanged{
			Base: base, PhaseNumber: p.PhaseNumber, From: old.State, To: p.State,
		})
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i].(model.PhaseStateChanged), events[j].(model.PhaseStateChanged)
		return a.PhaseNumber < b.PhaseNumber
	})
	return events
}

func indexPhases(ps model.PhaseStatuses) map[uint32]model.PhaseStatus {
	out := make(map[uint32]model.PhaseStatus, len(ps.Phases))
	for _, p := range ps.Phases {
		out[p.PhaseNumber] = p
	}
	return out
}
