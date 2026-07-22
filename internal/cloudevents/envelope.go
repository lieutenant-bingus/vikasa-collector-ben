// Package cloudevents builds the CE envelopes the collector publishes.
// CE type = catalog ce-type verbatim; subject = tenant-spliced (ADR 0006);
// id = content-addressed so JetStream dedup survives restarts.
package cloudevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Envelope is a structured-mode CloudEvent (JSON).
type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

// New builds an envelope with a deterministic content-addressed ID.
func New(ceType, source string, occurredAt time.Time, contentType string, data []byte) Envelope {
	at := occurredAt.UTC()
	h := sha256.New()
	for _, part := range [][]byte{
		[]byte(ceType), []byte(source), []byte(at.Format(time.RFC3339Nano)), data,
	} {
		h.Write(part)
		h.Write([]byte{0})
	}
	return Envelope{
		SpecVersion:     "1.0",
		ID:              hex.EncodeToString(h.Sum(nil)),
		Source:          source,
		Type:            ceType,
		Time:            at,
		DataContentType: contentType,
		Data:            data,
	}
}
