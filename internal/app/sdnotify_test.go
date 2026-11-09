package app

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/sdnotify"
	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// notifySocket stands in for systemd's: it collects every datagram.
func notifySocket(t *testing.T) (*sdnotify.Notifier, <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sdn") // short: macOS caps socket paths at 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "notify")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	got := make(chan string, 1024)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			got <- string(buf[:n])
		}
	}()
	n, err := sdnotify.New(path)
	if err != nil {
		t.Fatal(err)
	}
	return n, got
}

func waitFor(t *testing.T, got <-chan string, want string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case d := <-got:
			if strings.Contains(d, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no %q within %v", want, within)
		}
	}
}

// READY=1 means what /readyz means — a device has answered — so systemd and
// podman auto-update gate on "working", not "running". Until then the
// collector polls and reports normally; it just has not proven itself. On
// shutdown it says STOPPING=1, and while its poll loops turn it feeds the
// watchdog.
func TestSupervisorIsToldReadyOnTheFirstGoodPoll(t *testing.T) {
	dev := &switchableASC{}
	dev.failing.Store(true)
	n, got := notifySocket(t)

	reg := adapter.NewRegistry()
	reg.Register(dev.Descriptor(), func(string, map[string]any) (adapter.Adapter, error) { return dev, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 10 * time.Millisecond}},
	}
	noBroker := "nats://" + freeAddr(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, reg, Options{
			NATSURL: noBroker, Version: "test",
			Notify: n, Watchdog: 200 * time.Millisecond,
		})
	}()
	defer func() { cancel(); <-done }()

	// Watchdog pings flow while the device is down: a dead device is the
	// collector working, not hung.
	waitFor(t, got, "WATCHDOG=1", 2*time.Second)
	for range len(got) {
		if d := <-got; strings.Contains(d, "READY=1") {
			t.Fatalf("sent %q before any device answered", d)
		}
	}

	dev.failing.Store(false)
	waitFor(t, got, "READY=1", 5*time.Second)

	cancel()
	waitFor(t, got, "STOPPING=1", 5*time.Second)
}

// The watchdog is only worth arming if a hung collector goes quiet: pings
// stop while any poll loop is stalled, and resume when it turns again.
func TestWatchdogIsWithheldWhileALoopIsStalled(t *testing.T) {
	n, got := notifySocket(t)
	live := stats.NewLiveness()
	live.Expect("asc-1", time.Hour, time.Now().Add(-2*time.Hour)) // already stalled
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { feedWatchdog(ctx, slog.New(slog.DiscardHandler), n, 40*time.Millisecond, live); close(done) }()
	defer func() { cancel(); <-done }()

	select {
	case d := <-got:
		t.Fatalf("sent %q while a loop was stalled", d)
	case <-time.After(300 * time.Millisecond):
	}
	live.Beat("asc-1", time.Now())
	waitFor(t, got, "WATCHDOG=1", 2*time.Second)
}
