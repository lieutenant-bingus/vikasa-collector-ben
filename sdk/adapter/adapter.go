// Package adapter defines the vendor×device-kind integration surface.
// Adapters own transport entirely; their only obligation is to return
// sdk/model types (ADR 0002, 0003). Everything is pull (ADR 0004).
package adapter

import (
	"context"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Capability is a bitset of what an adapter can do.
type Capability uint8

const (
	// CapState: implements StateReader — poll returns a Snapshot the core
	// diffs into events.
	CapState Capability = 1 << iota
	// CapEvents: implements EventReader — poll returns discrete events
	// (log fetchers); no diffing.
	CapEvents
	// CapCommand: implements Commander. Reserved seam; nothing dispatches
	// commands in v1 (ADR 0004).
	CapCommand
)

// Has reports whether c includes capability q.
func (c Capability) Has(q Capability) bool { return c&q != 0 }

// Descriptor identifies an adapter and its capabilities.
type Descriptor struct {
	Vendor     string // e.g. "ntcip", "econolite", "qfree"
	DeviceKind string // e.g. "asc", "rsu", "dms"
	Caps       Capability
}

// Key is the registry key: "<vendor>-<device_kind>".
func (d Descriptor) Key() string { return d.Vendor + "-" + d.DeviceKind }

// Adapter is the common surface every vendor×device-kind unit implements.
type Adapter interface {
	Descriptor() Descriptor
	Close() error
}

// StateReader polls the device and returns a normalized state snapshot.
type StateReader interface {
	Adapter
	Read(ctx context.Context) (*model.Snapshot, error)
}

// EventReader polls a source that yields discrete events (e.g. controller
// hi-res logs). Still pull; split from StateReader by semantics.
type EventReader interface {
	Adapter
	Fetch(ctx context.Context) ([]model.Event, error)
}

// Commander writes commands to the device. Dormant in v1.
type Commander interface {
	Adapter
	Execute(ctx context.Context, cmd model.Command) error
}

// Factory builds an Adapter for one configured device. conn is the
// device's `connection` config block — opaque to the core, parsed here.
type Factory func(deviceID string, conn map[string]any) (Adapter, error)
