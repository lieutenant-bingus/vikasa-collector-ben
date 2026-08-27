// Command dev runs the whole collector pipeline on one machine with no
// hardware and no broker to install: an embedded NATS server with JetStream,
// a synthetic device, and the real internal/app.Run. It prints every
// CloudEvent that reaches the bus.
//
// It exists because `go run ./cmd/collector -config collector.yaml` cannot
// work on a fresh clone — that command needs a NATS server listening on
// localhost and an SNMP device at the address in collector.yaml, and a
// newcomer has neither. Everything below the adapter is the production path:
// the same synth engine, the same wire emitters, the same publisher, the same
// deterministic ce-ids.
//
// # This is NOT a device simulator, and its output is NOT a fixture source
//
// The synthetic adapter here returns values chosen to exercise the DIFFER —
// a mode that changes, a fault that raises and clears — not values that
// resemble what any real controller reports. It never speaks SNMP at all.
//
// That distinction is the whole point and it is worth being blunt about,
// because the tempting shortcut is real: you cannot write a vendor adapter
// without the device. A fixture recorded from a simulator encodes what the
// simulator's author believed the device does, which is the same defect as a
// hand-typed fixture wearing a costume — and `internal/vendors/ntcip`'s
// alarm bitmap, carried from a previous implementation and still unvalidated
// against physical hardware, is what that costs. ADR 0008 requires recorded
// raw transport responses from the real thing. Nothing produced by this
// harness clears that bar or is meant to.
//
// What it IS good for: seeing the pipeline run end to end, watching subjects
// render under a template you are editing, and exercising changes to
// internal/synth, internal/wire, internal/subject or internal/publish without
// a cabinet.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// devConfig is written to a temp file and loaded through the real
// config.Load, so the harness exercises boot validation rather than
// constructing a *config.Config the validator never saw.
const devConfig = `
collector_id: dev-collector
region: us-ga
agency: metro
agency_unit: d01
site: dev-cabinet
model_version: openits/v1
devices:
  - id: dev-asc-1
    vendor: dev
    device_kind: asc
    poll_interval: 2s
    connection: {}
  - id: dev-dms-1
    vendor: dev
    device_kind: dms
    poll_interval: 2s
    connection: {}
`

// syntheticASC produces a state that keeps changing, so the differ has
// transitions to find and the run prints more than one status report.
//
// Its values are picked to exercise the pipeline, NOT to resemble a real
// controller — see this command's package comment.
type syntheticASC struct {
	mu    sync.Mutex
	polls int
}

func (s *syntheticASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "dev", DeviceKind: "asc", Caps: adapter.CapState}
}

func (s *syntheticASC) Close() error { return nil }

func (s *syntheticASC) Read(context.Context) (*model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls++

	snap := &model.Snapshot{DeviceID: "dev-asc-1", SampledAt: time.Now().UTC()}

	// Alternate mode every third poll so mode-changed fires periodically
	// rather than only once.
	mode := model.ModeNormal
	if (s.polls/3)%2 == 1 {
		mode = model.ModeFlash
	}
	snap.Facets = append(snap.Facets, model.SignalStatus{Mode: mode, ActivePlanID: 3})

	// Raise a fault for a stretch of polls, then clear it, so both halves of
	// the fault family appear.
	faults := model.FaultSet{}
	if (s.polls/4)%2 == 1 {
		faults.Faults = append(faults.Faults, model.Fault{
			ID:          "dev-cabinet-door",
			Severity:    model.SeverityMinor,
			Description: "synthetic fault from cmd/dev",
		})
	}
	snap.Facets = append(snap.Facets, faults)

	return snap, nil
}

// syntheticDMS produces face/control/env transitions so the DMS differs
// and the v0.4 wire surface (message-changed, mode-changed, sign-status-report)
// show up on the bus. Values exercise the pipeline; they are not a fixture.
type syntheticDMS struct {
	mu    sync.Mutex
	polls int
}

func (s *syntheticDMS) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "dev", DeviceKind: "dms", Caps: adapter.CapState}
}

func (s *syntheticDMS) Close() error { return nil }

func (s *syntheticDMS) Read(context.Context) (*model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls++

	snap := &model.Snapshot{DeviceID: "dev-dms-1", SampledAt: time.Now().UTC()}

	st := model.DMSStatus{
		ControlMode:       model.ControlCentral,
		DisplayState:      model.DisplayNormal,
		ActiveMemoryType:  model.MemoryChangeable,
		ActiveSlot:        1,
		MessageStatus:     model.StatusValid,
		MessageText:       "[jl3]ROAD WORK",
		MessageCRC:        0x1111,
		ActivationTrigger: model.TriggerCommand,
	}
	if (s.polls/3)%2 == 1 {
		st.ControlMode = model.ControlLocal
		st.ActiveSlot = 2
		st.MessageText = "[jl3]CRASH AHEAD"
		st.MessageCRC = 0x2222
		st.ActivationTrigger = model.TriggerSchedule
	}
	snap.Facets = append(snap.Facets, st)

	env := model.DMSEnvironment{
		BrightnessPercent:    70,
		BrightnessReported:   true,
		AmbientLightPercent:  uint8(30 + (s.polls % 40)),
		AmbientLightReported: true,
		CabinetTempDeciC:     220,
		CabinetTempReported:  true,
		DoorOpen:             false,
		DoorReported:         true,
	}
	snap.Facets = append(snap.Facets, env)
	return snap, nil
}

func main() {
	pollFor := flag.Duration("for", 0, "stop after this long (0 = run until Ctrl-C)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(*pollFor); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stopAfter time.Duration) error {
	storeDir, err := os.MkdirTemp("", "vikasa-dev-jetstream-")
	if err != nil {
		return fmt.Errorf("temp store dir: %w", err)
	}
	defer os.RemoveAll(storeDir)

	// Port -1 asks the server for any free port, so two `make dev` runs do
	// not fight over 4222 and neither collides with a real local broker.
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: storeDir})
	if err != nil {
		return fmt.Errorf("start embedded nats: %w", err)
	}
	ns.Start()
	defer ns.Shutdown()
	if !ns.ReadyForConnections(5 * time.Second) {
		return fmt.Errorf("embedded nats did not become ready")
	}
	fmt.Printf("embedded NATS (JetStream) listening on %s\n", ns.ClientURL())

	reg := adapter.NewRegistry()
	reg.Register(
		adapter.Descriptor{Vendor: "dev", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &syntheticASC{}, nil },
	)
	reg.Register(
		adapter.Descriptor{Vendor: "dev", DeviceKind: "dms", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &syntheticDMS{}, nil },
	)

	cfgDir, err := os.MkdirTemp("", "vikasa-dev-config-")
	if err != nil {
		return fmt.Errorf("temp config dir: %w", err)
	}
	defer os.RemoveAll(cfgDir)
	cfgPath := filepath.Join(cfgDir, "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(devConfig), 0o600); err != nil {
		return fmt.Errorf("write dev config: %w", err)
	}
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		return fmt.Errorf("load dev config: %w", err)
	}

	// Subscribe before app.Run so the boot event is not missed. A core NATS
	// subscription sees JetStream traffic, which is all this needs.
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		return fmt.Errorf("connect to embedded nats: %w", err)
	}
	defer nc.Close()
	if _, err := nc.Subscribe(">", printEvent); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if stopAfter > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, stopAfter)
		defer cancel()
	}

	fmt.Println("running the real app.Run against a synthetic device — Ctrl-C to stop")
	fmt.Println()
	if err := app.Run(ctx, cfg, reg, ns.ClientURL(), "dev"); err != nil {
		return err
	}
	return nil
}

// printEvent renders one CloudEvent the way a consumer would read it: the
// subject it was routed to, and the ce-* attributes that carry identity. The
// payload body is deliberately not decoded — doing so would mean importing
// openits-models outside internal/wire, which is exactly the boundary ADR
// 0002 draws and scripts/lint-boundary.sh enforces.
func printEvent(m *nats.Msg) {
	ceType := m.Header.Get("ce-type")
	if ceType == "" {
		return
	}
	fmt.Printf("%s\n    ce-type=%s\n    ce-source=%s\n    ce-id=%s  (%d bytes)\n\n",
		m.Subject, ceType, m.Header.Get("ce-source"), m.Header.Get("ce-id"), len(m.Data))
}
