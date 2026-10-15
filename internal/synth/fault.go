package synth

import (
	"sort"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewFaultDiffer diffs the fault-set facet: a raise for each fault that
// appeared, a clear for each that vanished.
func NewFaultDiffer() Differ { return &faultDiffer{} }

type faultDiffer struct {
	// firstSeen[deviceID][faultID] is the poll time the fault was first
	// observed in this process. A restart forgets it: the persisted facet
	// does not store a raise time, so a clear after Seed uses the first
	// poll that still saw the fault.
	firstSeen map[string]map[string]time.Time
}

func (*faultDiffer) Facet() model.Facet { return model.FaultSet{} }

func (d *faultDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	currFaults := index(curr.(model.FaultSet))
	var prevFaults map[string]model.Fault
	if prev != nil {
		prevFaults = index(prev.(model.FaultSet))
	}
	seen := d.seenFor(base.DeviceID)

	// Sorted iteration: gen-1 ranged over a map here, making event order
	// nondeterministic. Determinism is a construction-time requirement.
	var events []model.Event
	for _, id := range sortedIDs(currFaults) {
		if _, existed := prevFaults[id]; existed {
			if _, ok := seen[id]; !ok {
				seen[id] = base.OccurredAt
			}
			continue // still raised; an attribute change is not a raise
		}
		f := currFaults[id]
		seen[id] = base.OccurredAt
		events = append(events, model.FaultRaised{
			Base: base, FaultID: f.ID, Severity: f.Severity,
			Category: f.Category, Description: f.Description,
		})
	}
	for _, id := range sortedIDs(prevFaults) {
		if _, still := currFaults[id]; still {
			continue
		}
		first := seen[id]
		delete(seen, id)
		f := prevFaults[id]
		events = append(events, model.FaultCleared{
			Base: base, FaultID: id, Category: f.Category, FirstObserved: first,
		})
	}
	return events
}

func (d *faultDiffer) seenFor(deviceID string) map[string]time.Time {
	if d.firstSeen == nil {
		d.firstSeen = make(map[string]map[string]time.Time)
	}
	seen := d.firstSeen[deviceID]
	if seen == nil {
		seen = make(map[string]time.Time)
		d.firstSeen[deviceID] = seen
	}
	return seen
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
