package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewGateDiffer diffs the gate-bank facet into per-gate transition events.
//
// Position and mode are independent axes. Alarm bits ride the shared
// fault-set facet (and FaultDiffer), not this one — so a failed-to-close
// raise does not also invent a position change.
//
// First poll emits nothing: we learned the state, nothing transitioned.
func NewGateDiffer() Differ { return gateDiffer{} }

type gateDiffer struct{}

func (gateDiffer) Kind() model.Kind { return model.KindGateBank }

func (gateDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.GateBank)
	if prev == nil {
		return nil
	}
	p := prev.(model.GateBank)

	prevByID := indexGates(p.Gates)
	currIDs := make([]string, 0, len(c.Gates))
	for _, g := range c.Gates {
		currIDs = append(currIDs, g.ID)
	}
	sort.Strings(currIDs)

	var events []model.Event
	for _, id := range currIDs {
		cg := gateByID(c.Gates, id)
		pg, had := prevByID[id]
		if !had {
			// Newly configured mid-run: treat like first observation for that
			// gate — no fabricated transition from Unknown.
			continue
		}
		if pg.Position != cg.Position {
			events = append(events, model.GatePositionChanged{
				Base: base, GateID: id, From: pg.Position, To: cg.Position,
			})
		}
		if pg.Mode != cg.Mode {
			events = append(events, model.GateModeChanged{
				Base: base, GateID: id, From: pg.Mode, To: cg.Mode,
			})
		}
	}
	return events
}

func indexGates(gates []model.GateReading) map[string]model.GateReading {
	out := make(map[string]model.GateReading, len(gates))
	for _, g := range gates {
		out[g.ID] = g
	}
	return out
}

func gateByID(gates []model.GateReading, id string) model.GateReading {
	for _, g := range gates {
		if g.ID == id {
			return g
		}
	}
	return model.GateReading{}
}
