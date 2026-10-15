package durable

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var (
	t0   = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	door = model.Fault{ID: "cabinet-door", Severity: model.SeverityMinor, Category: model.CategoryCabinet, Description: "Cabinet door open"}
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

func startNATS(t *testing.T, port int, store string) *server.Server {
	t.Helper()
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: store})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	return ns
}

func jsFor(t *testing.T, port int) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", port),
		nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1), nats.ReconnectWait(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func fast() tuning {
	return tuning{backoffMin: 10 * time.Millisecond, backoffMax: 50 * time.Millisecond, opTimeout: 500 * time.Millisecond}
}

// boot is one collector lifetime: a held engine plus durable state.
func boot(t *testing.T, js jetstream.JetStream) (*synth.Engine, *State) {
	t.Helper()
	e := synth.NewEngine(synth.NewFaultDiffer(), synth.NewSignalDiffer()).HoldUntilSeeded()
	s := start(t.Context(), js, e, []string{"asc-1"}, slog.New(slog.DiscardHandler), fast())
	return e, s
}

func waitSeeded(t *testing.T, s *State) {
	t.Helper()
	select {
	case <-s.Seeded():
	case <-time.After(10 * time.Second):
		t.Fatal("state was never seeded")
	}
}

func shutdown(t *testing.T, s *State) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Flush(ctx)
}

func faults(at time.Time, fs ...model.Fault) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at, Facets: []model.Facet{model.FaultSet{Faults: fs}}}
}

func eventKinds(evs []model.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.EventKind()
	}
	return out
}

// ADR 0017's whole point: a restart with a fault standing re-raises nothing.
func TestRestartDoesNotReRaiseAStandingFault(t *testing.T) {
	port := freePort(t)
	startNATS(t, port, t.TempDir())
	js := jsFor(t, port)

	e1, s1 := boot(t, js)
	waitSeeded(t, s1)
	if got := eventKinds(e1.Apply(faults(t0, door))); len(got) != 1 || got[0] != "fault-raised" {
		t.Fatalf("first boot = %v, want the fault raised once", got)
	}
	shutdown(t, s1)

	e2, s2 := boot(t, js)
	waitSeeded(t, s2)
	t.Cleanup(func() { shutdown(t, s2) })
	if got := eventKinds(e2.Apply(faults(t0.Add(time.Minute), door))); len(got) != 0 {
		t.Fatalf("after restart = %v; the standing fault must not be re-raised", got)
	}
}

// A transition that happened while the collector was down is emitted once,
// on the first diff after the restart.
func TestTransitionAcrossARestartIsEmittedOnce(t *testing.T) {
	port := freePort(t)
	startNATS(t, port, t.TempDir())
	js := jsFor(t, port)

	e1, s1 := boot(t, js)
	waitSeeded(t, s1)
	e1.Apply(faults(t0, door))
	shutdown(t, s1)

	e2, s2 := boot(t, js)
	waitSeeded(t, s2)
	t.Cleanup(func() { shutdown(t, s2) })
	if got := eventKinds(e2.Apply(faults(t0.Add(time.Minute)))); len(got) != 1 || got[0] != "fault-cleared" {
		t.Fatalf("after restart = %v, want the clear that happened while down, once", got)
	}
	if got := eventKinds(e2.Apply(faults(t0.Add(2 * time.Minute)))); len(got) != 0 {
		t.Fatalf("next poll = %v, want nothing", got)
	}
}

// ADR 0020 clause 2: with no broker, the engine is held — it diffs nothing,
// not even a first-poll raise — and is released once state can be loaded.
func TestHeldWhileTheBrokerIsAbsent(t *testing.T) {
	port := freePort(t)
	js := jsFor(t, port)
	e, s := boot(t, js)

	if got := eventKinds(e.Apply(faults(t0, door))); len(got) != 0 {
		t.Fatalf("with no broker = %v, want nothing (held)", got)
	}
	startNATS(t, port, t.TempDir())
	t.Cleanup(func() { shutdown(t, s) }) // registered after the broker, so it runs first
	waitSeeded(t, s)
	if got := eventKinds(e.Apply(faults(t0.Add(time.Second), door))); len(got) != 1 {
		t.Fatalf("after seeding an empty bucket = %v, want the first-boot raise", got)
	}
}

// ADR 0017: writes only on transitions. An unchanged facet polled again is
// not rewritten, which is what keeps flash wear proportional to change.
func TestUnchangedStateIsNotRewritten(t *testing.T) {
	port := freePort(t)
	startNATS(t, port, t.TempDir())
	js := jsFor(t, port)
	e, s := boot(t, js)
	waitSeeded(t, s)
	for i := range 5 {
		e.Apply(faults(t0.Add(time.Duration(i)*time.Second), door))
	}
	shutdown(t, s)

	kv, err := js.KeyValue(t.Context(), Bucket)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := kv.Get(t.Context(), key("asc-1", model.KindFaultSet))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Revision() != 1 {
		t.Fatalf("revision = %d after five identical polls, want 1 write", entry.Revision())
	}
}
