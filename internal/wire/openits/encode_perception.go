package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pcpv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/perception/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodePerception builds the payload for a perception event.
func (e *emitter) encodePerception(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.ZoneIncidentDetected:
		d := &pcpv1.ZoneIncidentDetected{
			Kind:           perceptionTypes + "pcp-zone-incident-detected",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			IncidentId:     proto.String(v.IncidentID),
			ZoneId:         proto.String(v.ZoneID),
			Type:           optString(incidentTypeIdentity(v.Type)),
			Severity:       incidentSeverityFor(v.Severity),
			ObjectClass:    proto.String(objectClassIdentity(v.ObjectClass)),
			Confidence:     proto.Uint32(uint32(v.ConfidencePercent)),
			TrackId:        proto.Uint32(v.TrackID),
			TrackEpoch:     proto.Uint32(v.TrackEpoch),
		}
		// Zero is meaningful here — a stopped-vehicle incident IS zero km/h —
		// so an unreported speed stays absent rather than rendering as stopped.
		if v.SpeedReported {
			d.SpeedKmh = proto.String(hundredths(v.SpeedHundredthsKPH))
		}
		msg = d

	case model.ZoneIncidentUpdated:
		u := &pcpv1.ZoneIncidentUpdated{
			Kind:           perceptionTypes + "pcp-zone-incident-updated",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			IncidentId:     proto.String(v.IncidentID),
			ZoneId:         proto.String(v.ZoneID),
			Severity:       incidentSeverityFor(v.Severity),
			Confidence:     proto.Uint32(uint32(v.ConfidencePercent)),
		}
		if v.SpeedReported {
			u.SpeedKmh = proto.String(hundredths(v.SpeedHundredthsKPH))
		}
		msg = u

	case model.ZoneIncidentCleared:
		msg = &pcpv1.ZoneIncidentCleared{
			Kind:           perceptionTypes + "pcp-zone-incident-cleared",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			IncidentId:     proto.String(v.IncidentID),
			ZoneId:         proto.String(v.ZoneID),
		}

	case model.ZoneIntervalReport:
		zones := make([]*pcpv1.ZoneIntervalReportZone, 0, len(v.Zones))
		for _, z := range v.Zones {
			counts := make([]*pcpv1.ClassCount, 0, len(z.CrossedClassCounts))
			for _, cc := range z.CrossedClassCounts {
				counts = append(counts, &pcpv1.ClassCount{
					Class: proto.String(objectClassIdentity(cc.Class)), Count: proto.Uint32(cc.Count),
				})
			}
			// ObservedCount and OccupancyPercent are deliberately absent:
			// v0.3.0 removed both leaves from this notification and moved
			// them to zone-occupancy-interval-report, which the same poll
			// also emits. The values are not dropped, they are published
			// under the ce-type that now defines them.
			zone := &pcpv1.ZoneIntervalReportZone{
				ZoneId:            proto.String(z.ZoneID),
				CrossedVolume:     proto.Uint32(z.CrossedVolume),
				ClassCount:        counts,
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: proto.Uint32(wholeSeconds(v.IntervalDuration)),
			}
			if z.SpeedReported {
				zone.AverageSpeedKmh = proto.String(hundredths(z.SpeedAvgHundredthsKPH))
			}
			zones = append(zones, zone)
		}
		msg = &pcpv1.ZoneIntervalReport{
			Kind:           perceptionTypes + "pcp-zone-interval-report",
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
