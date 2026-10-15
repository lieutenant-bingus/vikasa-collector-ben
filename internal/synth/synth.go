// Package synth turns consecutive state Snapshots into domain events.
// One registered Differ per facet kind; the engine never grows vendor or
// wire knowledge.
package synth

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Differ diffs one facet kind. prev is nil on first observation.
type Differ interface {
	// Facet returns the zero value of the one concrete facet type this
	// differ diffs. Its FacetKind is the kind the differ is registered
	// under, and its dynamic type is the only type Apply will hand to Diff,
	// so Diff may assert it unchecked.
	Facet() model.Facet
	Diff(prev, curr model.Facet, base model.Base) []model.Event
}

// volatile is implemented by a differ whose facet changes on every poll
// (cumulative counters). Its state is never reported to a ChangeHook:
// persisting it would mean a write per poll, where ADR 0017 writes only on
// transitions. After a restart such a differ simply starts from a first
// observation.
type volatile interface{ Volatile() bool }

// ChangeHook observes every facet the engine now remembers for a device
// (non-volatile kinds only). It runs under the engine's lock, so it must
// not block: the durable layer only records the latest value and returns.
type ChangeHook func(deviceID string, kind model.Kind, f model.Facet)

// RejectHook observes a facet Apply refused because its dynamic type is not
// the one its differ diffs. got is the facet's Go type, e.g.
// "*model.SignalStatus".
type RejectHook func(deviceID string, kind model.Kind, got string)

// Engine applies snapshots and remembers last-known facets per device.
type Engine struct {
	mu      sync.Mutex
	differs map[model.Kind]Differ
	types   map[model.Kind]reflect.Type
	reject  RejectHook

	volatile map[model.Kind]bool
	onChange ChangeHook
	// hold, once set, makes Apply diff nothing for a device until Seed has
	// run for it (ADR 0020 clause 2).
	hold   bool
	seeded map[string]bool
	// prev[deviceID][kind] = last successfully-read facet
	prev map[string]map[model.Kind]model.Facet
}

// NewEngine registers one differ per facet kind. Two differs for the same
// kind is a wiring bug and panics, as adapter.Registry does for a duplicate
// adapter.
func NewEngine(differs ...Differ) *Engine {
	e := &Engine{
		differs: make(map[model.Kind]Differ),
		types:   make(map[model.Kind]reflect.Type),
		prev:    make(map[string]map[model.Kind]model.Facet),

		volatile: make(map[model.Kind]bool),
		seeded:   make(map[string]bool),
	}
	for _, d := range differs {
		zero := d.Facet()
		k := zero.FacetKind()
		if _, dup := e.differs[k]; dup {
			panic(fmt.Sprintf("synth.NewEngine: two differs registered for facet kind %q", k))
		}
		e.differs[k] = d
		e.types[k] = reflect.TypeOf(zero)
		if v, ok := d.(volatile); ok && v.Volatile() {
			e.volatile[k] = true
		}
	}
	return e
}

// HoldUntilSeeded makes Apply diff nothing for a device until Seed has run
// for it, and returns e. Call it before the first Apply. Without it the
// engine starts every device from a first observation, as it always has.
func (e *Engine) HoldUntilSeeded() *Engine {
	e.hold = true
	return e
}

// Seed installs a device's previous state — what the engine knew before a
// restart — and releases its hold. Facets of a kind with no differ, or of
// the wrong type, are ignored, as Apply would ignore them. Seeding with no
// facets is a first boot: diffing then follows first-observation rules.
func (e *Engine) Seed(deviceID string, facets []model.Facet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	dev := make(map[model.Kind]model.Facet, len(facets))
	for _, f := range facets {
		k := f.FacetKind()
		if t, ok := e.types[k]; ok && reflect.TypeOf(f) == t {
			dev[k] = f
		}
	}
	e.prev[deviceID] = dev
	e.seeded[deviceID] = true
}

// WithChangeHook sets the observer for remembered facets and returns e.
// Call it before the first Apply.
func (e *Engine) WithChangeHook(h ChangeHook) *Engine {
	e.onChange = h
	return e
}

// DecodeFacet rebuilds a facet of kind k from its JSON encoding, as the
// concrete type k's differ diffs. It is how stored state comes back.
func (e *Engine) DecodeFacet(k model.Kind, raw []byte) (model.Facet, error) {
	t, ok := e.types[k]
	if !ok {
		return nil, fmt.Errorf("synth: no differ for facet kind %q", k)
	}
	v := reflect.New(t)
	if err := json.Unmarshal(raw, v.Interface()); err != nil {
		return nil, fmt.Errorf("synth: decode %s: %w", k, err)
	}
	return v.Elem().Interface().(model.Facet), nil
}

// WithRejectHook sets the observer for wrong-typed facets and returns e.
// Call it before the first Apply.
func (e *Engine) WithRejectHook(h RejectHook) *Engine {
	e.reject = h
	return e
}

// Apply diffs snap against last-known state and returns domain events.
// Iron rule: a facet that failed (snap.Errors) or is simply absent emits
// nothing and keeps its previous value — absence of evidence is never a
// state change. A facet the snapshot ALSO reports as failed counts as
// failed, and a facet whose dynamic type is not its differ's is rejected
// (reported to the RejectHook) rather than diffed: neither is evidence.
func (e *Engine) Apply(snap *model.Snapshot) []model.Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.hold && !e.seeded[snap.DeviceID] {
		return nil // held: no previous state yet, so nothing is evidence
	}

	dev := e.prev[snap.DeviceID]
	if dev == nil {
		dev = make(map[model.Kind]model.Facet)
		e.prev[snap.DeviceID] = dev
	}

	base := model.Base{DeviceID: snap.DeviceID, DeviceKind: snap.DeviceKind, OccurredAt: snap.SampledAt}
	var events []model.Event
	for _, f := range snap.Facets {
		k := f.FacetKind()
		d, ok := e.differs[k]
		if !ok {
			continue // facet kind with no differ registered: carried, not diffed
		}
		if snap.FacetFailed(k) {
			continue
		}
		if reflect.TypeOf(f) != e.types[k] {
			if e.reject != nil {
				e.reject(snap.DeviceID, k, fmt.Sprintf("%T", f))
			}
			continue
		}
		events = append(events, d.Diff(dev[k], f, base)...)
		dev[k] = f
		if e.onChange != nil && !e.volatile[k] {
			e.onChange(snap.DeviceID, k, f)
		}
	}
	return events
}
