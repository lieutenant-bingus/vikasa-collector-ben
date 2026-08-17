// Package model is the collector-owned domain model: the only currency
// adapters produce and the core consumes. It never imports wire schemas
// (openits-models); mapping to wire types is internal/wire's job (ADR 0002).
package model

import "time"

// Kind identifies a facet family. Facets are per-device-KIND, never
// per-vendor — a governance rail from the 2026-07-12 greenfield collector
// architecture spec, §4. That spec is scheduled for deletion once §4 is
// harvested into the explanation tier, so it is named in full rather than
// cited as a bare "spec §4"; see docs/README.md's known-gaps list.
type Kind string

// Facet is one typed slice of device state within a Snapshot.
type Facet interface{ FacetKind() Kind }

// FacetError records a facet the adapter tried and failed to read this
// poll. Synth suspends diffing for failed facets: absence of evidence is
// never a state change.
type FacetError struct {
	Kind Kind
	Err  string
}

// Snapshot is the state of one device at a single poll.
type Snapshot struct {
	DeviceID string
	// DeviceKind is stamped by the runner from the adapter's Descriptor after
	// Read returns; adapters do not set it. Synth copies it onto every event.
	DeviceKind string
	SampledAt  time.Time
	Facets     []Facet
	Errors     []FacetError
}

// Facet returns the facet of kind k, if present.
func (s *Snapshot) Facet(k Kind) (Facet, bool) {
	for _, f := range s.Facets {
		if f.FacetKind() == k {
			return f, true
		}
	}
	return nil, false
}

// FacetFailed reports whether the adapter recorded a read failure for k.
func (s *Snapshot) FacetFailed(k Kind) bool {
	for _, e := range s.Errors {
		if e.Kind == k {
			return true
		}
	}
	return false
}
