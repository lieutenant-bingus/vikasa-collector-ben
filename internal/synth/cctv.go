package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewCCTVDiffer turns consecutive camera-management snapshots into control-mode
// and tour-state transitions.
//
// Two independent axes, diffed independently: an operator taking local control
// and a tour pausing are different occurrences, and a poll where both move
// produces both events rather than one summary.
func NewCCTVDiffer() Differ { return cctvDiffer{} }

type cctvDiffer struct{}

func (cctvDiffer) Kind() model.Kind { return model.KindCCTVStatus }

func (cctvDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	if prev == nil {
		// Transition events need a known prior state and there is none.
		return nil
	}
	p := prev.(model.CCTVStatus)
	c := curr.(model.CCTVStatus)

	var events []model.Event
	if p.ControlMode != c.ControlMode {
		events = append(events, model.CCTVControlModeChanged{
			Base: base, From: p.ControlMode, To: c.ControlMode,
		})
	}

	prevTours := make(map[uint32]model.TourRunState, len(p.Tours))
	for _, t := range p.Tours {
		prevTours[t.TourID] = t.State
	}

	tours := append([]model.CCTVTour(nil), c.Tours...)
	sort.Slice(tours, func(i, j int) bool { return tours[i].TourID < tours[j].TourID })
	for _, t := range tours {
		was, known := prevTours[t.TourID]
		if !known {
			// A tour appearing in the table has no prior state to transition
			// from — the same reason the first poll is silent. Reporting
			// "stopped -> running" here would invent the left-hand side.
			continue
		}
		if was != t.State {
			events = append(events, model.CCTVTourStateChanged{
				Base: base, TourID: t.TourID, From: was, To: t.State,
			})
		}
	}
	// A tour DISAPPEARING is deliberately silent: the tour was deleted from
	// the camera's configuration, which is not the same occurrence as it
	// stopping, and there is no catalog event that says so.
	return events
}
