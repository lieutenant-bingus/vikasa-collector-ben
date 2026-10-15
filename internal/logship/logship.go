// Package logship publishes the collector's own warn-and-above log records
// as events (ADR 0019 clause 10). Counters say something is wrong; logs say
// what — and every error path otherwise writes to stderr in a cabinet nobody
// can visit.
//
// Handler wraps the process's slog.Handler: every record still reaches the
// local log unchanged, and records at or above a floor are also handed to a
// bounded channel that the collector drains into the event pipeline. Shipping
// is rate-limited per window (the heartbeat interval, so there is one
// tunable, not two), and the excess is counted in logs_suppressed, which
// rides the heartbeat: a flood cannot starve the counter that reports it.
package logship

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
)

// Record is one shipped log record. Attribute values are rendered as
// strings: the structure (keys, including group prefixes) is kept, and a
// value is exactly what the local log would print.
type Record struct {
	Time    time.Time
	Level   string
	Message string
	Attrs   map[string]string
}

// shared is the state every With/WithGroup clone of a Handler shares.
type shared struct {
	floor    slog.Level
	limit    int
	out      chan Record
	counters *stats.Counters
	now      func() time.Time
	window   time.Duration

	mu          sync.Mutex
	windowStart time.Time
	sent        int
}

// Handler is an slog.Handler that tees records at or above its floor to
// Records.
type Handler struct {
	base slog.Handler
	*shared
	attrs  []slog.Attr // From WithAttrs, already prefixed by their groups.
	prefix string      // Current group path, "a.b.", for later attributes.
}

// NewHandler wraps base, shipping records at or above floor, at most limit
// per window. Until Configure is called the window is five minutes (the
// heartbeat default) and counts are private.
func NewHandler(base slog.Handler, floor slog.Level, limit int) *Handler {
	return &Handler{base: base, shared: &shared{
		floor: floor, limit: limit, out: make(chan Record, 4*limit),
		counters: new(stats.Counters), now: time.Now, window: 5 * time.Minute,
	}}
}

// Configure sets the rate-limit window (the heartbeat interval) and the
// counters logs_suppressed is counted in. Call it before logging begins.
func (h *Handler) Configure(window time.Duration, c *stats.Counters) {
	h.window, h.counters = window, c
}

// Records is the channel shipped records arrive on.
func (h *Handler) Records() <-chan Record { return h.out }

// Enabled reports whether either the local log or shipping wants level.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.floor || h.base.Enabled(ctx, level)
}

// Handle writes r to the local log, if it wants it, and ships it if it is at
// or above the floor.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if h.base.Enabled(ctx, r.Level) {
		err = h.base.Handle(ctx, r)
	}
	if r.Level >= h.floor {
		h.ship(r)
	}
	return err
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.base = h.base.WithAttrs(as)
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), prefixed(h.prefix, as)...)
	return &c
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.base = h.base.WithGroup(name)
	c.prefix = h.prefix + name + "."
	return &c
}

func (h *Handler) ship(r slog.Record) {
	if !h.admit() {
		h.counters.LogsSuppressed.Add(1)
		return
	}
	attrs := make(map[string]string, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		flatten(attrs, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		flatten(attrs, h.prefix, a)
		return true
	})
	rec := Record{Time: r.Time.UTC(), Level: r.Level.String(), Message: r.Message, Attrs: attrs}
	select {
	case h.out <- rec:
	default:
		h.counters.LogsSuppressed.Add(1) // the drainer is behind; never block a logger
	}
}

// admit applies the per-window limit.
func (h *Handler) admit() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if h.windowStart.IsZero() || now.Sub(h.windowStart) >= h.window {
		h.windowStart, h.sent = now, 0
	}
	if h.sent >= h.limit {
		return false
	}
	h.sent++
	return true
}

func prefixed(prefix string, as []slog.Attr) []slog.Attr {
	if prefix == "" {
		return as
	}
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = slog.Attr{Key: prefix + a.Key, Value: a.Value}
	}
	return out
}

// flatten writes a into m, expanding group values to dotted keys and
// dropping empty attributes, as slog's own handlers do.
func flatten(m map[string]string, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p = prefix + a.Key + "."
		}
		for _, g := range v.Group() {
			flatten(m, p, g)
		}
		return
	}
	m[strings.TrimSuffix(prefix+a.Key, ".")] = v.String()
}
