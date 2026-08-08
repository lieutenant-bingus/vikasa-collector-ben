// Package openits maps collector domain events to openits-models wire
// payloads. It is one of the two emitter families described in ADR 0002,
// and the only place in the collector that imports openits-models.
package openits

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// contentType is the profile's per-event protobuf encoding.
const contentType = "application/protobuf"

// New returns the openits-models emitter. collectorID identifies this
// collector as the OBSERVER of events it synthesizes from polling.
func New(collectorID string) wire.Emitter { return &emitter{collectorID: collectorID} }

type emitter struct {
	collectorID string
}

// key is the emitter's dispatch key. It is the TUPLE (event kind, device
// kind), never the event kind alone: the catalog defines mode-changed and
// fault-raised ONCE and reuses them across services, so the same domain
// shape publishes as signal-control.mode-changed or dms.mode-changed
// depending only on the device. Keying on EventKind alone appears to work
// until a second device kind reuses a shared event, then silently resolves
// to whichever entry was registered last.
type key struct{ event, deviceKind string }

// ceTypeFor is the complete domain→ce-type routing table.
var ceTypeFor = map[key]string{
	{"mode-changed", "asc"}: "openits.signal-control.mode-changed.v1",
}

func (e *emitter) CETypes() []string { return nil }

func (e *emitter) Encode(ev model.Event) (*wire.Encoded, bool, error) {
	ceType, ok := ceTypeFor[key{ev.EventKind(), ev.EventDeviceKind()}]
	if !ok {
		return nil, false, nil
	}

	var msg proto.Message
	switch v := ev.(type) {
	case model.ModeChanged:
		msg = &commonv1.ModeChanged{
			SourceDeviceId: v.DeviceID,
		}
	default:
		return nil, false, nil
	}

	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return nil, false, fmt.Errorf("openits encode %s: %w", ceType, err)
	}
	return &wire.Encoded{CEType: ceType, ContentType: contentType, Data: data}, true, nil
}
