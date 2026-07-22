package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewFaultDiffer diffs the fault-set facet: a raise for each fault that
// appeared, a clear for each that vanished.
func NewFaultDiffer() Differ { return faultDiffer{} }

type faultDiffer struct{}

func (faultDiffer) Kind() model.Kind { return model.KindFaultSet }

func (faultDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	currFaults := index(curr.(model.FaultSet))
	var prevFaults map[string]model.Fault
	if prev != nil {
		prevFaults = index(prev.(model.FaultSet))
	}

	// Sorted iteration: gen-1 ranged over a map here, making event order
	// nondeterministic. Determinism is a construction-time requirement.
	var events []model.Event
	for _, id := range sortedIDs(currFaults) {
		if _, existed := prevFaults[id]; existed {
			continue // still raised; an attribute change is not a raise
		}
		f := currFaults[id]
		events = append(events, model.FaultRaised{
			Base: base, FaultID: f.ID, Severity: f.Severity,
			Category: f.Category, Description: f.Description,
		})
	}
	for _, id := range sortedIDs(prevFaults) {
		if _, still := currFaults[id]; still {
			continue
		}
		events = append(events, model.FaultCleared{Base: base, FaultID: id})
	}
	return events
}

func index(fs model.FaultSet) map[string]model.Fault {
	out := make(map[string]model.Fault, len(fs.Faults))
	for _, f := range fs.Faults {
		out[f.ID] = f
	}
	return out
}

func sortedIDs(m map[string]model.Fault) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
