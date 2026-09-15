package maxtime

import (
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestParsePreemptDescriptions(t *testing.T) {
	got := parsePreemptDescriptions(map[string]string{
		"3": "NB%20-%20Ph%201%266",
		"4": "SB - Ph 2&5",
		"5": "WB - Ph 3&8",
		"6": "EB - Ph 4&7",
	})
	want := map[uint32]string{
		1: "NB", 6: "NB",
		2: "SB", 5: "SB",
		3: "WB", 8: "WB",
		4: "EB", 7: "EB",
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d (%v)", len(got), len(want), got)
	}
	for phase, approach := range want {
		if got[phase] != approach {
			t.Errorf("phase %d: got %q want %q", phase, got[phase], approach)
		}
	}
}

func TestDecodeMIBString(t *testing.T) {
	if got := decodeMIBString("Main%20St%20%28SR%201%29%20"); got != "Main St (SR 1)" {
		t.Fatalf("got %q", got)
	}
	if got := mibString(map[string]string{"1": "Second%20St-Preempt"}); got != "Second St-Preempt" {
		t.Fatalf("mibString=%q", got)
	}
}

func TestInventoryEnrichDetectorTransition(t *testing.T) {
	inv := &Inventory{}
	inv.setASC("Main St", "Second St", "047", map[uint32]string{1: "NB", 6: "NB", 2: "SB"})
	inv.setDetectors(map[uint32]DetectorInventory{
		3: {Lane: "NB thru", PhaseServed: 1},
	})
	events := []model.Event{
		model.DetectorTransition{Channel: 3, Kind: model.DetectorTransitionCallOn},
		model.DetectorTransition{Channel: 9, Kind: model.DetectorTransitionCallOn},
	}
	inv.EnrichEvents(events)
	got := events[0].(model.DetectorTransition)
	if got.PhaseServed != 1 || got.Approach != "NB" || got.Lane != "NB thru" {
		t.Fatalf("enriched = %+v", got)
	}
	if events[1].(model.DetectorTransition).Approach != "" {
		t.Fatalf("channel 9 should stay empty: %+v", events[1])
	}
}
