// Package cloudevents builds the CE envelopes the collector publishes.
// CE type = catalog ce-type verbatim; source = the profile URN (ADR 0006);
// id = deterministic, so JetStream dedup survives restarts and redundant
// publishers agree without coordinating.
package cloudevents

import "time"

// Envelope is one CloudEvent in binary mode: attributes ride as ce-* transport
// headers and Data is the raw encoded body. There is no structured-JSON form —
// the profile specifies binary mode for every event, so a single publish path
// serves both the openits and health emitters.
type Envelope struct {
	SpecVersion     string
	ID              string
	Source          string
	Type            string
	Time            time.Time
	DataContentType string
	// DataSchema is empty for collector-owned schemas (ADR 0007), which omit
	// the attribute rather than point at a registry they are not in.
	DataSchema string
	Data       []byte
}

// Event is the input to New: everything needed to build an envelope.
type Event struct {
	CEType      string
	Source      string
	ContentType string
	DataSchema  string

	// OccurredAt is the event's own time — the device's clock, or the
	// observer's for events the collector infers. It becomes ce-time AND the
	// stable-time the id is derived from.
	OccurredAt time.Time

	// Data is what goes on the wire. Identity is what the id is derived from:
	// the same payload with producer-assigned leaves (sequence, observed-by)
	// cleared, so the id describes the occurrence rather than the observation.
	//
	// They differ only for emitters whose payloads carry such leaves. When
	// Identity is nil, Data is used — correct for the health schema, which has
	// no producer-assigned fields to clear.
	Data     []byte
	Identity []byte
}

// New builds an envelope with a deterministic id.
func New(ev Event) Envelope {
	identity := ev.Identity
	if identity == nil {
		identity = ev.Data
	}
	at := ev.OccurredAt.UTC()
	return Envelope{
		SpecVersion:     "1.0",
		ID:              EventID(ev.Source, ev.CEType, at, identity),
		Source:          ev.Source,
		Type:            ev.CEType,
		Time:            at,
		DataContentType: ev.ContentType,
		DataSchema:      ev.DataSchema,
		Data:            ev.Data,
	}
}
