package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/health"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// flakyASC fails its first read then succeeds — exercising both health
// transitions and the domain path in one e2e run.
type flakyASC struct {
	mu    sync.Mutex
	calls int
}

func (f *flakyASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}
func (f *flakyASC) Close() error { return nil }
func (f *flakyASC) Read(context.Context) (*model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return nil, errors.New("simulated timeout")
	}
	return &model.Snapshot{
		DeviceID: "asc-1", SampledAt: time.Now().UTC(),
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}},
	}, nil
}

func TestEndToEndHealthEventsReachJetStream(t *testing.T) {
	// Embedded NATS.
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	// Registry with the fixture vendor.
	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(deviceID string, conn map[string]any) (adapter.Adapter, error) { return &flakyASC{}, nil })

	// Config file.
	cfgYAML := `
collector_id: metro-cab-1-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: asc-1, vendor: fixture, device_kind: asc, poll_interval: 20ms, connection: {} }
`
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe BEFORE starting the app (core NATS sub sees JetStream traffic).
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	seen := make(chan string, 64)
	sub, err := nc.Subscribe("openits.us-ga.metro.d01.>", func(m *nats.Msg) {
		// Binary mode: the body is the raw payload and the CloudEvents
		// attributes ride as ce-* headers.
		if ceType := m.Header.Get("ce-type"); ceType != "" {
			seen <- m.Subject + "|" + ceType
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = Run(ctx, cfg, reg, ns.ClientURL(), "test") }()

	want := map[string]bool{
		"openits-collector.health.collector-started.v1":     false,
		"openits-collector.health.device-status-changed.v1": false,
	}
	deadline := time.After(8 * time.Second)
	for {
		allSeen := true
		for _, ok := range want {
			allSeen = allSeen && ok
		}
		if allSeen {
			break
		}
		select {
		case s := <-seen:
			parts := strings.SplitN(s, "|", 2)
			if !strings.HasPrefix(parts[0], "openits.us-ga.metro.d01.") {
				t.Fatalf("event on unexpected subject %q", parts[0])
			}
			if _, tracked := want[parts[1]]; tracked {
				want[parts[1]] = true
			}
		case <-deadline:
			t.Fatalf("timed out; seen so far: %+v", want)
		}
	}
}

// An unparseable subject template must stop the collector at boot, before any
// device is polled or any stream provisioned. This covers only the parse-time
// guard in subject.New (the template below has an unknown {nope} placeholder,
// which fails parseTokens before ValidateCETypes is ever reached).
//
// It does NOT exercise the ordering guarantee that ValidateCETypes runs
// before publish.Connect: no test does, because Run hardcodes both its
// emitter chain and its template with no injection point, so a template that
// fails only inside ValidateCETypes (and not in New) can't be constructed
// from outside this package. ValidateCETypes' own failure paths are covered
// directly in internal/subject/subject_test.go.
func TestRunRejectsUnparseableSubjectTemplate(t *testing.T) {
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
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(deviceID string, conn map[string]any) (adapter.Adapter, error) { return &flakyASC{}, nil })

	cfg := &config.Config{
		Agency: "metro", Site: "cab-1", ModelVersion: "openits/v1",
		Subject: config.Subject{Template: "{prefix}.{agency}.{nope}.{service}.{event}.{version}"},
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 20 * time.Millisecond}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, cfg, reg, ns.ClientURL(), "test"); err == nil {
		t.Fatal("Run must refuse to start on a template it cannot render")
	}
}

// TestUnclaimedDomainEventIsDroppedLoudly covers spec §7: an event no
// emitter claims must be dropped loudly (metric + log), never silently.
//
// This matters right now, not hypothetically: only the collector-owned
// health emitter is wired today (see Run's emitter chain comment); every
// domain event — operational-status-report, fault-raised, detector-report,
// and the rest — takes the drop path until the openits-models emitter
// lands. TestEndToEndHealthEventsReachJetStream can't exercise this: it
// waits only for the two health events, both of which arrive from boot and
// the first (deliberately failed) poll, so the test cancels before any
// successful poll produces a domain event and the drop path never runs
// there. If the drop ever went silent, the collector would look healthy —
// still emitting its health events — while quietly discarding all of its
// real data.
func TestUnclaimedDomainEventIsDroppedLoudly(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	// Only the health emitter is wired, so a domain event finds no claimant.
	emitters := []wire.Emitter{health.NewHealthEmitter()}
	ev := model.OperationalStatusReport{
		Base: model.Base{DeviceID: "asc-1", OccurredAt: time.Now().UTC()},
		Mode: model.ModeNormal, ActivePlanID: 3,
	}
	// pub is nil: safe, because the drop path returns before touching the
	// publisher.
	encodeAndPublish(context.Background(), nil, cloudevents.Tenant{Agency: "metro", Site: "cab-1"}, emitters, ev)

	got := buf.String()
	if !strings.Contains(got, "event dropped") {
		t.Fatalf("an unclaimed domain event must be dropped LOUDLY; log was: %q", got)
	}
	// The log must identify what was lost, or it is not actionable.
	if !strings.Contains(got, "operational-status-report") || !strings.Contains(got, "asc-1") {
		t.Errorf("drop log must name the event kind and device; got: %q", got)
	}
}
