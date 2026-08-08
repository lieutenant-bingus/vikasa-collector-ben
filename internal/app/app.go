// Package app wires config → adapters → runners → synth → emitters →
// publisher: the collector spine.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/internal/runner"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/health"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/openits"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// drainTimeout bounds how long shutdown will wait for in-flight publishes to
// land before abandoning them. Local JetStream is same-box, so this only ever
// elapses when the broker is genuinely unreachable.
const drainTimeout = 5 * time.Second

// Run starts the collector and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config, reg *adapter.Registry, natsURL, version string) error {
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
	emitters := []wire.Emitter{openits.New(cfg.CollectorID), health.NewHealthEmitter()}

	tmpl, err := subject.New(cfg.SubjectConfig(), cfg.Agency, cfg.Site)
	if err != nil {
		return err
	}
	// Exhaustive, not sampled: every ce-type any emitter can produce must
	// render to a legal subject inside the binding. A grammar mistake fails
	// here, at boot, rather than the first time a rare event fires.
	var ceTypes []string
	for _, em := range emitters {
		ceTypes = append(ceTypes, em.CETypes()...)
	}
	if err := tmpl.ValidateCETypes(ceTypes); err != nil {
		return err
	}

	pub, err := publish.Connect(ctx, natsURL, tmpl, cfg.StreamName())
	if err != nil {
		return err
	}
	defer pub.Close()

	// Publishing deliberately does NOT ride ctx: a poll that succeeded just as
	// shutdown arrived has already taken a real reading off a real device, and
	// cancelling its publish would throw that reading away. pubCtx outlives ctx
	// by a bounded drain (below) so those land.
	pubCtx, cancelPub := context.WithCancel(context.Background())
	defer cancelPub()

	sink := func(events []model.Event) {
		for _, ev := range events {
			encodeAndPublish(pubCtx, pub, tenant, emitters, ev)
		}
	}

	// Boot event.
	sink([]model.Event{model.CollectorStarted{
		Base: model.Base{OccurredAt: time.Now().UTC()}, Version: version,
	}})

	// Build adapters, then runners.
	engine := synth.NewEngine(
		synth.NewSignalDiffer(),
		synth.NewFaultDiffer(),
		synth.NewDetectorDiffer(),
		synth.NewDMSDiffer(),
	)
	var wg sync.WaitGroup
	var adapters []adapter.Adapter
	for _, d := range cfg.Devices {
		a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
		if err != nil {
			return fmt.Errorf("device %s: %w", d.ID, err)
		}
		adapters = append(adapters, a)
		sr, ok := a.(adapter.StateReader)
		if !ok {
			return fmt.Errorf("device %s: adapter %s lacks CapState", d.ID, a.Descriptor().Key())
		}
		r := runner.New(sr, d.ID, d.PollInterval, 0, engine, sink)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(ctx)
		}()
	}

	<-ctx.Done()

	// Runners call sink synchronously, so wg.Wait returning means every event
	// they produced has been through publish. The timer bounds that wait: a
	// dead broker must not hang shutdown forever, so the drain window is the
	// most we will spend trying to save in-flight readings.
	drain := time.AfterFunc(drainTimeout, cancelPub)
	wg.Wait()
	drain.Stop()
	cancelPub()

	for _, a := range adapters {
		if err := a.Close(); err != nil {
			slog.Warn("adapter close", "adapter", a.Descriptor().Key(), "err", err)
		}
	}
	return nil
}

func encodeAndPublish(ctx context.Context, pub *publish.Publisher, tenant cloudevents.Tenant,
	emitters []wire.Emitter, ev model.Event) {
	for _, em := range emitters {
		enc, ok, err := em.Encode(ev)
		if err != nil {
			slog.Error("emit failed", "event", ev.EventKind(), "err", err)
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
			slog.Warn("event dropped: no ce-source entity kind for device kind",
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
		if err := pub.Publish(ctx, env, enc.CEType); err != nil {
			slog.Error("publish failed", "type", enc.CEType, "err", err)
		}
		return
	}
	// Loud drop: no emitter claims this event (spec §7). Reaching here now
	// means a real mapping gap — an event kind with no ce-type, or one the
	// openits emitter declined because it could not encode it faithfully —
	// rather than the chain simply being unwired.
	slog.Warn("event dropped: no emitter for domain event",
		"event", ev.EventKind(), "device", ev.EventDeviceID())
}
