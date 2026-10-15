package stats

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

// The JSON names are a contract: /status serves them and the ADR 0019
// heartbeat carries them, and hub-side queries key on them. Renaming one is
// a breaking change to every dashboard, so the set is pinned here.
func TestSnapshotJSONNamesArePinned(t *testing.T) {
	var c Counters
	c.EventsPublished.Add(3)
	c.DroppedNoEmitter.Add(1)
	raw, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]uint64
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"adapter_panics", "dropped_at_shutdown", "dropped_no_emitter", "dropped_no_source",
		"dropped_queue_full", "dropped_unpublishable", "encode_errors", "events_published",
		"facets_rejected", "logs_suppressed",
	}
	keys := slices.Collect(maps.Keys(got))
	slices.Sort(keys)
	if !slices.Equal(keys, want) {
		t.Fatalf("snapshot keys = %v, want %v", keys, want)
	}
	if got["events_published"] != 3 || got["dropped_no_emitter"] != 1 {
		t.Fatalf("snapshot values = %v", got)
	}
}
