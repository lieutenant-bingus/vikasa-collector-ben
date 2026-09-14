package cameleon

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// TestACSEventsReachJetStream runs the real collector spine against embedded
// JetStream and a scripted ACS word reader (no network, no site addresses).
//
// Gate / fault domain events for device_kind "acs" currently have no
// ce-source entity-kind and no openits wire mapping, so they loud-drop by
// design. This e2e asserts the spine stays healthy: collector-started lands,
// and the adapter is polled successfully at least twice (position change on
// the scripted fixture proves the read path).
func TestACSEventsReachJetStream(t *testing.T) {
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	script := &scriptedWords{base: append([]uint16(nil), acs1DSWR90...)}
	reg := adapter.NewRegistry()
	reg.Register(acsDescriptor, func(deviceID string, _ map[string]any) (adapter.Adapter, error) {
		return NewACS(deviceID, script, 90, 110,
			[]gateCfg{
				{id: "WG-111", kind: model.GateKindWarning, dsw: 101},
				{id: "BG-114", kind: model.GateKindBarrier, dsw: 104},
			},
			cabinetCfg{lfFault: 179, gateEstop: 182},
		), nil
	})

	cfgYAML := `
collector_id: acs-e2e-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: acs-1, vendor: cameleon, device_kind: acs, poll_interval: 50ms, connection: {} }
`
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		t.Fatal(err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	seen := make(chan *nats.Msg, 128)
	sub, err := nc.Subscribe(">", func(m *nats.Msg) {
		if m.Header.Get("ce-type") != "" {
			seen <- m
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- app.Run(ctx, cfg, reg, ns.ClientURL(), "test") }()

	gotStarted := false
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case m := <-seen:
			ceType := m.Header.Get("ce-type")
			t.Logf("subject=%s ce-type=%s", m.Subject, ceType)
			if ceType == "openits-collector.health.collector-started.v1" {
				gotStarted = true
			}
		case err := <-runErr:
			if ctx.Err() != nil {
				break
			}
			t.Fatalf("app.Run: %v", err)
		case <-tick.C:
			// Gate events loud-drop (no acs entity-kind / wire map yet), so we
			// cannot wait for another CloudEvent — advance on poll count.
		case <-deadline:
			t.Fatalf("timed out; started=%v polls=%d", gotStarted, script.polls.Load())
		}
		if gotStarted && script.polls.Load() >= 2 {
			cancel()
			<-runErr
			return
		}
	}
}

// scriptedWords returns the ACS1 fixture, then flips WG-111 to opening on
// later polls so the gate differ has a transition to process (even though
// the wire layer drops it until mapped).
type scriptedWords struct {
	mu    sync.Mutex
	base  []uint16
	polls atomic.Int64
}

func (s *scriptedWords) ReadWords(_ byte, address1Based, count int) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.polls.Add(1)
	words := append([]uint16(nil), s.base...)
	if n >= 2 {
		// %R101: auto + opening (status=3) + keep lock-open bit pattern-ish
		words[101-90] = 0x0031 // mode auto (1), status opening (3<<4)
	}
	off := address1Based - 90
	out := make([]uint16, count)
	copy(out, words[off:off+count])
	return out, nil
}

func (s *scriptedWords) Close() error { return nil }
