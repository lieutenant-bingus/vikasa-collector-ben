package stats

import (
	"errors"
	"testing"
	"time"
)

var at = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

// The spec's readiness rule (management surface §1): ready means at least
// one successful device poll, and it LATCHES — a comms outage that later
// takes every device offline must not make the collector unready, or the
// supervisor restart-loops it for a fault it did not cause.
func TestEverPolledLatches(t *testing.T) {
	d := NewDevices([]string{"asc-1", "dms-1"})
	if d.EverPolled() {
		t.Fatal("ready before any poll")
	}
	d.PollFailed("asc-1", at, errors.New("timeout"))
	if d.EverPolled() {
		t.Fatal("ready after only a failed poll")
	}
	d.PollOK("dms-1", at.Add(time.Second))
	for i := range 5 {
		d.PollFailed("dms-1", at.Add(time.Duration(i+2)*time.Second), errors.New("timeout"))
	}
	if !d.EverPolled() {
		t.Fatal("readiness un-latched when the device later failed")
	}
}

func TestDeviceSnapshotTracksLastOutcome(t *testing.T) {
	d := NewDevices([]string{"asc-1"})
	d.PollOK("asc-1", at)
	d.PollFailed("asc-1", at.Add(time.Second), errors.New("read timeout"))
	d.PollFailed("asc-1", at.Add(2*time.Second), errors.New("read timeout"))

	snap := d.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot = %+v, want one device", snap)
	}
	s := snap[0]
	if s.ID != "asc-1" || !s.LastSuccess.Equal(at) || s.LastError != "read timeout" || s.ConsecutiveFailures != 2 {
		t.Fatalf("device = %+v", s)
	}
	d.PollOK("asc-1", at.Add(3*time.Second))
	if s := d.Snapshot()[0]; s.ConsecutiveFailures != 0 || s.LastError != "read timeout" {
		t.Fatalf("after recovery = %+v, want failures reset and the last error kept for the technician", s)
	}
}

// A device never polled is listed, not omitted: "configured but silent" is
// exactly what a technician needs to see.
func TestUnpolledDevicesAreListed(t *testing.T) {
	d := NewDevices([]string{"b", "a"})
	snap := d.Snapshot()
	if len(snap) != 2 || snap[0].ID != "a" || !snap[0].LastSuccess.IsZero() {
		t.Fatalf("snapshot = %+v, want both devices sorted, never polled", snap)
	}
}
