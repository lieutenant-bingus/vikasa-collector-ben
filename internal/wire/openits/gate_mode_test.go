package openits

import (
	"testing"

	"google.golang.org/protobuf/proto"

	rlv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/reversible_lane/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestManualNonInterlockedOmitsOperatingMode(t *testing.T) {
	enc, ok, err := New("collector").Encode(model.GateStatusChanged{
		Base:             gbase("acs-1", "acs"),
		GateID:           "WG-111",
		Position:         model.GatePositionClosed,
		PreviousPosition: model.GatePositionClosed,
		Mode:             model.GateModeManualNonInterlocked,
		PreviousMode:     model.GateModeAuto,
	})
	if err != nil || !ok {
		t.Fatalf("encode ok=%v err=%v", ok, err)
	}
	var msg rlv1.GateStatusChanged
	if err := proto.Unmarshal(enc.Data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.OperatingMode != nil {
		t.Fatalf("operating mode = %q, want unset", msg.GetOperatingMode())
	}
	const prev = "openits-reversible-lane-types:reversible-lane-gate-mode-automatic"
	if msg.GetPreviousOperatingMode() != prev {
		t.Fatalf("previous = %q", msg.GetPreviousOperatingMode())
	}
}
