package app

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/sdnotify"
	"github.com/Vikasa2M/vikasa-collector/internal/stats"
)

// notifyReady sends READY=1 when the first device answers: the moment
// /readyz latches (management-surface spec §1). It says "working", not
// "running", so systemd's start and podman auto-update's rollback gate on a
// collector that has proven it can poll.
func notifyReady(ctx context.Context, log *slog.Logger, n *sdnotify.Notifier, devices *stats.Devices) {
	select {
	case <-devices.Ready():
		if err := n.Notify("READY=1", "STATUS=polling; a device has answered"); err != nil {
			log.Warn("could not tell the supervisor the collector is ready", "err", err)
		}
	case <-ctx.Done():
	}
}

// feedWatchdog pings the supervisor's watchdog at half its timeout while
// every poll loop is turning. A device that is down does not stop it — its
// loop still completes rounds — so only a hung collector goes quiet, and the
// supervisor restarts it.
func feedWatchdog(ctx context.Context, log *slog.Logger, n *sdnotify.Notifier, timeout time.Duration, live *stats.Liveness) {
	tick := time.NewTicker(timeout / 2)
	defer tick.Stop()
	var withheld []string
	for {
		stalled := live.Stalled(time.Now())
		if len(stalled) == 0 {
			if err := n.Notify("WATCHDOG=1"); err != nil {
				log.Warn("could not feed the supervisor's watchdog", "err", err)
			}
		} else if !slices.Equal(stalled, withheld) {
			log.Error("poll loops have stopped turning; withholding the watchdog so the supervisor restarts the collector",
				"devices", stalled)
		}
		withheld = stalled
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}
