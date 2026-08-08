// Package wire is the ONLY layer allowed to know wire schemas. Emitters
// turn domain events into (payload, ce-type). One subpackage per pinned
// openits-models release (Plan 2+); package health is the collector-owned
// schema (ADR 0007).
package wire

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// Encoded is one wire-ready payload.
type Encoded struct {
	CEType      string
	ContentType string
	Data        []byte

	// DataSchema is the ce-dataschema URL: the immutable schema-registry
	// snapshot this body validates against. Empty for emitters whose schema
	// is collector-owned (ADR 0007 health), which deliberately omit the
	// attribute rather than point at a registry they are not in.
	DataSchema string

	// Identity is Data with producer-assigned leaves cleared — the bytes the
	// deterministic ce-id is derived from. Data is what ships; Identity is
	// what the event IS.
	//
	// They diverge because payloads carry bookkeeping that describes the
	// observation rather than the occurrence: a per-producer sequence counter
	// that resets on restart, and the id of whichever collector observed it.
	// Hashing those would give two collectors watching one device two ids for
	// one event, and would change an event's id across a restart.
	//
	// Only the emitter can compute this, because only internal/wire may know
	// the payload's shape (ADR 0002). Leave nil when there is nothing to
	// clear; callers then derive the id from Data.
	Identity []byte
}

// Emitter maps domain events to wire payloads. ok=false (with nil error)
// means "not mine": callers try the next emitter in their chain, and an
// event no emitter claims is dropped LOUDLY (metric + log), never silently.
type Emitter interface {
	Encode(ev model.Event) (enc *Encoded, ok bool, err error)

	// CETypes returns every ce-type this emitter can produce, sorted.
	// Boot validation renders each through the subject template, so an
	// emitter that under-reports here defeats that check.
	CETypes() []string
}
