package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewDetectorChannelDiffer diffs detector call/presence bits: one transition
// event per channel edge observed between polls.
func NewDetectorChannelDiffer() Differ { return detectorChannelDiffer{} }

type detectorChannelDiffer struct{}

func (detectorChannelDiffer) Kind() model.Kind { return model.KindDetectorChannelStatuses }

func (detectorChannelDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	if prev == nil {
		return nil
	}
	prevByCh := indexDetectorChannels(prev.(model.DetectorChannelStatuses))
	currCh := curr.(model.DetectorChannelStatuses)

	var events []model.Event
	for _, ch := range currCh.Channels {
		old, ok := prevByCh[ch.Channel]
		if !ok {
			continue
		}
		if old.CallActive != ch.CallActive {
			kind := model.DetectorTransitionCallOff
			if ch.CallActive {
				kind = model.DetectorTransitionCallOn
			}
			events = append(events, model.DetectorTransition{
				Base: base, Channel: ch.Channel, Kind: kind,
			})
		}
		if old.PresenceActive != ch.PresenceActive {
			kind := model.DetectorTransitionPresenceOff
			if ch.PresenceActive {
				kind = model.DetectorTransitionPresenceOn
			}
			events = append(events, model.DetectorTransition{
				Base: base, Channel: ch.Channel, Kind: kind,
			})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i].(model.DetectorTransition), events[j].(model.DetectorTransition)
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		return a.Kind < b.Kind
	})
	return events
}

func indexDetectorChannels(ds model.DetectorChannelStatuses) map[uint32]model.DetectorChannelStatus {
	out := make(map[uint32]model.DetectorChannelStatus, len(ds.Channels))
	for _, ch := range ds.Channels {
		out[ch.Channel] = ch
	}
	return out
}
