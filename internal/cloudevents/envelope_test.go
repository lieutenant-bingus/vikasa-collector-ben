package cloudevents

import (
	"testing"
	"time"
)

var tenant = Tenant{Region: "us-ga", Agency: "metro-atlanta", AgencyUnit: "d01", Site: "cabinet-042"}

func healthEvent(payload string) Event {
	return Event{
		CEType:      "openits-collector.health.collector-started.v1",
		Source:      "urn:openits:collector:us-ga:metro-atlanta:d01:cabinet-042",
		ContentType: "application/json",
		OccurredAt:  time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		Data:        []byte(payload),
	}
}

func TestNew_IDIsDeterministicAndContentSensitive(t *testing.T) {
	a := New(healthEvent(`{"v":1}`))
	b := New(healthEvent(`{"v":1}`))
	c := New(healthEvent(`{"v":2}`))

	if a.ID != b.ID {
		t.Error("identical inputs must produce identical IDs (JetStream dedup depends on it)")
	}
	if a.ID == c.ID {
		t.Error("different payloads must produce different IDs")
	}
	if len(a.ID) != 26 {
		t.Errorf("ID should be a 26-character ULID, got %d chars: %q", len(a.ID), a.ID)
	}
	if a.SpecVersion != "1.0" {
		t.Errorf("specversion = %q", a.SpecVersion)
	}
}

func TestNew_IDIgnoresProducerAssignedLeaves(t *testing.T) {
	// Two encodings of one occurrence that differ only in producer-assigned
	// bookkeeping — a restarted collector's sequence counter, say — must carry
	// the SAME id, or restart-invariance is lost and replay double-counts.
	ev := healthEvent(`{"v":1}`)
	ev.Identity = []byte("stable-identity")

	withSeq1 := ev
	withSeq1.Data = []byte(`{"v":1,"sequence":1}`)
	withSeq2 := ev
	withSeq2.Data = []byte(`{"v":1,"sequence":874}`)

	if New(withSeq1).ID != New(withSeq2).ID {
		t.Error("id changed with producer-assigned bookkeeping; it must derive from Identity, not Data")
	}
	// And the wire body must still carry the real bytes.
	if string(New(withSeq2).Data) != `{"v":1,"sequence":874}` {
		t.Error("Data must be the wire payload, not the identity projection")
	}
}

func TestNew_IdentityDefaultsToData(t *testing.T) {
	// Emitters with no producer-assigned leaves to clear (the health schema)
	// leave Identity nil, and must still get a content-derived id.
	ev := healthEvent(`{"v":1}`)
	explicit := ev
	explicit.Identity = ev.Data

	if New(ev).ID != New(explicit).ID {
		t.Error("a nil Identity must fall back to Data")
	}
}
