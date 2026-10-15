package health

import (
	"runtime/metrics"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// The collector's own events (ADR 0019): the openits-collector root carries
// the collector instance's health and nothing else. They are defined here,
// beside their wire schema, rather than in sdk/model: no adapter produces or
// consumes them.

// Instance is the identity every instance event states (ADR 0019 clause 7):
// collector_id in the body, while ce-source carries the site.
type Instance struct {
	CollectorID    string
	Version        string
	ConfigRevision string
	BootID         string
}

// Started fires once at boot, before any device is polled.
type Started struct {
	model.Base
	Instance
}

// EventKind implements model.Event.
func (Started) EventKind() string { return "collector-started" }

// Stopping fires on a clean shutdown, best-effort: a killed process cannot
// send it, and a started with no preceding stopping is how a consumer
// recognises an unclean restart (ADR 0019 clause 8).
type Stopping struct {
	model.Base
	Instance
	Uptime time.Duration
}

// EventKind implements model.Event.
func (Stopping) EventKind() string { return "collector-stopping" }

// Heartbeat is the periodic self-report (ADR 0019 clause 9): identity,
// resource use, and the collector's cumulative counters. It carries no
// device state.
type Heartbeat struct {
	model.Base
	Instance
	Uptime   time.Duration
	Resource Resource
	Counters stats.Snapshot
}

// EventKind implements model.Event.
func (Heartbeat) EventKind() string { return "collector-heartbeat" }

// Log is one warn-and-above record shipped from the collector's own log
// (ADR 0019 clause 10), with its structured attributes kept.
type Log struct {
	model.Base
	Instance
	Level   string
	Message string
	Attrs   map[string]string
}

// EventKind implements model.Event.
func (Log) EventKind() string { return "collector-log" }

// Resource is the process's own resource use. CPU is cumulative seconds, not
// a percentage: the hub differentiates, and a busy loop shows as CPU
// climbing against a flat heap. The runtime refreshes its CPU estimate at
// each GC cycle, so it lags slightly behind real time; a long-running,
// allocating process collects continually, which is what the heartbeat
// samples.
type Resource struct {
	CPUSecondsTotal  float64
	MemoryBytes      uint64
	HeapObjectsBytes uint64
	Goroutines       uint64
}

var resourceMetrics = []string{
	"/cpu/classes/total:cpu-seconds",
	"/memory/classes/total:bytes",
	"/memory/classes/heap/objects:bytes",
	"/sched/goroutines:goroutines",
}

// ReadResource takes one runtime/metrics read: no syscall, no /proc parsing.
func ReadResource() Resource {
	samples := make([]metrics.Sample, len(resourceMetrics))
	for i, name := range resourceMetrics {
		samples[i].Name = name
	}
	metrics.Read(samples)
	var r Resource
	if v := samples[0].Value; v.Kind() == metrics.KindFloat64 {
		r.CPUSecondsTotal = v.Float64()
	}
	if v := samples[1].Value; v.Kind() == metrics.KindUint64 {
		r.MemoryBytes = v.Uint64()
	}
	if v := samples[2].Value; v.Kind() == metrics.KindUint64 {
		r.HeapObjectsBytes = v.Uint64()
	}
	if v := samples[3].Value; v.Kind() == metrics.KindUint64 {
		r.Goroutines = v.Uint64()
	}
	return r
}

// The bodies are flat on purpose: asyncapi.yaml's conformance test checks
// every top-level key against the emitted bytes, and a hub-side fleet table
// queries flat fields.

type identityBody struct {
	OccurredAt     time.Time `json:"occurred_at"`
	CollectorID    string    `json:"collector_id"`
	Version        string    `json:"version"`
	ConfigRevision string    `json:"config_revision"`
	BootID         string    `json:"boot_id"`
}

func identity(b model.Base, i Instance) identityBody {
	return identityBody{
		OccurredAt: b.OccurredAt.UTC(), CollectorID: i.CollectorID, Version: i.Version,
		ConfigRevision: i.ConfigRevision, BootID: i.BootID,
	}
}

type stoppingBody struct {
	identityBody
	UptimeSeconds int64 `json:"uptime_s"`
}

type heartbeatBody struct {
	identityBody
	UptimeSeconds    int64   `json:"uptime_s"`
	CPUSecondsTotal  float64 `json:"cpu_seconds_total"`
	MemoryBytes      uint64  `json:"memory_bytes"`
	HeapObjectsBytes uint64  `json:"heap_objects_bytes"`
	Goroutines       uint64  `json:"goroutines"`
	stats.Snapshot
}

type logBody struct {
	identityBody
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs"`
}
