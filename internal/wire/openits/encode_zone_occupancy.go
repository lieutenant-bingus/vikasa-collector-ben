package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	zocv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/zone_occupancy/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeZoneOccupancy builds the payload for a zone-occupancy event.
func (e *emitter) encodeZoneOccupancy(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.ZoneOccupancyIntervalReport:
		zones := make([]*zocv1.Zone, 0, len(v.Zones))
		for _, z := range v.Zones {
			classes := make([]*zocv1.ObservedClass, 0, len(z.ObservedClassCounts))
			for _, cc := range z.ObservedClassCounts {
				// MeanConfidence is left unset: the domain carries no
				// per-class confidence, and zero is the wire's "not stated"
				// for a uint32 count of confidence, not a claim of none.
				classes = append(classes, &zocv1.ObservedClass{
					Class: proto.String(objectClassIdentity(cc.Class)), Count: proto.Uint32(cc.Count),
				})
			}
			// PeakOccupancyCount is left unset for the same reason: no
			// adapter observes a within-interval peak today, and inventing
			// one from the mean would be a wrong value rather than a missing
			// one.
			zones = append(zones, &zocv1.Zone{
				ZoneId:            proto.String(z.ZoneID),
				ObservedCount:     proto.Uint32(z.ObservedCount),
				OccupancyPercent:  proto.String(occupancyPercent(z.OccupancyTenths)),
				ObservedClass:     classes,
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: proto.Uint32(wholeSeconds(v.IntervalDuration)),
			})
		}
		msg = &zocv1.ZoneOccupancyIntervalReport{
			Kind:           zoneOccTypes + "zoc-zone-occupancy-interval-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			Zone:           zones,
		}
	default:
		return nil, false
	}
	return msg, true
}
