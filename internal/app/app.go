// Package app wires config → adapters → runners → synth → emitters →
// publisher: the collector spine.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/durable"
	"github.com/Vikasa2M/vikasa-collector/internal/logship"
	"github.com/Vikasa2M/vikasa-collector/internal/manage"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/internal/runner"
	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/health"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/openits"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// drainTimeout bounds how long shutdown spends publishing what is still
// queued (ADR 0020 clause 5). Local JetStream is same-box, so this only ever
// elapses when the broker is genuinely unreachable; what is left is counted
// and logged, not waited on.
const drainTimeout = 5 * time.Second

// stateFlushTimeout bounds how long shutdown spends writing synth state
// still pending. It runs before the publisher drains, because the drain
// closes the connection both share.
const stateFlushTimeout = 2 * time.Second

// logShipLimit is how many log records are shipped as instance.log events
// per heartbeat interval (ADR 0019 clause 10); the excess is counted in
// logs_suppressed. Enough for a burst of genuine trouble, not a flood.
const logShipLimit = 20

// closeTimeout bounds how long shutdown waits for adapters to close. Close
// is third-party code; a supervisor waiting on the process to exit must not
// wait on an adapter bug. A var so tests can shorten it.
var closeTimeout = 2 * time.Second

// Options carries what Run needs beyond the validated config and registry.
type Options struct {
	// NATSURL is the cabinet-local broker.
	NATSURL string
	// NATS is the broker credential and CA (ADR 0022).
	NATS publish.Security
	// Version is stamped into the collector's own instance events.
	Version string
	// Logger receives every line the collector writes. Nil means
	// slog.Default(); main always sets it, so its flags decide format and
	// level.
	Logger *slog.Logger
	// StatusAddr is the loopback address for the management surface
	// (/livez, /readyz, /status); empty disables it.
	StatusAddr string
	// Counters receives the collector's own counts (ADR 0020 clause 4).
	// Nil means Run keeps private ones; the caller that serves /status or
	// the heartbeat passes its own.
	Counters *stats.Counters
}

// Run starts the collector and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config, reg *adapter.Registry, opts Options) error {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	counters := opts.Counters
	if counters == nil {
		counters = new(stats.Counters)
	}
	heartbeatInterval := cfg.HeartbeatInterval
	if heartbeatInterval == 0 {
		heartbeatInterval = config.DefaultHeartbeatInterval
	}
	// Ship the collector's own warn-and-above log records as events (ADR
	// 0019 clause 10). Every record still reaches the local log; the
	// shipper goroutine below drains these into the pipeline.
	floor, err := cfg.LogShipFloor()
	if err != nil {
		return err
	}
	shipping := logship.NewHandler(log.Handler(), floor, logShipLimit)
	shipping.Configure(heartbeatInterval, counters)
	log = slog.New(shipping)
	if err := manage.ValidateAddr(opts.StatusAddr); err != nil {
		return err
	}
	devices := stats.NewDevices(cfg.DeviceIDs())
	bootID, err := newBootID()
	if err != nil {
		return err
	}
	started := time.Now().UTC()
	tenant := cfg.Tenant()

	// Emitter chain: first claim wins. openits goes first so catalog events
	// are claimed there; health keeps the collector-owned schema (ADR 0007)
	// and is never claimed by openits.
	//
	// Events still reach the loud-drop path below — deliberately. The openits
	// emitter declines anything it cannot encode faithfully (a controller mode
	// with no upstream identity, a shared event on an unserved device kind),
	// so the drop is the visible outcome of a mapping gap rather than evidence
	// the chain is unwired.
	catalog, err := catalogEmitter(cfg.ModelVersion, cfg.CollectorID)
	if err != nil {
		return err
	}
	emitters := []wire.Emitter{catalog, health.NewHealthEmitter()}

	tmpl, err := subject.New(cfg.SubjectConfig(), cfg.SubjectIdentity())
	if err != nil {
		return err
	}
	// Exhaustive, not sampled: every ce-type any emitter can produce must
	// render to a legal subject inside its namespace's binding. A grammar
	// mistake fails here, at boot, rather than the first time a rare event
	// fires. The same list drives stream provisioning below, so the streams
	// and the validated ce-types cannot drift: a namespace with no stream
	// would publish into a subject space nothing captures, and the events
	// would vanish with no error at any layer.
	var ceTypes []string
	for _, em := range emitters {
		ceTypes = append(ceTypes, em.CETypes()...)
	}
	if err := tmpl.ValidateCETypes(ceTypes, cfg.DeviceIDs()); err != nil {
		return err
	}

	// Every adapter is built before anything is announced or started, so a
	// device that cannot be built fails boot cleanly: no runner is polling,
	// no started event has gone out, and the adapters already built are
	// closed. Building inside the runner-start loop left devices ahead of
	// the broken one polling against a publisher Run was about to close.
	readers, err := buildReaders(log, cfg.Devices, reg)
	if err != nil {
		return err
	}
	defer closeAll(log, readers)

	// Start never waits for the broker (ADR 0020): it connects in the
	// background and queues until the broker is there, so the collector boots
	// and polls whether or not NATS is up yet.
	pub, err := publish.Start(ctx, opts.NATSURL, opts.NATS, tmpl, ceTypes, log, counters)
	if err != nil {
		return err
	}

	if opts.StatusAddr != "" {
		surface := &surface{
			version: opts.Version, revision: cfg.Revision, bootID: bootID, started: started,
			counters: counters, devices: devices, pub: pub,
		}
		srv, err := manage.Start(ctx, opts.StatusAddr, surface, log)
		if err != nil {
			return err
		}
		// Last to stop, so /livez answers through the whole drain.
		defer func() {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
			defer cancel()
			srv.Shutdown(sctx)
		}()
	}

	// sink only queues, so a runner never waits on the broker. Publishing
	// deliberately does not ride ctx: a reading taken just as shutdown
	// arrived is still published, within the drain window below.
	sink := func(events []model.Event) {
		for _, ev := range events {
			encodeAndEnqueue(log, pub, counters, tenant, emitters, ev)
		}
	}

	// The collector's identity, stated in every instance event (ADR 0019
	// clause 7), and the restart record (clause 8).
	inst := health.Instance{
		CollectorID: cfg.CollectorID, Version: opts.Version, ConfigRevision: cfg.Revision, BootID: bootID,
	}
	sink([]model.Event{health.Started{Base: model.Base{OccurredAt: started}, Instance: inst}})

	engine := synth.NewEngine(
		synth.NewSignalDiffer(),
		synth.NewFaultDiffer(),
		synth.NewDetectorDiffer(),
		synth.NewDMSDiffer(),
		synth.NewDMSEnvironmentDiffer(),
		synth.NewTrafficIntervalDiffer(),
		synth.NewZoneIncidentDiffer(),
		synth.NewZoneIntervalDiffer(),
		synth.NewCCTVDiffer(),
		synth.NewGateDiffer(),
	)
	engine.WithRejectHook(func(deviceID string, kind model.Kind, got string) {
		counters.FacetsRejected.Add(1)
		// An adapter bug, not a device fault: the facet has the right kind but
		// not the type its differ diffs. Rejected rather than diffed, so the
		// poll loop survives and the previous state stands.
		log.Warn("facet rejected: wrong type for its kind",
			"device", deviceID, "kind", string(kind), "type", got)
	})

	// The engine is held until its previous state loads (ADR 0017, ADR 0020
	// clause 2): runners poll and report device health from the start, but
	// nothing is diffed for a device until the engine knows what it knew
	// before this boot — otherwise every restart re-raises every standing
	// fault and loses whatever changed while it was down.
	engine.HoldUntilSeeded()
	state := durable.Start(ctx, pub.JetStream(), engine, cfg.DeviceIDs(), log)

	var wg sync.WaitGroup
	for i, d := range cfg.Devices {
		r := runner.New(readers[i], runner.Config{
			DeviceID: d.ID, Interval: d.PollInterval, Engine: engine, Out: sink,
			Counters: counters, Devices: devices, Log: log,
		})
		wg.Go(func() { r.Run(ctx) })
	}
	wg.Go(func() { runHeartbeat(ctx, heartbeatInterval, inst, started, counters, sink) })
	// The shipper stops with the runners, before the drain: a record logged
	// while draining (say, an event not queued) must not loop back into a
	// publisher that is closing.
	wg.Go(func() {
		for {
			select {
			case r := <-shipping.Records():
				sink([]model.Event{health.Log{Base: model.Base{OccurredAt: r.Time}, Instance: inst,
					Level: r.Level, Message: r.Message, Attrs: r.Attrs}})
			case <-ctx.Done():
				return
			}
		}
	})

	<-ctx.Done()

	// Runners stop promptly (a hung read is abandoned, and sink never
	// blocks), so once they have, nothing else can be queued. Then give the
	// queue its bounded drain window.
	wg.Wait()
	// Best-effort (ADR 0019 clause 8): queued ahead of the drain, so it goes
	// out whenever the broker can take it within the window.
	stopAt := time.Now().UTC()
	sink([]model.Event{health.Stopping{Base: model.Base{OccurredAt: stopAt}, Instance: inst, Uptime: stopAt.Sub(started)}})
	fctx, cancelFlush := context.WithTimeout(context.WithoutCancel(ctx), stateFlushTimeout)
	defer cancelFlush()
	state.Flush(fctx)
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drainTimeout)
	defer cancel()
	pub.Drain(dctx)

	return nil
}

// catalogEmitter selects the catalog emitter for model_version (ADR 0005).
// config has already rejected anything outside wire.SupportedModelVersions;
// the error path is for a version listed there with no case here, which
// TestEverySupportedModelVersionSelectsAnEmitter catches first.
func catalogEmitter(modelVersion, collectorID string) (wire.Emitter, error) {
	switch modelVersion {
	case openits.ModelVersion:
		return openits.New(collectorID), nil
	default:
		return nil, fmt.Errorf("model_version %q has no catalog emitter in this build", modelVersion)
	}
}

// buildReaders builds one StateReader per device, in config order. On any
// failure it closes what it already built, so the caller owns either every
// reader or none.
func buildReaders(log *slog.Logger, devices []config.Device, reg *adapter.Registry) ([]adapter.StateReader, error) {
	readers := make([]adapter.StateReader, 0, len(devices))
	for _, d := range devices {
		a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
		if err != nil {
			closeAll(log, readers)
			return nil, fmt.Errorf("device %s: %w", d.ID, err)
		}
		sr, ok := a.(adapter.StateReader)
		if !ok {
			_ = a.Close()
			closeAll(log, readers)
			return nil, fmt.Errorf("device %s: adapter %s lacks CapState", d.ID, a.Descriptor().Key())
		}
		readers = append(readers, sr)
	}
	return readers, nil
}

// closeAll closes every reader concurrently and waits at most
// closeTimeout. A Close still running then is abandoned and logged; the
// process is exiting, and the OS reclaims what it held.
func closeAll(log *slog.Logger, readers []adapter.StateReader) {
	var wg sync.WaitGroup
	for _, r := range readers {
		wg.Go(func() {
			if err := r.Close(); err != nil {
				log.Warn("adapter close", "adapter", r.Descriptor().Key(), "err", err)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(closeTimeout):
		log.Warn("adapter close timed out; exiting without it", "timeout", closeTimeout)
	}
}

func encodeAndEnqueue(log *slog.Logger, pub *publish.Publisher, counters *stats.Counters, tenant cloudevents.Tenant,
	emitters []wire.Emitter, ev model.Event) {
	for _, em := range emitters {
		enc, ok, err := em.Encode(ev)
		if err != nil {
			counters.EncodeErrors.Add(1)
			log.Error("emit failed", "event", ev.EventKind(), "device", ev.EventDeviceID(), "err", err)
			return
		}
		if !ok {
			continue
		}
		source := cloudevents.SourceFor(tenant, ev.EventDeviceKind(), ev.EventDeviceID())
		if source == "" {
			// No entity-kind mapping for this device kind. Publishing would
			// mean inventing a URN shape upstream does not define, and ce-id
			// hashes that string, so the invention would be baked into every
			// id. Drop it as loudly as an unclaimed event.
			counters.DroppedNoSource.Add(1)
			log.Warn("event dropped: no ce-source entity kind for device kind",
				"event", ev.EventKind(), "device_kind", ev.EventDeviceKind(), "device", ev.EventDeviceID())
			return
		}
		env := cloudevents.New(cloudevents.Event{
			CEType:      enc.CEType,
			Source:      source,
			ContentType: enc.ContentType,
			DataSchema:  enc.DataSchema,
			OccurredAt:  ev.EventOccurredAt(),
			Data:        enc.Data,
			Identity:    enc.Identity,
		})
		if err := pub.Enqueue(env, ev.EventDeviceID()); err != nil {
			log.Error("event not queued", "type", enc.CEType, "device", ev.EventDeviceID(), "err", err)
		}
		return
	}
	// Loud drop: no emitter claims this event. README.md's "Events can still
	// be dropped with a warning, and that is the designed behaviour" is the
	// living statement of this rule. Reaching here now
	// means a real mapping gap — an event kind with no ce-type, or one the
	// openits emitter declined because it could not encode it faithfully —
	// rather than the chain simply being unwired.
	counters.DroppedNoEmitter.Add(1)
	log.Warn("event dropped: no emitter for domain event",
		"event", ev.EventKind(), "device", ev.EventDeviceID())
}

// surface is the management surface's view of a running collector.
type surface struct {
	version, revision, bootID string
	started                   time.Time
	counters                  *stats.Counters
	devices                   *stats.Devices
	pub                       *publish.Publisher
}

// Ready is the spec's rule: booted (we are serving), config accepted (Run
// got this far), and at least one successful device poll — latched, and
// independent of the broker.
func (s *surface) Ready() bool { return s.devices.EverPolled() }

func (s *surface) Status() manage.Status {
	return manage.Status{
		Version: s.version, ConfigRevision: s.revision, BootID: s.bootID,
		StartedAt: s.started, UptimeSeconds: int64(time.Since(s.started).Seconds()),
		Ready:    s.Ready(),
		Broker:   manage.Broker{Connected: s.pub.Connected(), Queued: s.pub.Stats().Queued},
		Counters: s.counters.Snapshot(),
		Devices:  s.devices.Snapshot(),
	}
}

// newBootID identifies this process lifetime (ADR 0019): a consumer seeing
// it change knows the cumulative counters restarted from zero.
func newBootID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("boot id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
