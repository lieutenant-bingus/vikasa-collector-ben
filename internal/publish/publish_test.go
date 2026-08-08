package publish

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

func startNATS(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats-server not ready")
	}
	return ns
}

func TestPublishRoundTripAndDedup(t *testing.T) {
	ns := startNATS(t)
	ctx := context.Background()

	tmpl, err := subject.New(subject.Config{}, subject.Identity{Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Connect(ctx, ns.ClientURL(), tmpl, "OPENITS-METRO-CAB-1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	at := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	ceType := "openits-collector.health.collector-started.v1"
	env := cloudevents.New(cloudevents.Event{
		CEType:      ceType,
		Source:      "urn:openits:collector:us-ga:metro:d01:cab-1",
		ContentType: "application/json",
		OccurredAt:  at,
		Data:        []byte(`{"version":"dev"}`),
	})

	// Publish the identical envelope twice: dedup must keep exactly one.
	if err := p.Publish(ctx, env, ceType, "cab-1"); err != nil {
		t.Fatalf("Publish 1: %v", err)
	}
	if err := p.Publish(ctx, env, ceType, "cab-1"); err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	stream, err := js.Stream(ctx, "OPENITS-METRO-CAB-1")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stream msgs = %d, want 1 (dedup)", info.State.Msgs)
	}

	// Read the message back and verify subject + envelope round-trip.
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "t"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(2 * time.Second))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	wantSubject := "openits.us-ga.metro.d01.health.cab-1.collector-started"
	if msg.Subject() != wantSubject {
		t.Fatalf("subject = %q, want %q", msg.Subject(), wantSubject)
	}
	// Binary mode: attributes are ce-* headers, the body is the raw payload
	// verbatim — not a JSON envelope wrapping it.
	h := msg.Headers()
	if h.Get("ce-id") != env.ID || h.Get("ce-type") != ceType {
		t.Fatalf("ce headers = %+v, want ce-id=%q ce-type=%q", h, env.ID, ceType)
	}
	if h.Get("ce-source") != env.Source || h.Get("ce-specversion") != "1.0" {
		t.Fatalf("ce headers = %+v", h)
	}
	if string(msg.Data()) != string(env.Data) {
		t.Fatalf("body = %q, want the raw payload %q", msg.Data(), env.Data)
	}
	// Health has no registry entry, so ce-dataschema must be ABSENT rather
	// than present-and-empty.
	if _, ok := h["Ce-Dataschema"]; ok {
		t.Errorf("ce-dataschema present on a collector-owned schema: %+v", h)
	}
}

func TestPublishUsesCustomTemplate(t *testing.T) {
	ns := startNATS(t)
	ctx := context.Background()

	tmpl, err := subject.New(subject.Config{
		Template: "{prefix}.{geo}.{agency}.{service}.{event}.{version}",
		Vars:     map[string]string{"prefix": "traffic", "geo": "southeast"},
	}, subject.Identity{Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Connect(ctx, ns.ClientURL(), tmpl, "EDGE-METRO-CAB1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	at := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	ceType := "openits-collector.health.collector-started.v1"
	env := cloudevents.New(cloudevents.Event{
		CEType:      ceType,
		Source:      "urn:openits:collector:us-ga:metro:d01:cab-1",
		ContentType: "application/json",
		OccurredAt:  at,
		Data:        []byte(`{"version":"dev"}`),
	})
	if err := p.Publish(ctx, env, ceType, "cab-1"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	stream, err := js.Stream(ctx, "EDGE-METRO-CAB1")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "t"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(2 * time.Second))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := "traffic.southeast.metro.health.collector-started.v1"
	if msg.Subject() != want {
		t.Fatalf("subject = %q, want %q", msg.Subject(), want)
	}
}
