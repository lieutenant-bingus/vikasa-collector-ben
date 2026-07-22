// Package synth turns consecutive state Snapshots into domain events.
// One registered Differ per facet kind; the engine never grows vendor or
// wire knowledge.
package synth

import (
	"sync"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Differ diffs one facet kind. prev is nil on first observation.
type Differ interface {
	Kind() model.Kind
	Diff(prev, curr model.Facet, base model.Base) []model.Event
}

// Engine applies snapshots and remembers last-known facets per device.
type Engine struct {
	mu      sync.Mutex
	differs map[model.Kind]Differ
	// prev[deviceID][kind] = last successfully-read facet
	prev map[string]map[model.Kind]model.Facet
}

func NewEngine(differs ...Differ) *Engine {
	e := &Engine{
		differs: make(map[model.Kind]Differ),
		prev:    make(map[string]map[model.Kind]model.Facet),
	}
	for _, d := range differs {
		e.differs[d.Kind()] = d
	}
	return e
}

// Apply diffs snap against last-known state and returns domain events.
// Iron rule: a facet that failed (snap.Errors) or is simply absent emits
// nothing and keeps its previous value — absence of evidence is never a
// state change.
func (e *Engine) Apply(snap *model.Snapshot) []model.Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	dev := e.prev[snap.DeviceID]
	if dev == nil {
		dev = make(map[model.Kind]model.Facet)
		e.prev[snap.DeviceID] = dev
	}

	base := model.Base{DeviceID: snap.DeviceID, DeviceKind: snap.DeviceKind, OccurredAt: snap.SampledAt}
	var events []model.Event
	for _, f := range snap.Facets {
		d, ok := e.differs[f.FacetKind()]
		if !ok {
			continue // facet kind with no differ registered: carried, not diffed
		}
		events = append(events, d.Diff(dev[f.FacetKind()], f, base)...)
		dev[f.FacetKind()] = f
	}
	return events
}
