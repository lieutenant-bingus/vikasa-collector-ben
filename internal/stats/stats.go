// Package stats holds the collector's own counters: the one metrics surface
// (ADR 0020 clause 4). Each counter is incremented where the event it counts
// happens — the publisher, the runners, the encode path — and Snapshot is the
// single read that both /status and the ADR 0019 heartbeat serve, so the two
// can never disagree.
//
// Every counter is cumulative since boot (ADR 0019 clause 4); a consumer
// detects a restart by boot_id, not by a counter going down. Device state
// (polls, reachability) is deliberately absent: it is reported per device on
// the catalog root, not as collector health (ADR 0019 clause 9).
package stats

import "sync/atomic"

// Counters is safe for concurrent use; the zero value is ready.
type Counters struct {
	// EventsPublished counts events the broker acknowledged.
	EventsPublished atomic.Uint64

	// The three ways the publisher loses an event (ADR 0020 clause 3).
	// Together they replace ADR 0019's single publish_failures, which
	// predates the queue and cannot say which loss happened.
	DroppedQueueFull     atomic.Uint64 // oldest queued event evicted to make room
	DroppedUnpublishable atomic.Uint64 // the broker will never accept it
	DroppedAtShutdown    atomic.Uint64 // still queued when the drain window closed

	// DroppedNoEmitter counts domain events no emitter claimed.
	DroppedNoEmitter atomic.Uint64
	// DroppedNoSource counts events with no ce-source entity kind for their
	// device kind, so no honest source URN to publish under.
	DroppedNoSource atomic.Uint64
	// EncodeErrors counts emitter failures encoding a claimed event.
	EncodeErrors atomic.Uint64

	// AdapterPanics counts Read panics the runner recovered (ADR 0019
	// clause 12: a collector fault, not a device fault).
	AdapterPanics atomic.Uint64
	// FacetsRejected counts facets the engine refused because their Go type
	// was not their differ's: an adapter bug, surfaced instead of panicking.
	FacetsRejected atomic.Uint64
	// LogsSuppressed counts warn-and-above log records not shipped as events
	// because the per-interval limit was reached (ADR 0019 clause 10).
	LogsSuppressed atomic.Uint64
}

// Snapshot is a point-in-time copy of Counters, with the JSON names /status
// and the heartbeat publish. The names are a contract with hub-side queries.
type Snapshot struct {
	EventsPublished      uint64 `json:"events_published"`
	DroppedQueueFull     uint64 `json:"dropped_queue_full"`
	DroppedUnpublishable uint64 `json:"dropped_unpublishable"`
	DroppedAtShutdown    uint64 `json:"dropped_at_shutdown"`
	DroppedNoEmitter     uint64 `json:"dropped_no_emitter"`
	DroppedNoSource      uint64 `json:"dropped_no_source"`
	EncodeErrors         uint64 `json:"encode_errors"`
	AdapterPanics        uint64 `json:"adapter_panics"`
	FacetsRejected       uint64 `json:"facets_rejected"`
	LogsSuppressed       uint64 `json:"logs_suppressed"`
}

// Snapshot reads every counter. Each value is individually exact; the set
// is not one atomic instant, which no consumer of cumulative counters needs.
func (c *Counters) Snapshot() Snapshot {
	return Snapshot{
		EventsPublished:      c.EventsPublished.Load(),
		DroppedQueueFull:     c.DroppedQueueFull.Load(),
		DroppedUnpublishable: c.DroppedUnpublishable.Load(),
		DroppedAtShutdown:    c.DroppedAtShutdown.Load(),
		DroppedNoEmitter:     c.DroppedNoEmitter.Load(),
		DroppedNoSource:      c.DroppedNoSource.Load(),
		EncodeErrors:         c.EncodeErrors.Load(),
		AdapterPanics:        c.AdapterPanics.Load(),
		FacetsRejected:       c.FacetsRejected.Load(),
		LogsSuppressed:       c.LogsSuppressed.Load(),
	}
}
