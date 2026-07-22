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
