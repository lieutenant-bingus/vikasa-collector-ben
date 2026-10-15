// Package durable keeps the synth engine's previous state across restarts
// (ADR 0017), in a key-value bucket on the cabinet's own JetStream.
//
// Without it a restarted collector diffs against nothing: the fault differ
// re-raises every standing fault, and every other differ silently loses
// whatever changed while the process was down. With it, the engine is held
// until its state is loaded (ADR 0020 clause 2), then seeded, and every
// change it remembers afterwards is written through.
//
// The bucket holds one JSON-encoded facet per (device, facet kind), history
// 1, written only when the encoding changes — so flash writes track real
// transitions, not polls. Volatile kinds (detector counters) never reach
// it; see synth's volatile.
package durable

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Bucket is the KV bucket's name. One collector per cabinet (ADR 0017), so
// one fixed name: there is no second writer to keep apart.
const Bucket = "VIKASA_SYNTH_STATE"

// maxValueSize caps one stored facet. The largest real facet (an interval
// report across a sensor's lanes or zones) is a few KB.
const maxValueSize = 64 << 10

type tuning struct {
	backoffMin, backoffMax time.Duration
	opTimeout              time.Duration // one KV operation
}

var defaults = tuning{backoffMin: 100 * time.Millisecond, backoffMax: 5 * time.Second, opTimeout: 2 * time.Second}

// State loads the engine's previous state, seeds it, and writes through
// every change afterwards.
type State struct {
	js        jetstream.JetStream
	engine    *synth.Engine
	deviceIDs []string
	log       *slog.Logger
	tune      tuning

	mu      sync.Mutex
	pending map[string][]byte // key -> latest unwritten encoding
	written map[string][]byte // key -> what the bucket holds
	closed  bool
	wake    chan struct{}

	seeded chan struct{}
	stop   context.CancelFunc
	done   chan struct{}
}

// Start installs itself as engine's change hook and begins, in the
// background, to create the bucket, load it, and seed every device in
// deviceIDs — a device with nothing stored is seeded empty, which is the
// legitimate first boot. engine must be held (synth.Engine.HoldUntilSeeded)
// and not yet applied to. Start never waits for the broker; until it can
// load, the engine stays held. ctx supplies values only.
func Start(ctx context.Context, js jetstream.JetStream, engine *synth.Engine, deviceIDs []string, log *slog.Logger) *State {
	return start(ctx, js, engine, deviceIDs, log, defaults)
}

func start(ctx context.Context, js jetstream.JetStream, engine *synth.Engine, deviceIDs []string, log *slog.Logger, tune tuning) *State {
	if log == nil {
		log = slog.Default()
	}
	lctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	s := &State{
		js: js, engine: engine, deviceIDs: deviceIDs, log: log, tune: tune,
		pending: make(map[string][]byte), written: make(map[string][]byte),
		wake: make(chan struct{}, 1), seeded: make(chan struct{}), stop: stop, done: make(chan struct{}),
	}
	engine.WithChangeHook(s.record)
	go s.run(lctx)
	return s
}

// Seeded is closed once every device has been seeded and the engine released.
func (s *State) Seeded() <-chan struct{} { return s.seeded }

// Flush writes what is pending until it is all written or ctx ends, then
// stops. Anything unwritten is logged: the next boot will diff against the
// last state that was written, which can re-emit a transition already sent.
func (s *State) Flush(ctx context.Context) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.poke()
	select {
	case <-s.done:
	case <-ctx.Done():
		s.stop()
		<-s.done
	}
	s.stop()
	s.mu.Lock()
	left := len(s.pending)
	s.mu.Unlock()
	if left > 0 {
		s.log.Warn("shutdown with synth state unwritten; the next boot may re-emit those transitions", "keys", left)
	}
}

// key names a (device, kind) entry. The device ID is base64url-encoded:
// config does not restrict it to KV-key characters, and that alphabet is
// entirely KV-safe. Kinds are [a-z-] already.
func key(deviceID string, k model.Kind) string {
	return base64.RawURLEncoding.EncodeToString([]byte(deviceID)) + "." + string(k)
}

func parseKey(s string) (deviceID string, k model.Kind, ok bool) {
	enc, kind, found := strings.Cut(s, ".")
	if !found {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", "", false
	}
	return string(raw), model.Kind(kind), true
}

// record is the engine's change hook. It runs under the engine's lock, so it
// only notes the latest encoding and returns; the writer goroutine does I/O.
func (s *State) record(deviceID string, k model.Kind, f model.Facet) {
	raw, err := json.Marshal(f)
	if err != nil {
		s.log.Error("synth state not encodable; it will not survive a restart", "device", deviceID, "kind", string(k), "err", err)
		return
	}
	kk := key(deviceID, k)
	s.mu.Lock()
	_, queued := s.pending[kk]
	if !queued && bytes.Equal(raw, s.written[kk]) {
		s.mu.Unlock()
		return // unchanged: no write, which is what bounds flash wear
	}
	s.pending[kk] = raw
	s.mu.Unlock()
	s.poke()
}

func (s *State) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *State) run(ctx context.Context) {
	defer close(s.done)
	backoff := s.tune.backoffMin
	wait := func() bool {
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return false
		}
		backoff = min(backoff*2, s.tune.backoffMax)
		return true
	}

	var kv jetstream.KeyValue
	warned := false
	for {
		var err error
		if kv, err = s.load(ctx); err == nil {
			break
		}
		if !warned {
			warned = true
			s.log.Warn("synth state unavailable; holding change detection until it loads", "err", err)
		}
		if !wait() {
			return
		}
	}
	close(s.seeded)
	s.log.Info("synth state loaded", "devices", len(s.deviceIDs))
	backoff = s.tune.backoffMin

	for {
		batch := s.takePending()
		if len(batch) == 0 {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			select {
			case <-s.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		if err := s.write(ctx, kv, batch); err != nil {
			s.log.Warn("synth state write failed; retrying", "err", err)
			if !wait() {
				return
			}
			continue
		}
		backoff = s.tune.backoffMin
	}
}

// load creates the bucket if needed, reads every entry, and seeds the engine.
func (s *State) load(ctx context.Context) (jetstream.KeyValue, error) {
	octx, cancel := context.WithTimeout(ctx, s.tune.opTimeout)
	defer cancel()
	kv, err := s.js.CreateOrUpdateKeyValue(octx, jetstream.KeyValueConfig{
		Bucket: Bucket, History: 1, MaxValueSize: maxValueSize, Storage: jetstream.FileStorage,
		Description: "vikasa-collector synth previous state (ADR 0017); cabinet-local, never replicated",
	})
	if err != nil {
		return nil, fmt.Errorf("ensure bucket %s: %w", Bucket, err)
	}
	keys, err := kv.ListKeys(octx)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", Bucket, err)
	}
	byDevice := make(map[string][]model.Facet)
	written := make(map[string][]byte)
	for kk := range keys.Keys() {
		deviceID, k, ok := parseKey(kk)
		if !ok {
			continue // not ours; leave it alone
		}
		entry, err := kv.Get(octx, kk)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get %s: %w", kk, err)
		}
		f, err := s.engine.DecodeFacet(k, entry.Value())
		if err != nil {
			// A kind this build no longer diffs, or a shape it cannot read:
			// that device starts this kind from a first observation.
			s.log.Warn("stored synth state not usable; starting it fresh", "device", deviceID, "kind", string(k), "err", err)
			continue
		}
		byDevice[deviceID] = append(byDevice[deviceID], f)
		written[kk] = entry.Value()
	}

	s.mu.Lock()
	for kk, v := range written {
		s.written[kk] = v
	}
	s.mu.Unlock()
	for _, id := range s.deviceIDs {
		s.engine.Seed(id, byDevice[id])
	}
	return kv, nil
}

// batchEntry is one key taken for writing, with what the bucket held before
// so a failed write can be rolled back.
type batchEntry struct{ value, before []byte }

// takePending moves pending into a batch and marks it written at once, so a
// record of the same value while the Put is in flight is seen as unchanged
// rather than queued for a second, identical write.
func (s *State) takePending() map[string]batchEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	batch := make(map[string]batchEntry, len(s.pending))
	for kk, v := range s.pending {
		batch[kk] = batchEntry{value: v, before: s.written[kk]}
		s.written[kk] = v
	}
	s.pending = make(map[string][]byte)
	return batch
}

// write puts a batch. A failed entry is rolled back to what the bucket
// still holds and re-queued, unless a newer value has been recorded since.
func (s *State) write(ctx context.Context, kv jetstream.KeyValue, batch map[string]batchEntry) error {
	var firstErr error
	for kk, b := range batch {
		octx, cancel := context.WithTimeout(ctx, s.tune.opTimeout)
		_, err := kv.Put(octx, kk, b.value)
		cancel()
		if err == nil {
			continue
		}
		s.mu.Lock()
		if bytes.Equal(s.written[kk], b.value) {
			s.written[kk] = b.before
		}
		if _, newer := s.pending[kk]; !newer {
			s.pending[kk] = b.value
		}
		s.mu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
