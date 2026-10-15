package logship

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"testing/slogtest"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
)

// The wrapper is still a correct slog.Handler for the local log: the
// standard library's own conformance suite, run against what reaches the
// wrapped (base) handler.
func TestBaseHandlerStillConforms(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(slog.NewJSONHandler(&buf, nil), slog.LevelWarn, 10)
	results := func() []map[string]any {
		var out []map[string]any
		for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		return out
	}
	if err := slogtest.TestHandler(h, results); err != nil {
		t.Fatal(err)
	}
}

// Records at or above the floor are shipped with their message, level and
// every attribute — With() attrs and groups included, flattened — while
// below-floor records are not.
func TestShipsAtOrAboveTheFloorWithAttrs(t *testing.T) {
	h := NewHandler(slog.DiscardHandler, slog.LevelWarn, 10)
	log := slog.New(h).With("device", "asc-1").WithGroup("snmp")
	log.Info("routine", "k", "v")
	log.Warn("read timeout", "attempt", 3)

	got := drain(h)
	if len(got) != 1 {
		t.Fatalf("shipped %d records, want 1 (warn only)", len(got))
	}
	r := got[0]
	if r.Level != "WARN" || r.Message != "read timeout" || r.Attrs["device"] != "asc-1" || r.Attrs["snmp.attempt"] != "3" {
		t.Fatalf("shipped = %+v", r)
	}
}

// ADR 0019 clause 10: rate-limited per window; the excess is counted, never
// shipped, so a flood cannot starve the counter that reports it.
func TestRateLimitCountsTheExcess(t *testing.T) {
	var c stats.Counters
	h := NewHandler(slog.DiscardHandler, slog.LevelWarn, 3)
	h.counters = &c
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	h.window = time.Minute
	log := slog.New(h)
	for range 5 {
		log.Error("boom")
	}
	if n := len(drain(h)); n != 3 || c.LogsSuppressed.Load() != 2 {
		t.Fatalf("shipped %d, suppressed %d; want 3 and 2", n, c.LogsSuppressed.Load())
	}
	now = now.Add(time.Minute) // next window
	log.Error("after")
	if n := len(drain(h)); n != 1 {
		t.Fatalf("next window shipped %d, want 1", n)
	}
}

// A shipped record must never carry what the local log would not: the
// credential-scrubbed text is all that exists to ship (the publisher scrubs
// before logging), and messages stay strings, not arbitrary objects.
func TestAttrsAreRenderedAsStrings(t *testing.T) {
	h := NewHandler(slog.DiscardHandler, slog.LevelWarn, 10)
	slog.New(h).Error("x", "n", 42, "d", 1500*time.Millisecond, "err", context.DeadlineExceeded)
	r := drain(h)[0]
	if r.Attrs["n"] != "42" || r.Attrs["d"] != "1.5s" || !strings.Contains(r.Attrs["err"], "deadline") {
		t.Fatalf("attrs = %v", r.Attrs)
	}
}

func drain(h *Handler) []Record {
	var out []Record
	for {
		select {
		case r := <-h.Records():
			out = append(out, r)
		default:
			return out
		}
	}
}
