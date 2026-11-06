package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	typesv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/types/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeHighRes builds the notification for one high-resolution log row:
// unmapped-event, carrying the row's raw code and param in source (decoder
// "indiana"), so a consumer can always recover exactly what the controller
// logged.
func (e *emitter) encodeHighRes(v model.HighResEvent) (proto.Message, bool) {
	return &scv1.UnmappedEvent{
		Kind: scTypes + "sc-unmapped-event", SourceDeviceId: v.DeviceID,
		OccurredAt: timestamppb.New(v.OccurredAt.UTC()),
		ObservedBy: proto.String(e.collectorID), SequenceEpoch: e.epoch, Sequence: e.nextSequence(v.DeviceID),
		Source: &typesv1.WireSource{
			Decoder: "indiana",
			Tag: &typesv1.WireSource_Indiana_{Indiana: &typesv1.WireSource_Indiana{
				IndianaCode: proto.Uint32(uint32(v.Code)), IndianaParam: proto.Uint32(uint32(v.Param)),
			}},
		},
	}, true
}
