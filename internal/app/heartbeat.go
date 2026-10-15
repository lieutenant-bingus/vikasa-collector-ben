package app

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/health"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// phaseFor is a collector's stable offset within the heartbeat interval
// (ADR 0019 clause 5): derived from its identity, so it is the same every
// boot and spread across a fleet — no thundering herd of aligned beats, and
// no re-randomised arrival times for a hub's dead-man timer to guess at.
func phaseFor(collectorID string, interval time.Duration) time.Duration {
	h := fnv.New64a()
	_, _ = h.Write([]byte(collectorID))
	return time.Duration(h.Sum64() % uint64(interval))
}

// nextBeat is the first wall-clock instant after now that is ≡ phase
// (mod interval). Scheduling on these slots, rather than on a ticker
// started at boot, keeps a collector's beats on the same cadence across
// restarts.
func nextBeat(now time.Time, interval, phase time.Duration) time.Time {
	since := now.UnixNano() - int64(phase)
	slot := since - since%int64(interval) + int64(phase)
	next := time.Unix(0, slot).UTC()
	for !next.After(now) {
		next = next.Add(interval)
	}
	return next
}

// runHeartbeat emits a heartbeat on every slot until ctx is done. Each beat
// re-arms a timer to the next aligned slot, so the cadence never drifts.
func runHeartbeat(ctx context.Context, interval time.Duration, inst health.Instance, started time.Time,
	counters *stats.Counters, sink func([]model.Event)) {
	phase := phaseFor(inst.CollectorID, interval)
	for {
		now := time.Now().UTC()
		timer := time.NewTimer(nextBeat(now, interval, phase).Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case at := <-timer.C:
			sink([]model.Event{health.Heartbeat{
				Base: model.Base{OccurredAt: at.UTC()}, Instance: inst,
				Uptime: at.Sub(started), Resource: health.ReadResource(), Counters: counters.Snapshot(),
			}})
		}
	}
}
