// Package health encodes health events in the collector-owned schema
// (ADR 0007): ce-types openits-collector.health.*.v1, JSON bodies.
package health

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewHealthEmitter returns the emitter for collector-owned health events.
func NewHealthEmitter() wire.Emitter { return emitter{} }

type emitter struct{}

// The collector-owned ce-types (ADR 0007). device-status-changed is still
// here until device reachability moves to the catalog (ADR 0019 Scope);
// the instance.* family is the collector's own health.
const (
	ceTypeDeviceStatusChanged = "openits-collector.health.device-status-changed.v1"
	ceTypeStarted             = "openits-collector.instance.started.v1"
	ceTypeStopping            = "openits-collector.instance.stopping.v1"
	ceTypeHeartbeat           = "openits-collector.instance.heartbeat.v1"
	ceTypeLog                 = "openits-collector.instance.log.v1"
)

func (emitter) CETypes() []string {
	// Sorted: boot validation and goldens both iterate this.
	return []string{ceTypeDeviceStatusChanged, ceTypeHeartbeat, ceTypeLog, ceTypeStarted, ceTypeStopping}
}

type deviceStatusBody struct {
	DeviceID            string    `json:"device_id"`
	OccurredAt          time.Time `json:"occurred_at"`
	Reachable           bool      `json:"reachable"`
	Reason              string    `json:"reason"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

func (emitter) Encode(ev model.Event) (*wire.Encoded, bool, error) {
	switch e := ev.(type) {
	case model.DeviceStatusChanged:
		return encodeJSON(ceTypeDeviceStatusChanged, deviceStatusBody{
			DeviceID: e.DeviceID, OccurredAt: e.OccurredAt.UTC(),
			Reachable: e.Reachable, Reason: e.Reason, ConsecutiveFailures: e.ConsecutiveFailures,
		})
	case Started:
		return encodeJSON(ceTypeStarted, identity(e.Base, e.Instance))
	case Stopping:
		return encodeJSON(ceTypeStopping, stoppingBody{
			identityBody: identity(e.Base, e.Instance), UptimeSeconds: int64(e.Uptime / time.Second),
		})
	case Log:
		attrs := e.Attrs
		if attrs == nil {
			attrs = map[string]string{} // always an object on the wire, never null
		}
		return encodeJSON(ceTypeLog, logBody{
			identityBody: identity(e.Base, e.Instance), Level: e.Level, Message: e.Message, Attrs: attrs,
		})
	case Heartbeat:
		return encodeJSON(ceTypeHeartbeat, heartbeatBody{
			identityBody: identity(e.Base, e.Instance), UptimeSeconds: int64(e.Uptime / time.Second),
			CPUSecondsTotal: e.Resource.CPUSecondsTotal, MemoryBytes: e.Resource.MemoryBytes,
			HeapObjectsBytes: e.Resource.HeapObjectsBytes, Goroutines: e.Resource.Goroutines,
			Snapshot: e.Counters,
		})
	default:
		return nil, false, nil
	}
}

func encodeJSON(ceType string, body any) (*wire.Encoded, bool, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("health encode %s: %w", ceType, err)
	}
	return &wire.Encoded{CEType: ceType, ContentType: "application/json", Data: data}, true, nil
}
