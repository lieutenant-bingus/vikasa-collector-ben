// Package adapter defines the vendor×device-kind integration surface.
// Adapters own transport entirely; their only obligation is to return
// sdk/model types (ADR 0002, 0003). Everything is pull (ADR 0004), and the
// one read path today is StateReader: an event-shaped reader or a command
// writer is declared together with its first implementer (ADR 0021).
package adapter

import (
	"context"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Capability is a bitset of what an adapter can do.
type Capability uint8

// CapState means the adapter implements StateReader: a poll returns a
// Snapshot the core diffs into events. It is the only capability today;
// the bitset leaves room for the next read path without a breaking change.
const CapState Capability = 1

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

// Factory builds an Adapter for one configured device. conn is the
// device's `connection` config block — opaque to the core, parsed here.
type Factory func(deviceID string, conn map[string]any) (Adapter, error)
