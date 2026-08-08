package cloudevents

import (
	"encoding/hex"
	"testing"
	"time"
)

// The vectors published in openits-models docs/ce-id-spec.md. Any
// implementation of the algorithm must reproduce both exactly.
func TestEventID_ReproducesPublishedVectors(t *testing.T) {
	const (
		source = "urn:openits:sign:us-xx:example-agency:d01:demo-sign-1"
		ceType = "openits.dms.message-activation-failed.v1"
	)
	// openits.dms.v1.MessageActivationFailed{reason: "validation"}
	payload, err := hex.DecodeString("1a0a76616c69646174696f6e")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		stable string
		want   string
	}{{
		"vector 1 — occurred-at equals ce-time",
		"2026-07-22T12:00:00.000Z",
		"01KY4V4VG09C9D44NNQCDWKJCN",
	}, {
		// The vector that matters: it is the only one that can catch sourcing
		// the ULID timestamp from ce-time instead of stable-time, because in
		// vector 1 the two coincide.
		"vector 2 — backfill, occurred-at before ce-time",
		"2026-07-22T11:59:00.000Z",
		"01KY4V30X08F697N951X9F30AS",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			stable, err := time.Parse(time.RFC3339, tc.stable)
			if err != nil {
				t.Fatal(err)
			}
			if got := EventID(source, ceType, stable, payload); got != tc.want {
				t.Errorf("EventID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEventID_IsObserverInvariant(t *testing.T) {
	// Two collectors observing one event at different wall-clock instants must
	// agree on the id. Nothing but the arguments below feeds it — in
	// particular, no clock is read inside EventID.
	stable := time.Date(2026, 7, 22, 11, 59, 0, 0, time.UTC)
	a := EventID("urn:openits:controller:us-tx:txdot:d07:i35", "openits.signal-control.mode-changed.v1", stable, []byte("x"))
	b := EventID("urn:openits:controller:us-tx:txdot:d07:i35", "openits.signal-control.mode-changed.v1", stable, []byte("x"))
	if a != b {
		t.Errorf("EventID is not deterministic: %q vs %q", a, b)
	}
}

func TestEventID_IsSensitiveToEveryInput(t *testing.T) {
	stable := time.Date(2026, 7, 22, 11, 59, 0, 0, time.UTC)
	ref := EventID("src-a", "type-a", stable, []byte("payload-a"))

	for name, got := range map[string]string{
		"source":  EventID("src-b", "type-a", stable, []byte("payload-a")),
		"type":    EventID("src-a", "type-b", stable, []byte("payload-a")),
		"time":    EventID("src-a", "type-a", stable.Add(time.Second), []byte("payload-a")),
		"payload": EventID("src-a", "type-a", stable, []byte("payload-b")),
	} {
		if got == ref {
			t.Errorf("changing %s did not change the id (%q)", name, got)
		}
	}
}

func TestEventID_SeparatorIsNotAmbiguous(t *testing.T) {
	// Concatenating without a separator would make ("ab","c") and ("a","bc")
	// hash identically. The 0x1f unit separator is what prevents that, and it
	// is why the spec pins the separator rather than leaving it to taste.
	stable := time.Date(2026, 7, 22, 11, 59, 0, 0, time.UTC)
	if EventID("ab", "c", stable, nil) == EventID("a", "bc", stable, nil) {
		t.Error("field boundaries are ambiguous: adjacent fields can be shifted without changing the id")
	}
}
