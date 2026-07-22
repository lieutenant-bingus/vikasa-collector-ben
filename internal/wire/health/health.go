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

// ceTypeDeviceStatusChanged and ceTypeCollectorStarted are the collector-owned
// health ce-types (ADR 0007).
const (
	ceTypeDeviceStatusChanged = "openits-collector.health.device-status-changed.v1"
	ceTypeCollectorStarted    = "openits-collector.health.collector-started.v1"
)

func (emitter) CETypes() []string {
	// Sorted: boot validation and goldens both iterate this.
	return []string{ceTypeCollectorStarted, ceTypeDeviceStatusChanged}
}

type deviceStatusBody struct {
	DeviceID            string    `json:"device_id"`
	OccurredAt          time.Time `json:"occurred_at"`
	Reachable           bool      `json:"reachable"`
	Reason              string    `json:"reason"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

type collectorStartedBody struct {
	OccurredAt time.Time `json:"occurred_at"`
	Version    string    `json:"version"`
}

func (emitter) Encode(ev model.Event) (*wire.Encoded, bool, error) {
	switch e := ev.(type) {
	case model.DeviceStatusChanged:
		return encodeJSON(ceTypeDeviceStatusChanged, deviceStatusBody{
			DeviceID: e.DeviceID, OccurredAt: e.OccurredAt.UTC(),
			Reachable: e.Reachable, Reason: e.Reason, ConsecutiveFailures: e.ConsecutiveFailures,
		})
	case model.CollectorStarted:
		return encodeJSON(ceTypeCollectorStarted, collectorStartedBody{
			OccurredAt: e.OccurredAt.UTC(), Version: e.Version,
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
