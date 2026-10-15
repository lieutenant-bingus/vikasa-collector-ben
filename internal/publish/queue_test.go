package publish

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

const healthType = "openits-collector.instance.started.v1"

// freePort reserves a TCP port for a broker the test starts later.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func startNATSOn(t *testing.T, port int, opts ...func(*server.Options)) *server.Server {
	t.Helper()
	o := &server.Options{Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: t.TempDir()}
	for _, f := range opts {
		f(o)
	}
	ns, err := server.NewServer(o)
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

func testTemplate(t *testing.T) *subject.Template {
	t.Helper()
	tmpl, err := subject.New(subject.Config{}, subject.Identity{Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1"})
	if err != nil {
		t.Fatal(err)
	}
	return tmpl
}

func envN(n int, data []byte) cloudevents.Envelope {
	if data == nil {
		data = fmt.Appendf(nil, `{"n":%d}`, n)
	}
	return cloudevents.New(cloudevents.Event{
		CEType: healthType, Source: "urn:openits:collector:us-ga:metro:d01:cab-1",
		ContentType: "application/json", OccurredAt: time.Date(2026, 7, 12, 10, 0, n, 0, time.UTC),
		Data: data,
	})
}

// fast is the test tuning: tiny backoffs so retry paths run in milliseconds.
func fast(capacity int) tuning {
	return tuning{capacity: capacity, backoffMin: 10 * time.Millisecond, backoffMax: 50 * time.Millisecond,
		publishTimeout: 500 * time.Millisecond, reconnectWait: 50 * time.Millisecond}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// drainBounded is test cleanup that can never hang the suite.
func drainBounded(p *Publisher) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Drain(ctx)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func streamMsgs(t *testing.T, url string) []string {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	st, err := js.Stream(t.Context(), StreamNameForBinding("openits-collector.us-ga.metro.d01.>"))
	if err != nil {
		return nil
	}
	info, err := st.Info(t.Context())
	if err != nil || info.State.Msgs == 0 {
		return nil
	}
	cons, err := st.OrderedConsumer(t.Context(), jetstream.OrderedConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for range info.State.Msgs {
		m, err := cons.Next(jetstream.FetchMaxWait(2 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(m.Data()))
	}
	return out
}

// ADR 0020 clause 1: boot does not depend on the broker. Events queued
// while it is absent are published once it appears.
func TestEventsQueuedBeforeTheBrokerExistsArePublishedWhenItStarts(t *testing.T) {
	port := freePort(t)
	p, err := start(t.Context(), fmt.Sprintf("nats://127.0.0.1:%d", port), testTemplate(t), []string{healthType}, quiet(), fast(100))
	if err != nil {
		t.Fatalf("start with no broker = %v; boot must not depend on the broker", err)
	}
	t.Cleanup(func() { drainBounded(p) })

	for i := range 3 {
		if err := p.Enqueue(envN(i, nil), "cab-1"); err != nil {
			t.Fatal(err)
		}
	}
	ns := startNATSOn(t, port)
	waitFor(t, "3 publishes after the broker started", func() bool { return p.Stats().Published == 3 })
	if got := streamMsgs(t, ns.ClientURL()); strings.Join(got, ",") != `{"n":0},{"n":1},{"n":2}` {
		t.Fatalf("stream = %v, want the three events in order", got)
	}
}

// ADR 0020 clause 3: a full queue drops its OLDEST event — the newest
// describe the device as it is now — and counts each drop.
func TestFullQueueDropsOldestAndCounts(t *testing.T) {
	port := freePort(t)
	p, err := start(t.Context(), fmt.Sprintf("nats://127.0.0.1:%d", port), testTemplate(t), []string{healthType}, quiet(), fast(3))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainBounded(p) })

	for i := range 6 {
		if err := p.Enqueue(envN(i, nil), "cab-1"); err != nil {
			t.Fatal(err)
		}
	}
	// One event may already be in the drainer's hands (in flight, outside the
	// queue); the queue itself holds 3, so 2 or 3 were dropped.
	ns := startNATSOn(t, port)
	waitFor(t, "the queue to empty", func() bool { s := p.Stats(); return s.Queued == 0 && s.Published > 0 })
	got := streamMsgs(t, ns.ClientURL())
	s := p.Stats()
	if int(s.QueueDropped)+len(got) != 6 {
		t.Fatalf("dropped %d + published %d != 6 enqueued", s.QueueDropped, len(got))
	}
	if got[len(got)-1] != `{"n":5}` || s.QueueDropped < 2 {
		t.Fatalf("published %v with %d dropped; want the newest kept and the oldest dropped", got, s.QueueDropped)
	}
}

// A message the broker will never accept is dropped and counted at once,
// not retried forever in front of everything queued behind it.
func TestPermanentFailureIsDroppedAndDoesNotBlockTheQueue(t *testing.T) {
	port := freePort(t)
	ns := startNATSOn(t, port, func(o *server.Options) { o.MaxPayload = 1024 })
	p, err := start(t.Context(), ns.ClientURL(), testTemplate(t), []string{healthType}, quiet(), fast(100))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainBounded(p) })

	if err := p.Enqueue(envN(0, make([]byte, 4096)), "cab-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Enqueue(envN(1, nil), "cab-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the small event behind the oversized one", func() bool { return p.Stats().Published == 1 })
	if s := p.Stats(); s.PermanentDropped != 1 {
		t.Fatalf("PermanentDropped = %d, want 1", s.PermanentDropped)
	}
}

// A broker restarted with a wiped store has no streams; the publisher
// re-provisions instead of failing every publish from then on.
func TestReprovisionsWhenTheStreamDisappears(t *testing.T) {
	port := freePort(t)
	ns := startNATSOn(t, port)
	p, err := start(t.Context(), ns.ClientURL(), testTemplate(t), []string{healthType}, quiet(), fast(100))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainBounded(p) })
	if err := p.Enqueue(envN(0, nil), "cab-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first publish", func() bool { return p.Stats().Published == 1 })

	nc, _ := nats.Connect(ns.ClientURL())
	js, _ := jetstream.New(nc)
	if err := js.DeleteStream(t.Context(), StreamNameForBinding("openits-collector.us-ga.metro.d01.>")); err != nil {
		t.Fatal(err)
	}
	nc.Close()

	if err := p.Enqueue(envN(1, nil), "cab-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "publish after the stream was deleted", func() bool { return p.Stats().Published == 2 })
}

// ADR 0020 clause 5: shutdown drains within its window, and whatever is
// left — the broker never came — is counted, not waited on.
func TestDrainGivesUpAtItsDeadlineAndCountsTheRest(t *testing.T) {
	p, err := start(t.Context(), fmt.Sprintf("nats://127.0.0.1:%d", freePort(t)), testTemplate(t), []string{healthType}, quiet(), fast(100))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		if err := p.Enqueue(envN(i, nil), "cab-1"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	begin := time.Now()
	p.Drain(ctx)
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("Drain took %v with a 200ms deadline", took)
	}
	if s := p.Stats(); s.ShutdownDropped != 4 || s.Published != 0 {
		t.Fatalf("stats = %+v, want 4 shutdown drops and nothing published", s)
	}
	if err := p.Enqueue(envN(9, nil), "cab-1"); err == nil {
		t.Fatal("Enqueue after Drain succeeded; a drained publisher must refuse new events")
	}
}

// A NATS URL can carry credentials (user:pass@ or a bare token@). Start
// refuses it before connecting (ADR 0022), and neither the refusal nor
// anything logged may contain the secret.
func TestCredentialsInTheURLAreRefusedUnlogged(t *testing.T) {
	port := freePort(t)
	startNATSOn(t, port)
	var buf lockedBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	url := fmt.Sprintf("nats://alice:hunter2@127.0.0.1:%d,nats://s3cr3t-token@127.0.0.1:%d", port, port)
	p, err := start(t.Context(), url, testTemplate(t), []string{healthType}, log, fast(10))
	if err == nil {
		drainBounded(p)
		t.Fatal("start accepted credentials in the URL")
	}
	for _, secret := range []string{"hunter2", "alice", "s3cr3t-token"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(buf.String(), secret) {
			t.Fatalf("%q leaked: err=%v log=%s", secret, err, buf.String())
		}
	}
	if got := redactURL("::not a url::"); strings.Contains(got, "not a url") {
		t.Fatalf("an unparseable URL was echoed back: %q", got)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The URL parser's own error echoes the raw string, credentials included;
// Start's error must not carry them through (main prints it to stderr).
func TestStartErrorNeverContainsCredentials(t *testing.T) {
	// Each password is chosen to defeat a pattern-based scrubber: the URL
	// parser splits userinfo at the LAST '@', ends the authority at the
	// first '/', and escapes '"' when it quotes the input.
	for _, u := range []string{
		"nats://alice:hunter2@bad host:4222",
		"nats://alice:hunter2@127.0.0.1:notaport",
		"nats://s3cr3t-token@[::1",
		"nats://alice:hun@ter2@127.0.0.1:notaport",
		"nats://alice:hun/ter2@127.0.0.1:4222",
		`nats://alice:hun"ter2@127.0.0.1:notaport`,
	} {
		_, err := start(t.Context(), u, testTemplate(t), []string{healthType}, quiet(), fast(10))
		if err == nil {
			t.Fatalf("start(%q) succeeded; want a malformed-URL error", u)
		}
		for _, secret := range []string{"hunter2", "alice", "s3cr3t-token", "ter2", "hun"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("start(%q) error leaks %q: %v", u, secret, err)
			}
		}
	}
}

// The runtime scrubber replaces literal copies of the configured URLs, raw
// or quote-escaped, whatever the password contains.
func TestScrubberRemovesConfiguredURLsVerbatim(t *testing.T) {
	servers := `nats://alice:hun@ter2@10.0.0.1:4222, nats://bob:pa"ss/x@10.0.0.2:4222`
	s := scrubber(servers)
	for _, text := range []string{
		"dial nats://alice:hun@ter2@10.0.0.1:4222: refused",
		`parse "nats://bob:pa\"ss/x@10.0.0.2:4222": bad`,
		"all servers: " + servers,
	} {
		got := s.Replace(text)
		for _, secret := range []string{"alice", "hun@", "ter2", "bob:", `pa"ss`, `pa\"ss`, "ss/x"} {
			if strings.Contains(got, secret) {
				t.Errorf("scrubbed %q still contains %q: %q", text, secret, got)
			}
		}
	}
}

// The publisher counts into the collector's shared Counters when given
// one, so /status and the heartbeat read the same numbers it does.
func TestPublisherCountsIntoSharedCounters(t *testing.T) {
	port := freePort(t)
	startNATSOn(t, port)
	var c stats.Counters
	tune := fast(100)
	tune.counters = &c
	p, err := start(t.Context(), fmt.Sprintf("nats://127.0.0.1:%d", port), testTemplate(t), []string{healthType}, quiet(), tune)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainBounded(p) })
	for i := range 2 {
		if err := p.Enqueue(envN(i, nil), "cab-1"); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "two publishes in the shared counters", func() bool { return c.EventsPublished.Load() == 2 })
	if p.Stats().Published != 2 {
		t.Fatalf("Stats().Published = %d, want 2 (same counters)", p.Stats().Published)
	}
}
