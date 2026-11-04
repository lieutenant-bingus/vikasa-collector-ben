// Package adapter defines the vendor×device-kind integration surface.
// Adapters own transport entirely; their only obligation is to return
// sdk/model types (ADR 0002, 0003). Everything is pull (ADR 0004). There are
// two read paths: StateReader, which every adapter implements, and
// EventReader, which an adapter for a device with an event log implements as
// well (ADR 0025). A command writer is declared together with its first
// implementer (ADR 0021).
package adapter

import (
	"context"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Capability is a bitset of what an adapter can do.
type Capability uint8

const (
	// CapState means the adapter implements StateReader: a poll returns a
	// Snapshot the core diffs into events. Every adapter has it.
	CapState Capability = 1 << iota
	// CapEvents means the adapter also implements EventReader: it reads a
	// log of discrete events the device keeps, which the core publishes as
	// they are, without diffing (ADR 0025). A registered Descriptor sets it
	// when the adapter can read a log; a built adapter's Descriptor sets it
	// only when this device is configured to.
	CapEvents
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

// EventReader reads a log the device keeps of discrete, timestamped events —
// a signal controller's high-resolution log. It is pull, like StateReader,
// and is called on its own loop (ADR 0025).
//
// The core owns the checkpoint. after is the cursor ReadEvents returned last
// time, persisted across restarts; nil means there is none (first boot, or a
// checkpoint this build cannot use). ReadEvents returns the events logged
// after it, oldest first, and the cursor that follows the last one; the core
// publishes the events, then stores next.
//
// With after nil, ReadEvents returns no events and the cursor of the log's
// current end: a first boot never replays the device's history as if it
// had just happened. A cursor the log no longer reaches — the device
// rebooted and renumbered, or the log wrapped past it — is the adapter's to
// recognise: it resynchronises (returns what it can place after the cursor,
// or nothing and a fresh end-of-log cursor) rather than failing every read
// until an operator intervenes.
//
// Each event's OccurredAt is the device's own timestamp for it, in UTC. Its
// DeviceID and DeviceKind are stamped by the core (model.LogEvent), never by
// the adapter.
//
// A cursor is opaque to the core and at most 1 KiB. An error means the log
// could not be read; the core keeps after and retries on the next interval.
type EventReader interface {
	Adapter
	ReadEvents(ctx context.Context, after []byte) (events []model.LogEvent, next []byte, err error)
	// EventInterval is how often ReadEvents is called.
	EventInterval() time.Duration
}

// MaxCursorBytes bounds an EventReader cursor.
const MaxCursorBytes = 1 << 10

// Factory builds an Adapter for one configured device. conn is the
// device's `connection` config block — opaque to the core, parsed here.
type Factory func(deviceID string, conn map[string]any) (Adapter, error)
