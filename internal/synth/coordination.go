package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewCoordinationDiffer diffs coordination-status facets: one event per scalar
// axis that changed between polls.
func NewCoordinationDiffer() Differ { return coordinationDiffer{} }

type coordinationDiffer struct{}

func (coordinationDiffer) Kind() model.Kind { return model.KindCoordinationStatus }

func (coordinationDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	if prev == nil {
		return nil
	}
	p := prev.(model.CoordinationStatus)
	c := curr.(model.CoordinationStatus)

	type axis struct {
		kind model.CoordinationAxis
		prev int64
		curr int64
	}
	axes := []axis{
		{model.CoordAxisOperationalMode, p.OperationalMode, c.OperationalMode},
		{model.CoordAxisSelectedPattern, p.SelectedPattern, c.SelectedPattern},
		{model.CoordAxisActualOffset, p.ActualOffset, c.ActualOffset},
		{model.CoordAxisCycleLength, p.CycleLength, c.CycleLength},
		{model.CoordAxisCoordinationMode, p.CoordinationMode, c.CoordinationMode},
	}

	var events []model.Event
	for _, ax := range axes {
		if ax.prev == ax.curr {
			continue
		}
		events = append(events, model.CoordinationChanged{
			Base: base, Axis: ax.kind, PreviousValue: ax.prev, NewValue: ax.curr,
		})
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].(model.CoordinationChanged).Axis <
			events[j].(model.CoordinationChanged).Axis
	})
	return events
}
