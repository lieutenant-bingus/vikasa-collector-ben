// Package publish writes CloudEvents to the cabinet-local JetStream.
//
// Broker absence is latency, not loss (ADR 0020): Start never waits for the
// broker, events wait in a bounded in-memory queue while it is away, and one
// drainer goroutine publishes them in order once it is back. The only events
// lost are ones the queue could not hold, ones the broker will never accept,
// and ones still queued when shutdown's drain window closes — and each of
// those is counted.
package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/stats"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

const dedupWindow = 2 * time.Minute

// tuning holds the publisher's timing and size constants. Production uses
// defaults; tests shrink them so retry paths run in milliseconds.
type tuning struct {
	// capacity is the queue bound. Sized for the deployment (about ten
	// devices per cabinet, transitions rare by construction): minutes of a
	// broker outage at a realistic event rate, a few MB at worst.
	capacity       int
	backoffMin     time.Duration
	backoffMax     time.Duration
	publishTimeout time.Duration // one JetStream publish, ack included
	reconnectWait  time.Duration

	// counters receives every count; nil means private counters. Carried
	// here so it is set before the drainer starts.
	counters *stats.Counters
	// security is the broker credential and CA (ADR 0022).
	security Security
}

var defaults = tuning{
	capacity:       10_000,
	backoffMin:     100 * time.Millisecond,
	backoffMax:     5 * time.Second,
	publishTimeout: 2 * time.Second,
	reconnectWait:  time.Second,
}

// Stats is the publisher's view of its counters (cumulative since Start, or
// since boot when they are the collector's shared Counters), plus Queued,
// the current queue depth.
type Stats struct {
	Published        uint64
	QueueDropped     uint64 // oldest event evicted because the queue was full
	PermanentDropped uint64 // the broker will never accept it (e.g. too large)
	ShutdownDropped  uint64 // still queued when the drain window closed
	Queued           int
}

// Publisher owns the NATS connection, the streams it provisions, and the
// queue between the runners and the broker.
type Publisher struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	tmpl     *subject.Template
	bindings []string
	log      *slog.Logger
	tune     tuning
	scrub    *strings.Replacer // redacts the configured URLs out of error text

	mu     sync.Mutex
	queue  []*nats.Msg
	closed bool
	wake   chan struct{} // capacity 1: "the queue changed"

	stop context.CancelFunc // hard stop for the drainer
	done chan struct{}      // closed when the drainer exits

	counters *stats.Counters
}

// StreamNameForBinding derives a stream name from its subject filter, so the
// two cannot disagree: uppercased, dots to dashes, trailing wildcard dropped.
// "openits.us-ga.metro.d01.>" becomes "OPENITS-US-GA-METRO-D01".
//
// Derived rather than configured because with a stream per namespace a single
// configured name is meaningless, and a per-namespace config block would be a
// second source of truth free to disagree with the bindings it is supposed to
// name. An explicit override stays additive if an operator ever needs one.
func StreamNameForBinding(binding string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(binding, ">"), ".")
	return strings.ToUpper(strings.ReplaceAll(trimmed, ".", "-"))
}

// Start begins connecting to url in the background and returns at once. It
// fails only on what can never work (no template, no ce-types, a malformed
// URL), never on an absent broker. ctx supplies values only: the publisher
// outlives its cancellation, because shutdown's Drain runs after the
// caller's context is done. Streams — one per ce-type namespace
// (ADR 0011), with subject filters derived from the template so grammar and
// capture cannot disagree — are provisioned on connect and again whenever a
// publish finds one missing.
//
// c receives the publisher's counts (published, and the three losses); nil
// keeps them private, readable through Stats.
func Start(ctx context.Context, url string, sec Security, tmpl *subject.Template, ceTypes []string, log *slog.Logger, c *stats.Counters) (*Publisher, error) {
	tune := defaults
	tune.counters = c
	tune.security = sec
	return start(ctx, url, tmpl, ceTypes, log, tune)
}

func start(ctx context.Context, url string, tmpl *subject.Template, ceTypes []string, log *slog.Logger, tune tuning) (*Publisher, error) {
	if tmpl == nil {
		return nil, errors.New("publish: subject template is required")
	}
	bindings, err := tmpl.Bindings(ceTypes)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, errors.New("publish: no ce-types given, so there is no stream to provision")
	}
	if log == nil {
		log = slog.Default()
	}
	if tune.counters == nil {
		tune.counters = new(stats.Counters)
	}
	// Named once, at boot: an operator writing the broker grant for a
	// custom subject template needs these (ADR 0022 decision 3).
	streams := make([]string, len(bindings))
	for i, b := range bindings {
		streams[i] = StreamNameForBinding(b)
	}
	log.Info("publishing to streams", "streams", streams)
	safe := redactURL(url) // the only form of url that may be logged or returned
	scrub := scrubber(url)
	if err := ValidateSecurity(url, tune.security); err != nil {
		return nil, err
	}
	var auth []nats.Option
	if tune.security.CredsFile != "" {
		auth = append(auth, nats.UserCredentials(tune.security.CredsFile))
	}
	if tune.security.CAFile != "" {
		auth = append(auth, nats.RootCAs(tune.security.CAFile))
	}
	nc, err := nats.Connect(url, append(auth,
		nats.Name("vikasa-collector"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(tune.reconnectWait),
		// No client-side reconnect buffer: while disconnected a publish fails
		// fast and the event waits in our queue, which is bounded and counted.
		nats.ReconnectBufSize(-1),
		nats.ConnectHandler(func(*nats.Conn) { log.Info("broker connected", "url", safe) }),
		nats.ReconnectHandler(func(*nats.Conn) { log.Info("broker reconnected", "url", safe) }),
		// Async errors are where the broker reports a permission violation
		// (ADR 0022 decision 3); without this a too-tight grant is silent.
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Error("broker refused an operation", "url", safe, "err", scrub.Replace(err.Error()))
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("broker disconnected", "url", safe, "err", scrub.Replace(err.Error()))
			}
		}),
	)...)
	if err != nil {
		// Never %w, and never the parser's own message: it quotes the input,
		// and any fragment of that can be part of a password.
		var ue *neturl.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("publish: -nats %s is not a valid URL (parser detail withheld: it would quote any credentials)", safe)
		}
		return nil, fmt.Errorf("publish: nats %s: %s", safe, scrub.Replace(err.Error()))
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	dctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	p := &Publisher{
		nc: nc, js: js, tmpl: tmpl, bindings: bindings, log: log, tune: tune, scrub: scrub, counters: tune.counters,
		wake: make(chan struct{}, 1), stop: stop, done: make(chan struct{}),
	}
	go p.drain(dctx)
	return p, nil
}

// Headers renders an envelope as CloudEvents binary-mode headers: every
// attribute becomes a ce-* header and the body stays the raw encoded payload.
//
// Nats-Msg-Id carries the same value as ce-id so JetStream's own dedup window
// keys on the event identity rather than on a second, unrelated id — which is
// also what makes the drainer's retries safe.
//
// ce-dataschema is omitted entirely when empty rather than sent blank: the
// collector-owned health schema (ADR 0007) has no registry entry to point at,
// and an empty attribute would claim it does.
func Headers(env cloudevents.Envelope) nats.Header {
	h := nats.Header{}
	h.Set("ce-specversion", env.SpecVersion)
	h.Set("ce-id", env.ID)
	h.Set("ce-source", env.Source)
	h.Set("ce-type", env.Type)
	h.Set("ce-time", env.Time.UTC().Format(time.RFC3339Nano))
	h.Set("ce-datacontenttype", env.DataContentType)
	if env.DataSchema != "" {
		h.Set("ce-dataschema", env.DataSchema)
	}
	h.Set("Nats-Msg-Id", env.ID)
	return h
}

// Enqueue renders env's subject and queues it for publishing. It never
// blocks: a full queue evicts its oldest event. It fails only if the subject
// cannot be rendered or the publisher is draining.
func (p *Publisher) Enqueue(env cloudevents.Envelope, deviceID string) error {
	subj, err := p.tmpl.Render(env.Type, deviceID)
	if err != nil {
		return err
	}
	msg := &nats.Msg{Subject: subj, Data: env.Data, Header: Headers(env)}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("publish: draining, not accepting events")
	}
	if len(p.queue) >= p.tune.capacity {
		p.queue[0] = nil
		p.queue = p.queue[1:]
		p.counters.DroppedQueueFull.Add(1)
	}
	p.queue = append(p.queue, msg)
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default:
	}
	return nil
}

// JetStream is the publisher's JetStream handle, for other cabinet-local
// state that should share its one broker connection (the synth state
// bucket, ADR 0017).
func (p *Publisher) JetStream() jetstream.JetStream { return p.js }

// Connected reports whether the broker connection is up right now.
func (p *Publisher) Connected() bool { return p.nc.IsConnected() }

// Stats returns the counters and the current queue depth.
func (p *Publisher) Stats() Stats {
	p.mu.Lock()
	queued := len(p.queue)
	p.mu.Unlock()
	return Stats{
		Published:        p.counters.EventsPublished.Load(),
		QueueDropped:     p.counters.DroppedQueueFull.Load(),
		PermanentDropped: p.counters.DroppedUnpublishable.Load(),
		ShutdownDropped:  p.counters.DroppedAtShutdown.Load(),
		Queued:           queued,
	}
}

// Drain stops accepting events, keeps publishing until the queue is empty
// or ctx ends, counts and logs whatever is left, and closes the connection.
func (p *Publisher) Drain(ctx context.Context) {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}

	select {
	case <-p.done:
	case <-ctx.Done():
		p.stop()
		<-p.done
	}
	p.stop()

	p.mu.Lock()
	left := len(p.queue)
	p.queue = nil
	p.mu.Unlock()
	if left > 0 {
		p.counters.DroppedAtShutdown.Add(uint64(left))
		p.log.Warn("shutdown drain window closed with events unpublished", "dropped", left)
	}
	p.nc.Close()
}

// drain is the single publishing goroutine. It provisions the streams
// first (and again whenever a publish finds one missing), then takes events
// out of the queue one at a time — so the event in flight can never be
// evicted by a full queue — and retries each until it is published, found
// permanently unpublishable, or the publisher is stopped.
func (p *Publisher) drain(ctx context.Context) {
	defer close(p.done)
	provisioned := false
	backoff := p.tune.backoffMin
	failing := false

	var inflight *nats.Msg
	defer func() {
		if inflight != nil {
			p.counters.DroppedAtShutdown.Add(1)
		}
	}()

	fail := func(err error) bool {
		if !failing {
			failing = true
			// With no reconnect buffer, a disconnected publish fails with
			// "outbound buffer limit exceeded" — true, but not what an
			// operator needs to read.
			if !p.nc.IsConnected() {
				err = fmt.Errorf("not connected to the broker (%s)", p.nc.Status())
			}
			p.log.Warn("publishing unavailable; queueing until the broker is back", "err", p.scrub.Replace(err.Error()))
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return false
		}
		backoff = min(backoff*2, p.tune.backoffMax)
		return true
	}
	recovered := func() {
		backoff = p.tune.backoffMin
		if failing {
			failing = false
			p.log.Info("publishing recovered", "queued", p.Stats().Queued)
		}
	}

	for {
		if !provisioned {
			if inflight == nil && p.drainedEmpty() {
				return
			}
			pctx, cancel := context.WithTimeout(ctx, p.tune.publishTimeout)
			err := p.provision(pctx)
			cancel()
			if err != nil {
				if !fail(err) {
					return
				}
				continue
			}
			provisioned = true
			recovered()
		}

		if inflight == nil {
			if inflight = p.next(ctx); inflight == nil {
				return
			}
		}

		pctx, cancel := context.WithTimeout(ctx, p.tune.publishTimeout)
		// No client-side retries: this loop is the one retry policy.
		_, err := p.js.PublishMsg(pctx, inflight, jetstream.WithRetryAttempts(0))
		cancel()
		switch {
		case err == nil:
			p.counters.EventsPublished.Add(1)
			inflight = nil
			recovered()
		case permanent(err):
			p.counters.DroppedUnpublishable.Add(1)
			p.log.Error("event dropped: broker will never accept it", "subject", inflight.Subject, "err", p.scrub.Replace(err.Error()))
			inflight = nil
		default:
			// Re-provision before the next attempt. A failure can mean the
			// stream is gone (a broker restarted with a wiped store), and
			// CreateOrUpdateStream on an unchanged config is a no-op, so
			// doing it on every transient failure costs nothing and needs no
			// guess about which error shape "stream missing" arrives in.
			provisioned = false
			if !fail(err) {
				return
			}
		}
	}
}

// drainedEmpty reports that Drain has been called and nothing is queued.
func (p *Publisher) drainedEmpty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed && len(p.queue) == 0
}

// next blocks until an event is queued and removes it, or returns nil once
// the publisher is draining with nothing left, or is hard-stopped.
func (p *Publisher) next(ctx context.Context) *nats.Msg {
	for {
		p.mu.Lock()
		if len(p.queue) > 0 {
			m := p.queue[0]
			p.queue[0] = nil
			p.queue = p.queue[1:]
			p.mu.Unlock()
			return m
		}
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return nil
		}
		select {
		case <-p.wake:
		case <-ctx.Done():
			return nil
		}
	}
}

// provision ensures one stream per binding. CreateOrUpdateStream is
// idempotent, so re-running it after a reconnect is safe.
func (p *Publisher) provision(ctx context.Context) error {
	for _, b := range p.bindings {
		if _, err := p.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:       StreamNameForBinding(b),
			Subjects:   []string{b},
			Storage:    jetstream.FileStorage,
			Duplicates: dedupWindow,
		}); err != nil {
			return fmt.Errorf("ensure stream for %s: %w", b, err)
		}
	}
	return nil
}

// permanent reports a failure no retry can fix: the event is dropped and
// counted so it cannot block everything queued behind it.
func permanent(err error) bool {
	return errors.Is(err, nats.ErrMaxPayload) || errors.Is(err, nats.ErrBadSubject)
}

// scrubber replaces every literal copy of the configured server URLs —
// raw, and as strconv.Quote would escape them — with their redacted form.
// It deliberately does no parsing: a scrubber that parses can disagree with
// the parser that produced the text (userinfo ends at the LAST '@', the
// authority at the first '/', quotes are escaped), and every disagreement
// is a leak.
func scrubber(servers string) *strings.Replacer {
	var pairs []string
	add := func(raw string) {
		if raw == "" {
			return
		}
		safe := redactURL(raw)
		quoted := strconv.Quote(raw)
		pairs = append(pairs, raw, safe, quoted[1:len(quoted)-1], safe)
	}
	add(servers)
	for part := range strings.SplitSeq(servers, ",") {
		add(strings.TrimSpace(part))
	}
	return strings.NewReplacer(pairs...)
}

// redactURL makes a NATS server list safe to log. It drops each URL's
// userinfo outright — URL.Redacted masks only a password, and NATS also
// accepts a bare token as the username — and replaces anything it cannot
// parse, since an unparseable string could itself be a secret.
func redactURL(servers string) string {
	parts := strings.Split(servers, ",")
	for i, s := range parts {
		u, err := neturl.Parse(strings.TrimSpace(s))
		if err != nil || u.Host == "" {
			parts[i] = "<unparseable url>"
			continue
		}
		u.User = nil
		parts[i] = u.String()
	}
	return strings.Join(parts, ",")
}
