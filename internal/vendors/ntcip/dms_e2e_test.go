package ntcip

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// TestDMSEventsReachJetStream runs the real collector spine against
// embedded JetStream and ntcip-dms reading a scripted SNMP fixture.
// Poll 1 establishes baseline (sign-status-report from env); poll 2
// changes the face message and control mode so message-changed and
// mode-changed also land. Logs every CloudEvent that reaches the bus.
func TestDMSEventsReachJetStream(t *testing.T) {
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	reg := adapter.NewRegistry()
	reg.Register(dmsDescriptor, func(deviceID string, _ map[string]any) (adapter.Adapter, error) {
		return NewDMS(deviceID, &scriptedDMS{}), nil
	})

	cfgYAML := `
collector_id: dms-e2e-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: dms-1, vendor: ntcip, device_kind: dms, poll_interval: 50ms, connection: {} }
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
	go func() { _ = app.Run(ctx, cfg, reg, ns.ClientURL(), "test") }()

	want := map[string]bool{
		"openits-collector.health.collector-started.v1": false,
		"openits.dms.sign-status-report.v1":             false,
		"openits.dms.message-changed.v1":                false,
		"openits.dms.mode-changed.v1":                   false,
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-seen:
			ceType := m.Header.Get("ce-type")
			t.Logf("subject=%s ce-type=%s ce-source=%s ce-id=%s (%d bytes)",
				m.Subject, ceType, m.Header.Get("ce-source"), m.Header.Get("ce-id"), len(m.Data))
			if _, ok := want[ceType]; ok {
				want[ceType] = true
			}
			if allTrue(want) {
				return
			}
		case <-deadline:
			missing := make([]string, 0)
			for k, v := range want {
				if !v {
					missing = append(missing, k)
				}
			}
			t.Fatalf("timed out; still missing: %v", missing)
		}
	}
}

func allTrue(m map[string]bool) bool {
	for _, v := range m {
		if !v {
			return false
		}
	}
	return true
}

// scriptedDMS returns poll-1 baseline then poll-2+ with a new face message
// and local control, so transition differs fire after the first sample.
type scriptedDMS struct {
	mu    sync.Mutex
	polls int
}

func (s *scriptedDMS) Get(ctx context.Context, oids []string) (map[string]int64, error) {
	all, err := s.GetAll(ctx, oids)
	if err != nil {
		return nil, err
	}
	return all.Ints, nil
}

func (s *scriptedDMS) GetAll(_ context.Context, oids []string) (snmp.Values, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Two Gets per Read (status then env). Advance the poll counter on the
	// status Get so env sees the same poll's values.
	isStatus := false
	for _, oid := range oids {
		if oid == oidDMSControlMode {
			isStatus = true
			break
		}
	}
	if isStatus {
		s.polls++
	}
	poll := s.polls
	if poll < 1 {
		poll = 1
	}

	ints := map[string]int64{}
	octets := map[string][]byte{}
	for k, v := range healthyDMSInts {
		ints[k] = v
	}
	for k, v := range healthyDMSOctets {
		octets[k] = v
	}
	if poll >= 2 {
		ints[oidDMSControlMode] = 2 // local
		ints[oidDMSMsgSourceMode] = 2
		octets[oidDMSMsgTableSource] = []byte{3, 0, 4, 0x12, 0x34} // changeable #4
		octets[oidDMSCurrentBufferMULTI] = []byte("[jl3]CRASH AHEAD")
	}

	out := snmp.Values{Ints: map[string]int64{}, Octets: map[string][]byte{}}
	for _, oid := range oids {
		if v, ok := ints[oid]; ok {
			out.Ints[oid] = v
		}
		if v, ok := octets[oid]; ok {
			out.Octets[oid] = v
		}
	}
	return out, nil
}

func (s *scriptedDMS) Close() error { return nil }

var _ snmp.Client = (*scriptedDMS)(nil)
