package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewSignalIndicationDiffer diffs RYG channel colors into transition events.
func NewSignalIndicationDiffer() Differ { return signalIndicationDiffer{} }

type signalIndicationDiffer struct{}

func (signalIndicationDiffer) Kind() model.Kind { return model.KindSignalIndications }

func (signalIndicationDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	if prev == nil {
		return nil
	}
	prevBy := indexIndications(prev.(model.SignalIndications))
	currInd := curr.(model.SignalIndications)
	var events []model.Event
	for _, ch := range currInd.Channels {
		old, ok := prevBy[ch.Channel]
		if !ok || old.Color == ch.Color {
			continue
		}
		events = append(events, model.SignalIndicationChanged{
			Base: base, Channel: ch.Channel, From: old.Color, To: ch.Color,
		})
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i].(model.SignalIndicationChanged), events[j].(model.SignalIndicationChanged)
		return a.Channel < b.Channel
	})
	return events
}

func indexIndications(s model.SignalIndications) map[uint32]model.ChannelIndication {
	out := make(map[uint32]model.ChannelIndication, len(s.Channels))
	for _, ch := range s.Channels {
		out[ch.Channel] = ch
	}
	return out
}
