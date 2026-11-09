package runner

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// The watchdog's evidence. A hung adapter Read is abandoned at the poll
// timeout, so its loop keeps turning and is not stalled: the device is down,
// the collector is fine. A loop that stops turning — here, a sink that
// blocks, which it must never do — is the collector hung, and is reported.
func TestLivenessSeparatesAHungDeviceFromAHungLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		live := stats.NewLiveness()
		h := newHanging()
		defer close(h.release)
		r := New(h, Config{
			Jitter: noJitter, DeviceID: "asc-1", Interval: time.Second, Timeout: time.Second,
			Engine: synth.NewEngine(synth.NewSignalDiffer()), Out: func([]model.Event) {}, Liveness: live,
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go r.Run(ctx)
		time.Sleep(time.Minute)
		if got := live.Stalled(time.Now()); len(got) != 0 {
			t.Fatalf("a hung device stalled its loop: %v", got)
		}

		block := make(chan struct{})
		defer close(block)
		// Its first failed poll emits a reachability event into the sink.
		down := &scriptedAdapter{script: []func() (*model.Snapshot, error){
			func() (*model.Snapshot, error) { return nil, errors.New("timeout") },
		}}
		blocked := New(down, Config{
			Jitter: noJitter, DeviceID: "asc-2", Interval: time.Second, Timeout: time.Second,
			Engine: synth.NewEngine(synth.NewSignalDiffer()),
			Out:    func([]model.Event) { <-block }, Liveness: live,
		})
		go blocked.Run(ctx)
		time.Sleep(time.Minute)
		if got := live.Stalled(time.Now()); len(got) != 1 || got[0] != "asc-2" {
			t.Fatalf("stalled = %v; want [asc-2], the loop whose sink blocks", got)
		}
	})
}
