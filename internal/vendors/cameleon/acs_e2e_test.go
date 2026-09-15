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
// Asserts collector-started, then a gate-position-changed CloudEvent after the
// scripted fixture flips WG-111 from closed to opening on the second poll.
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
	gotGatePos := false
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-seen:
			ceType := m.Header.Get("ce-type")
			t.Logf("subject=%s ce-type=%s ce-source=%s", m.Subject, ceType, m.Header.Get("ce-source"))
			if ceType == "openits-collector.health.collector-started.v1" {
				gotStarted = true
			}
			if ceType == "openits.reversible-lane.gate-position-changed.v1" {
				gotGatePos = true
				if src := m.Header.Get("ce-source"); src != "urn:openits:reversible-lane:us-ga:metro:d01:acs-1" {
					t.Errorf("ce-source = %q, want reversible-lane URN", src)
				}
			}
		case err := <-runErr:
			if ctx.Err() != nil {
				break
			}
			t.Fatalf("app.Run: %v", err)
		case <-deadline:
			t.Fatalf("timed out; started=%v gate-position=%v polls=%d", gotStarted, gotGatePos, script.polls.Load())
		}
		if gotStarted && gotGatePos {
			cancel()
			<-runErr
			return
		}
	}
}

// scriptedWords returns the ACS1 fixture, then flips WG-111 to opening on
// later polls so the gate differ emits gate-position-changed onto the wire.
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
		// %R101: auto + opening (status=3)
		words[101-90] = 0x0031 // mode auto (1), status opening (3<<4)
	}
	off := address1Based - 90
	out := make([]uint16, count)
	copy(out, words[off:off+count])
	return out, nil
}

func (s *scriptedWords) Close() error { return nil }
