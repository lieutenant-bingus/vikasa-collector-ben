# Quick-Wins — C6 / C7 / C9 Audit Fixes Implementation Plan


**Goal:** Fix three small, independent, diagnosed bugs from the architecture audit: C6 (float64 env overrides silently ignored), C7 (unbounded Prometheus cardinality on `unmapped_events_total`), and C9 (ATSPM ingest discards the source's observed-time).

**Architecture:** Three unrelated targeted fixes, one per task/commit. C6: add a `reflect.Float64` case to the config env-override reflection walker. C7: drop the two high-cardinality label dimensions from the `unmapped_events_total` metric (the code/param detail already rides the published `UnmappedEvent`). C9: thread the observed-at time from `Source.Fetch` into `DecodeOptions.FileTime` instead of substituting `time.Now()`.

**Tech Stack:** Go 1.26, `github.com/prometheus/client_golang`, stdlib `testing`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- These are intended behavior CORRECTIONS: C6 makes previously-dead env vars take effect; C7 removes two metric labels (any dashboard querying `indiana_code`/`indiana_param` must adapt — they were a cardinality bomb); C9 changes HR-event `FileTime` from poll-time to the source's observed-at. Each is the point of the fix.
- Tasks are independent — order doesn't matter; keep them separate commits.
- Commit after each task; `gofmt -s -w .` before committing. Full suite (14 packages) stays green.

---

### Task 1: C6 — float64 env overrides

**Files:**
- Modify: `internal/config/config.go` (add a `reflect.Float64` case in `applyEnvOverridesValue`)
- Test: `internal/config/config_test.go` (add a float-override test)

- [ ] **Step 1: Add the `reflect.Float64` case.** In `internal/config/config.go`, in `applyEnvOverridesValue`, the `switch field.Kind()` currently handles `String`, `Int`/`Int64` (with a `time.Duration` special case), and `Bool`. Add a `Float64` case (`strconv` is already imported):

```go
		case reflect.Float64:
			if f, err := strconv.ParseFloat(envVal, 64); err == nil {
				field.SetFloat(f)
			}
```

- [ ] **Step 2: Add the failing test** to `internal/config/config_test.go`:

```go
func TestApplyEnvOverrides_Float64(t *testing.T) {
	t.Setenv("OPENITS_RATELIMIT_GLOBAL_RATE", "250.5")
	t.Setenv("OPENITS_TRACING_SAMPLE_RATE", "0.25")
	cfg := DefaultConfig()
	applyEnvOverrides(cfg)
	if cfg.RateLimit.GlobalRate != 250.5 {
		t.Errorf("RateLimit.GlobalRate = %v, want 250.5 (env override ignored)", cfg.RateLimit.GlobalRate)
	}
	if cfg.Tracing.SampleRate != 0.25 {
		t.Errorf("Tracing.SampleRate = %v, want 0.25 (env override ignored)", cfg.Tracing.SampleRate)
	}
}
```

> Verify `applyEnvOverrides` (unexported) is the function that walks env overrides and that `RateLimitConfig.GlobalRate` / `TracingConfig.SampleRate` carry those exact env tags (`OPENITS_RATELIMIT_GLOBAL_RATE`, `OPENITS_TRACING_SAMPLE_RATE`). If a tag differs, use the actual tag and note it.

- [ ] **Step 3: Run the test to verify it fails, then passes after the fix.**

Run: `go test ./internal/config/... -run TestApplyEnvOverrides_Float64 -v`
Expected before the Step-1 edit: FAIL (values stay at defaults 100 / 1.0). After: PASS. (If writing test-first, add Step 2 before Step 1 to see RED.)

- [ ] **Step 4: Run config tests + full suite, then commit.**

```bash
gofmt -s -w . && go test ./internal/config/... -v && go test ./...
git add internal/config/config.go internal/config/config_test.go
git commit -m "fix(config): apply float64 env overrides (were silently ignored)"
```

---

### Task 2: C7 — bound `unmapped_events_total` cardinality

The metric is declared with labels `{decoder, indiana_code, indiana_param}` (`internal/metrics/metrics.go:296`); `indiana_param` is 0-255 and `indiana_code` 0-65535, so the series set is effectively unbounded. The specific code/param already ride the published `UnmappedEvent` payload, so drop them from the metric — keep only `decoder`.

**Files:**
- Modify: `internal/metrics/metrics.go:291-296` (`UnmappedEventsTotal` label set)
- Modify: `internal/atspm/decoders/econolite/econolite.go:106` (call site)
- Modify: `internal/atspm/decoders/mccain/mccain.go:133` (call site)

> Note: `WithLabelValues` panics at RUNTIME (not compile time) if the arg count doesn't match the label count. Both call sites MUST be updated in lockstep with the declaration, or an unmapped row panics the decode goroutine.

- [ ] **Step 1: Reduce the label set.** In `internal/metrics/metrics.go`, change `UnmappedEventsTotal`'s labels and Help:

```go
	UnmappedEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "openits",
		Subsystem: "decoder",
		Name:      "unmapped_events_total",
		Help:      "Wire rows the decoder could not type, by decoder family. The specific wire code/param ride the published UnmappedEvent payload.",
	}, []string{"decoder"})
```

- [ ] **Step 2: Update both call sites** to pass exactly one label value:
  - `internal/atspm/decoders/econolite/econolite.go:106`: `metrics.UnmappedEventsTotal.WithLabelValues("indiana", strconv.Itoa(int(code)), strconv.Itoa(int(param))).Inc()` → `metrics.UnmappedEventsTotal.WithLabelValues("indiana").Inc()`
  - `internal/atspm/decoders/mccain/mccain.go:133`: `metrics.UnmappedEventsTotal.WithLabelValues("indiana", strconv.Itoa(int(codeNum)), strconv.Itoa(int(paramNum))).Inc()` → `metrics.UnmappedEventsTotal.WithLabelValues("indiana").Inc()`

  If removing those `strconv.Itoa` calls leaves `strconv` unused in a file, remove the `strconv` import (`go build` reports it). If `strconv` is still used elsewhere in the file (e.g. mccain parses codeNum/paramNum), leave the import.

- [ ] **Step 3: Verify no stale 3-arg call remains + build + full suite.**

Run:
```bash
gofmt -s -w . && go build ./... && go vet ./...
grep -rn 'UnmappedEventsTotal.WithLabelValues' internal/   # every hit must pass exactly ONE arg
go test ./...
```
Expected: build/vet clean; the grep shows only single-arg calls; suite green. (A missed 3-arg call would compile but panic at runtime — the grep is the guard.)

- [ ] **Step 4: Commit.**

```bash
git add internal/metrics/metrics.go internal/atspm/decoders/econolite/econolite.go internal/atspm/decoders/mccain/mccain.go
git commit -m "fix(metrics): drop unbounded code/param labels from unmapped_events_total

indiana_code (0-65535) x indiana_param (0-255) made the series set
effectively unbounded. The specific code/param already ride the published
UnmappedEvent payload; keep only the bounded 'decoder' label."
```

---

### Task 3: C9 — thread the source's observed-time into `DecodeOptions.FileTime`

`internal/atspm/ingest/ingest.go:83` discards `Source.Fetch`'s observed-at return (`rdr, _, err`) and substitutes `FileTime: time.Now().UTC()` (`:96`), defeating the whole `FileTime` plumbing (documented as "the SFTP modified-time or the synthetic emit time").

**Files:**
- Modify: `internal/atspm/ingest/ingest.go` (`tickOnce`)
- Test: `internal/atspm/ingest/ingest_test.go` (create)

- [ ] **Step 1: Use the observed-at time.** In `internal/atspm/ingest/ingest.go`'s `tickOnce`, capture the second return of `Fetch` and use it for `FileTime`, falling back to now only when the source didn't supply one:

```go
func (r *Runner) tickOnce(ctx context.Context) {
	rdr, observedAt, err := r.Source.Fetch(ctx)
	if err != nil {
		r.Logger.Warn("atspm ingest: fetch failed",
			"controller", r.ControllerID, "source", r.Source.Kind(), "error", err)
		return
	}
	if rdr == nil {
		return
	}
	defer rdr.Close()

	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	events, err := r.Decoder.Decode(ctx, rdr, decoders.DecodeOptions{
		ControllerID: r.ControllerID,
		FileTime:     observedAt,
	})
	// ... rest unchanged ...
}
```

- [ ] **Step 2: Write the test** proving the observed-at flows to `FileTime`. Create `internal/atspm/ingest/ingest_test.go`:

```go
package ingest

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/atspm/decoders"
)

// fakeSource returns a fixed reader + observed-at once, then nothing.
type fakeSource struct {
	observedAt time.Time
	done       bool
}

func (s *fakeSource) Kind() string { return "fake" }
func (s *fakeSource) Fetch(context.Context) (io.ReadCloser, time.Time, error) {
	if s.done {
		return nil, time.Time{}, nil
	}
	s.done = true
	return io.NopCloser(strings.NewReader("")), s.observedAt, nil
}

// captureDecoder records the FileTime it was handed.
type captureDecoder struct{ gotFileTime time.Time }

func (d *captureDecoder) Kind() string { return "capture" }
func (d *captureDecoder) Decode(_ context.Context, _ io.Reader, opts decoders.DecodeOptions) ([]decoders.Event, error) {
	d.gotFileTime = opts.FileTime
	return nil, nil
}

func TestTickOnce_PassesSourceObservedTimeAsFileTime(t *testing.T) {
	observed := time.Date(2026, 7, 10, 8, 30, 0, 0, time.UTC)
	dec := &captureDecoder{}
	r := &Runner{
		ControllerID: "asc-001",
		Source:       &fakeSource{observedAt: observed},
		Decoder:      dec,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.tickOnce(context.Background())
	if !dec.gotFileTime.Equal(observed) {
		t.Errorf("FileTime = %v, want the source's observed-at %v", dec.gotFileTime, observed)
	}
}
```

> Verify the exact interface signatures before running: `atspmsources.Source.Fetch(ctx) (io.ReadCloser, time.Time, error)` and `atspmdecoders.Decoder.Decode(ctx, io.Reader, DecodeOptions) ([]Event, error)` + `Kind() string`, and the `Runner` struct field names (`ControllerID`, `Source`, `Decoder`, `Logger`, ...). Adjust the fakes to match the real signatures if they differ, and note it. The `Publisher` field is unused here because the decoder returns zero events.

- [ ] **Step 3: Run tests + full suite, then commit.**

```bash
gofmt -s -w . && go test ./internal/atspm/ingest/... -v && go test ./...
git add internal/atspm/ingest/ingest.go internal/atspm/ingest/ingest_test.go
git commit -m "fix(atspm): use the source's observed-time as FileTime (was time.Now)

Fetch returns the observation time (SFTP mtime / HTTP response / synthetic
tick); tickOnce discarded it and substituted poll-time, defeating the
FileTime contract. Thread it through, falling back to now only when absent."
```

---

## Self-Review

**Spec coverage:** C6 (Task 1) adds the missing `reflect.Float64` case + a test asserting two float env vars now apply. C7 (Task 2) reduces `unmapped_events_total` to the bounded `decoder` label at the declaration and both call sites, with a grep guard against a stale 3-arg call (which would panic at runtime). C9 (Task 3) threads `Fetch`'s observed-at into `DecodeOptions.FileTime` with a zero-guard, and a `tickOnce` test proves the observed time reaches the decoder. Each is an independent commit.

**Placeholder scan:** No placeholders. Each task's one verification uncertainty (env-tag names; interface signatures) has an explicit verify-and-adjust instruction.

**Type consistency:** C6 uses `strconv.ParseFloat`/`field.SetFloat` (reflect). C7's metric stays a `*CounterVec`, now 1 label; call sites pass 1 arg. C9's fakes implement `atspmsources.Source` (`Kind`, `Fetch(ctx) (io.ReadCloser, time.Time, error)`) and `atspmdecoders.Decoder` (`Kind`, `Decode(ctx, io.Reader, DecodeOptions) ([]Event, error)`); the `Runner` fields match `internal/atspm/ingest/ingest.go`.
