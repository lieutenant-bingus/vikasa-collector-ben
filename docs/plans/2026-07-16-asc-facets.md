# ASC Facets (FaultSet + DetectorSamples) Implementation Plan


**Goal:** Add the two remaining signal-controller facets — `FaultSet` and `DetectorSamples` — to the `ntcip-asc` adapter, with their synth differs.

**Architecture:** Facets carry what the device said; differs carry the arithmetic. `FaultSet` is diffed as a sorted set-difference on fault ID. `DetectorSamples` carries the raw cumulative counter and the differ turns consecutive values into an interval delta. Three facets on one snapshot means three independent failure domains: a dead alarm OID fails `FaultSet` alone. No openits-models dependency — new facets are a `sdk/model` + `internal/synth` + adapter problem, and the wire mapping is Plan 2's.

**Tech Stack:** Go 1.26, gosnmp, nats.go (unchanged). No new dependencies.

**Spec:** `docs/specs/2026-07-16-asc-facets-design.md`

## Global Constraints

- Branch from `main`. Conventional commits, **no Co-Authored-By trailers**.
- **NEVER bypass commit signing.** This repo signs via 1Password (`commit.gpgsign=true`). If `git commit` fails with a signing error, do NOT retry with `--no-gpg-sign` / `-c commit.gpgsign=false` and do NOT change git config — stop and report BLOCKED. Verify after committing: `git cat-file commit HEAD | grep -c '^gpgsig'` must print 1. (`git log --show-signature` errors locally due to a missing allowedSignersFile — that is expected, not a failure.)
- Module path `github.com/Vikasa2M/vikasa-collector`. Go 1.26.
- **The iron rule:** absence of evidence is never a state change. A facet the adapter failed to read goes in `snap.Errors` and its differ must emit nothing — never a clear, never a zeroed report.
- **Determinism:** anything goldened or hashed iterates sorted. Fault raise/clear order and detector report readings are both sorted; this is a regression guard on a real gen-1 bug, not a style preference.
- All times UTC; tests use fixed timestamps.
- No openits-models import (CI-enforced by `scripts/lint-boundary.sh`, which runs in `make check` along with its own selftest).
- **Every new guard must be shown to fail.** For each validation and invariant, write a test proving it rejects — then verify by deliberately breaking the thing it guards and watching the test fail. Twice this session a check that passed turned out to be incapable of failing.
- Run `make check` before every commit claim.

## File Structure

**New:**
- `sdk/model/fault.go` — `Fault`, `FaultSet`, `KindFaultSet`
- `sdk/model/detector.go` — `DetectorSample`, `DetectorSamples`, `KindDetectorSamples`
- `sdk/model/fault_test.go`, `sdk/model/detector_test.go`
- `internal/synth/fault.go` + `fault_test.go` — the fault differ
- `internal/synth/detector.go` + `detector_test.go` — the detector differ

**Modified:**
- `sdk/model/enums.go` — add `FaultSeverity`, `FaultCategory`
- `sdk/model/events.go` — add `FaultRaised`, `FaultCleared`, `DetectorReading`, `DetectorReport`
- `sdk/model/events_test.go` — extend the kind/accessor table
- `internal/vendors/ntcip/asc.go` + `asc_test.go` — alarm bitmap and detector table
- `internal/app/app.go` — register the two new differs

---

### Task 1: `sdk/model` — fault types and enums

**Files:**
- Create: `sdk/model/fault.go`, `sdk/model/fault_test.go`
- Modify: `sdk/model/enums.go`

**Interfaces:**
- Consumes: `Kind`, `Facet` (existing).
- Produces:
  - `type FaultSeverity uint8` with `SeverityInfo, SeverityWarning, SeverityMinor, SeverityMajor, SeverityCritical` and `String() string`
  - `type FaultCategory uint8` with `CategoryUnknown, CategoryConflict, CategoryCabinet, CategoryPower, CategoryCommunication, CategoryDetector, CategoryLamp` and `String() string`
  - `const KindFaultSet Kind = "fault-set"`
  - `type Fault struct { ID string; Severity FaultSeverity; Category FaultCategory; Description string }`
  - `type FaultSet struct{ Faults []Fault }` implementing `Facet`

- [ ] **Step 1: Write the failing test**

`sdk/model/fault_test.go`:
```go
package model

import "testing"

func TestFaultSetIsAFacet(t *testing.T) {
	var f Facet = FaultSet{Faults: []Fault{{
		ID: "mmu-fault", Severity: SeverityCritical, Category: CategoryConflict,
		Description: "MMU fault detected",
	}}}
	if f.FacetKind() != KindFaultSet {
		t.Fatalf("FacetKind() = %q, want %q", f.FacetKind(), KindFaultSet)
	}
	if got := f.(FaultSet).Faults[0].ID; got != "mmu-fault" {
		t.Fatalf("ID = %q", got)
	}
}

// Severity order mirrors the catalog's FAULT_SEVERITY_* (INFO=0..CRITICAL=4)
// so the Plan 2 mapping is a straight table. The type stays collector-owned:
// upstream renumbering must not move it.
func TestFaultSeverityString(t *testing.T) {
	cases := map[FaultSeverity]string{
		SeverityInfo: "info", SeverityWarning: "warning", SeverityMinor: "minor",
		SeverityMajor: "major", SeverityCritical: "critical",
		FaultSeverity(99): "unknown",
	}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", sev, got, want)
		}
	}
	if SeverityInfo != 0 || SeverityCritical != 4 {
		t.Errorf("severity order must mirror the catalog: info=%d critical=%d", SeverityInfo, SeverityCritical)
	}
}

func TestFaultCategoryString(t *testing.T) {
	cases := map[FaultCategory]string{
		CategoryUnknown: "unknown", CategoryConflict: "conflict",
		CategoryCabinet: "cabinet", CategoryPower: "power",
		CategoryCommunication: "communication", CategoryDetector: "detector",
		CategoryLamp: "lamp", FaultCategory(99): "unknown",
	}
	for cat, want := range cases {
		if got := cat.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", cat, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/`
Expected: FAIL — build error, `FaultSet`/`SeverityInfo`/`CategoryConflict` undefined.

- [ ] **Step 3: Implement**

`sdk/model/fault.go`:
```go
package model

// KindFaultSet is the set of faults currently raised on a device.
const KindFaultSet Kind = "fault-set"

// Fault is one raised fault. ID is stable and human-readable ("mmu-fault"),
// not a bit position: it is the identity synth diffs on, and the value the
// wire's fault_id carries.
type Fault struct {
	ID          string
	Severity    FaultSeverity
	Category    FaultCategory
	Description string
}

// FaultSet is every fault raised on the device at one poll. The differ takes
// the set-difference against the previous poll, so this carries no timing:
// a raise event's OccurredAt is the first observation.
type FaultSet struct{ Faults []Fault }

func (FaultSet) FacetKind() Kind { return KindFaultSet }
```

Append to `sdk/model/enums.go`:
```go
// FaultSeverity is the collector-owned severity enum. Its order mirrors the
// catalog's FAULT_SEVERITY_* (INFO=0..CRITICAL=4) so the wire mapping is a
// straight table — but the type is ours and does not move when upstream
// renumbers, which is the whole point of ADR 0002.
type FaultSeverity uint8

const (
	SeverityInfo FaultSeverity = iota
	SeverityWarning
	SeverityMinor
	SeverityMajor
	SeverityCritical
)

func (s FaultSeverity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityMinor:
		return "minor"
	case SeverityMajor:
		return "major"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// FaultCategory groups faults by cause. The catalog has NO category on its
// fault events — it folded category into a free-form `kind` string, keeping
// Category only on the state Fault message. We keep it anyway: the domain
// model may be richer than any wire version, and mapping-or-dropping it is a
// single emitter's decision (ADR 0002) rather than a collection blocker.
type FaultCategory uint8

const (
	CategoryUnknown FaultCategory = iota
	CategoryConflict
	CategoryCabinet
	CategoryPower
	CategoryCommunication
	CategoryDetector
	CategoryLamp
)

func (c FaultCategory) String() string {
	switch c {
	case CategoryConflict:
		return "conflict"
	case CategoryCabinet:
		return "cabinet"
	case CategoryPower:
		return "power"
	case CategoryCommunication:
		return "communication"
	case CategoryDetector:
		return "detector"
	case CategoryLamp:
		return "lamp"
	default:
		return "unknown"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/ -v -run 'Fault'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): fault domain types — Fault, FaultSet, severity, category

FaultCategory is kept although the catalog has no category on its fault
events (it folded category into a free-form kind string, keeping Category
only on the state message). The domain model may be richer than any wire
version; mapping-or-dropping is one emitter's decision, not a collection
blocker."
```

---

### Task 2: `sdk/model` — detector types

**Files:**
- Create: `sdk/model/detector.go`, `sdk/model/detector_test.go`

**Interfaces:**
- Consumes: `Kind`, `Facet` (existing).
- Produces:
  - `const KindDetectorSamples Kind = "detector-samples"`
  - `type DetectorSample struct { Channel uint32; VolumeCount uint32; OccupancyTenths uint16 }`
  - `type DetectorSamples struct{ Samples []DetectorSample }` implementing `Facet`

- [ ] **Step 1: Write the failing test**

`sdk/model/detector_test.go`:
```go
package model

import "testing"

func TestDetectorSamplesIsAFacet(t *testing.T) {
	var f Facet = DetectorSamples{Samples: []DetectorSample{
		{Channel: 1, VolumeCount: 1234, OccupancyTenths: 125},
	}}
	if f.FacetKind() != KindDetectorSamples {
		t.Fatalf("FacetKind() = %q, want %q", f.FacetKind(), KindDetectorSamples)
	}
	s := f.(DetectorSamples).Samples[0]
	// VolumeCount is the RAW cumulative counter as the controller reported it.
	// The differ turns consecutive values into an interval delta; keeping the
	// raw value here leaves the adapter memoryless.
	if s.VolumeCount != 1234 || s.OccupancyTenths != 125 {
		t.Fatalf("sample = %+v", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/ -run Detector`
Expected: FAIL — `DetectorSamples` undefined.

- [ ] **Step 3: Implement**

`sdk/model/detector.go`:
```go
package model

// KindDetectorSamples is per-channel vehicle detector data at one poll.
const KindDetectorSamples Kind = "detector-samples"

// DetectorSample is one detector channel as read.
//
// VolumeCount is the RAW cumulative counter the controller reported, not an
// interval count: the differ turns consecutive values into a delta, which
// leaves the adapter memoryless.
//
// OccupancyTenths is 0..1000. NTCIP reports occupancy in half-percent
// (0..200) and the catalog wants percent (0..100); tenths represents
// half-percent exactly (0.5% = 5), so the domain stays lossless and the
// emitter rounds to the wire's coarser precision.
type DetectorSample struct {
	Channel         uint32 // NTCIP channel number == table row, 1-based
	VolumeCount     uint32
	OccupancyTenths uint16
}

// DetectorSamples holds one sample per answering channel, sorted by Channel.
// A controller with no detectors yields an EMPTY facet, not an error — that
// is a normal deployment, and a FacetError would be a permanent false alarm.
type DetectorSamples struct{ Samples []DetectorSample }

func (DetectorSamples) FacetKind() Kind { return KindDetectorSamples }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/ -run Detector -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): detector domain types — DetectorSample, DetectorSamples

VolumeCount is the raw cumulative counter; the differ deltas it, which
keeps the adapter memoryless. Occupancy is tenths (0..1000) because NTCIP
gives half-percent and the catalog wants percent — tenths represents
half-percent exactly, so the domain stays lossless."
```

---

### Task 3: `sdk/model` — domain events

**Files:**
- Modify: `sdk/model/events.go`, `sdk/model/events_test.go`

**Interfaces:**
- Consumes: `Base`, `Event`, `FaultSeverity`, `FaultCategory` (Task 1).
- Produces:
  - `FaultRaised{Base; FaultID string; Severity FaultSeverity; Category FaultCategory; Description string}` → kind `"fault-raised"`
  - `FaultCleared{Base; FaultID string}` → kind `"fault-cleared"`
  - `DetectorReading{Channel uint32; VolumeDelta uint32; OccupancyTenths uint16}`
  - `DetectorReport{Base; IntervalStart time.Time; IntervalDuration time.Duration; Readings []DetectorReading}` → kind `"detector-report"`

- [ ] **Step 1: Write the failing test**

Append to `sdk/model/events_test.go`:
```go
// Event kinds match the catalog's ce-type event tokens (fault-raised,
// fault-cleared, detector-report) so Plan 2's mapping stays mechanical.
func TestFacetEventKindsAndAccessors(t *testing.T) {
	at := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "asc-1", OccurredAt: at}

	cases := []struct {
		ev   Event
		kind string
	}{
		{FaultRaised{Base: b, FaultID: "mmu-fault", Severity: SeverityCritical,
			Category: CategoryConflict, Description: "MMU fault detected"}, "fault-raised"},
		{FaultCleared{Base: b, FaultID: "mmu-fault"}, "fault-cleared"},
		{DetectorReport{Base: b, IntervalStart: at.Add(-time.Second), IntervalDuration: time.Second,
			Readings: []DetectorReading{{Channel: 1, VolumeDelta: 3, OccupancyTenths: 125}}}, "detector-report"},
	}
	for _, c := range cases {
		if got := c.ev.EventKind(); got != c.kind {
			t.Errorf("EventKind() = %q, want %q", got, c.kind)
		}
		if got := c.ev.EventOccurredAt(); !got.Equal(at) {
			t.Errorf("%s: OccurredAt = %v, want %v", c.kind, got, at)
		}
		if got := c.ev.EventDeviceID(); got != "asc-1" {
			t.Errorf("%s: DeviceID = %q", c.kind, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/ -run FacetEvent`
Expected: FAIL — `FaultRaised` undefined.

- [ ] **Step 3: Implement**

Append to `sdk/model/events.go`:
```go
// FaultRaised fires when a fault appears that was not raised on the previous
// poll. Its OccurredAt IS the first observation — the facet carries no
// timestamp of its own, because the adapter has no memory to know one.
type FaultRaised struct {
	Base
	FaultID     string
	Severity    FaultSeverity
	Category    FaultCategory
	Description string
}

func (FaultRaised) EventKind() string { return "fault-raised" }

// FaultCleared fires when a previously raised fault is absent from a
// SUCCESSFUL poll. A failed read never produces this: synth suspends a facet
// it could not read, so absence of evidence is never a clear.
type FaultCleared struct {
	Base
	FaultID string
}

func (FaultCleared) EventKind() string { return "fault-cleared" }

// DetectorReading is one channel's contribution to a report. VolumeDelta is
// the count over the report's interval, not a cumulative total.
type DetectorReading struct {
	Channel         uint32
	VolumeDelta     uint32
	OccupancyTenths uint16
}

// DetectorReport is the per-interval detector summary, emitted every poll
// after the first. There is no report on the first poll: with no previous
// sample there is no interval to attribute counts to.
type DetectorReport struct {
	Base
	IntervalStart   time.Time
	IntervalDuration time.Duration
	Readings        []DetectorReading // sorted by Channel
}

func (DetectorReport) EventKind() string { return "detector-report" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/ -v`
Expected: PASS (all model tests, old and new).

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): fault and detector domain events

Event kinds match the catalog's ce-type event tokens so Plan 2's mapping
stays mechanical. FaultRaised.OccurredAt is the first observation: the
facet carries no timestamp because the adapter has no memory to know one."
```

---

### Task 4: `internal/synth` — fault differ

**Files:**
- Create: `internal/synth/fault.go`, `internal/synth/fault_test.go`

**Interfaces:**
- Consumes: `Differ` (existing), `model.FaultSet`, `model.FaultRaised`, `model.FaultCleared`.
- Produces: `func NewFaultDiffer() Differ`

- [ ] **Step 1: Write the failing test**

`internal/synth/fault_test.go`:
```go
package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func faultSnap(at time.Time, faults ...model.Fault) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at,
		Facets: []model.Facet{model.FaultSet{Faults: faults}}}
}

var (
	mmu      = model.Fault{ID: "mmu-fault", Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "MMU fault detected"}
	door     = model.Fault{ID: "cabinet-door", Severity: model.SeverityMinor, Category: model.CategoryCabinet, Description: "Cabinet door open"}
	lampOut  = model.Fault{ID: "lamp-out", Severity: model.SeverityMajor, Category: model.CategoryLamp, Description: "Signal head lamp failure"}
	powerOut = model.Fault{ID: "power-loss", Severity: model.SeverityMajor, Category: model.CategoryPower, Description: "Power loss / generator running"}
)

func TestFirstPollRaisesEverythingCurrentlyRaised(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	evs := e.Apply(faultSnap(t0, mmu, door))
	if len(evs) != 2 {
		t.Fatalf("first poll events = %v, want 2 raises", kinds(evs))
	}
	for _, ev := range evs {
		if ev.EventKind() != "fault-raised" {
			t.Errorf("unexpected %q", ev.EventKind())
		}
	}
}

func TestRaiseAndClear(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu))

	// door appears, mmu persists -> one raise, no clear.
	evs := e.Apply(faultSnap(t0.Add(time.Second), mmu, door))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 raise", kinds(evs))
	}
	r, ok := evs[0].(model.FaultRaised)
	if !ok || r.FaultID != "cabinet-door" || r.Severity != model.SeverityMinor {
		t.Fatalf("bad raise: %+v", evs[0])
	}

	// mmu disappears -> one clear.
	evs = e.Apply(faultSnap(t0.Add(2*time.Second), door))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 clear", kinds(evs))
	}
	c, ok := evs[0].(model.FaultCleared)
	if !ok || c.FaultID != "mmu-fault" {
		t.Fatalf("bad clear: %+v", evs[0])
	}
}

func TestUnchangedFaultSetEmitsNothing(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu, door))
	if evs := e.Apply(faultSnap(t0.Add(time.Second), mmu, door)); len(evs) != 0 {
		t.Fatalf("unchanged set must emit nothing, got %v", kinds(evs))
	}
}

// A fault whose attributes change while its ID stays raised is not a raise.
// Nothing consumes an "amended fault" event; inventing one is YAGNI.
func TestAttributeChangeIsNotARaise(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu))
	worse := mmu
	worse.Description = "MMU fault detected (escalated)"
	if evs := e.Apply(faultSnap(t0.Add(time.Second), worse)); len(evs) != 0 {
		t.Fatalf("attribute change must emit nothing, got %v", kinds(evs))
	}
}

// Determinism: gen-1 iterated a Go map here, so its raise/clear order was
// nondeterministic. This is a regression guard, not a style preference.
func TestEventOrderIsSortedByFaultID(t *testing.T) {
	for i := 0; i < 20; i++ { // map iteration order varies per run
		e := NewEngine(NewFaultDiffer())
		evs := e.Apply(faultSnap(t0, lampOut, mmu, door, powerOut))
		var ids []string
		for _, ev := range evs {
			ids = append(ids, ev.(model.FaultRaised).FaultID)
		}
		want := []string{"cabinet-door", "lamp-out", "mmu-fault", "power-loss"}
		if len(ids) != len(want) {
			t.Fatalf("ids = %v", ids)
		}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("raise order = %v, want sorted %v", ids, want)
			}
		}
	}
}

func TestClearOrderIsSortedByFaultID(t *testing.T) {
	for i := 0; i < 20; i++ {
		e := NewEngine(NewFaultDiffer())
		e.Apply(faultSnap(t0, lampOut, mmu, door, powerOut))
		evs := e.Apply(faultSnap(t0.Add(time.Second)))
		var ids []string
		for _, ev := range evs {
			ids = append(ids, ev.(model.FaultCleared).FaultID)
		}
		want := []string{"cabinet-door", "lamp-out", "mmu-fault", "power-loss"}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("clear order = %v, want sorted %v", ids, want)
			}
		}
	}
}

// THE IRON RULE. Gen-1's failure here was severe: on any SNMP error it
// returned a reading containing only a synthetic snmp-unreachable fault, and
// because diffFaults was a pure set-difference that CLEARED EVERY REAL FAULT,
// then re-raised them on recovery. A failed read must never clear a fault.
func TestFailedFaultReadNeverClears(t *testing.T) {
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu, door))

	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindFaultSet, Err: "alarm OID unanswered"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}

	// Recovery with the same faults still raised: no re-raise, because prev
	// survived the failed poll.
	if evs := e.Apply(faultSnap(t0.Add(2*time.Second), mmu, door)); len(evs) != 0 {
		t.Fatalf("post-recovery events = %v, want none", kinds(evs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/synth/ -run Fault`
Expected: FAIL — `NewFaultDiffer` undefined.

- [ ] **Step 3: Implement**

`internal/synth/fault.go`:
```go
package synth

import (
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewFaultDiffer diffs the fault-set facet: a raise for each fault that
// appeared, a clear for each that vanished.
func NewFaultDiffer() Differ { return faultDiffer{} }

type faultDiffer struct{}

func (faultDiffer) Kind() model.Kind { return model.KindFaultSet }

func (faultDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	currFaults := index(curr.(model.FaultSet))
	var prevFaults map[string]model.Fault
	if prev != nil {
		prevFaults = index(prev.(model.FaultSet))
	}

	// Sorted iteration: gen-1 ranged over a map here, making event order
	// nondeterministic. Determinism is a construction-time requirement.
	var events []model.Event
	for _, id := range sortedIDs(currFaults) {
		if _, existed := prevFaults[id]; existed {
			continue // still raised; an attribute change is not a raise
		}
		f := currFaults[id]
		events = append(events, model.FaultRaised{
			Base: base, FaultID: f.ID, Severity: f.Severity,
			Category: f.Category, Description: f.Description,
		})
	}
	for _, id := range sortedIDs(prevFaults) {
		if _, still := currFaults[id]; still {
			continue
		}
		events = append(events, model.FaultCleared{Base: base, FaultID: id})
	}
	return events
}

func index(fs model.FaultSet) map[string]model.Fault {
	out := make(map[string]model.Fault, len(fs.Faults))
	for _, f := range fs.Faults {
		out[f.ID] = f
	}
	return out
}

func sortedIDs(m map[string]model.Fault) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/synth/ -v -run Fault -count=3`
Expected: PASS. `-count=3` because the determinism tests depend on Go's randomized map iteration; a single run can pass by luck.

- [ ] **Step 5: Commit**

```bash
git add internal/synth
git commit -m "feat(core): fault differ — sorted set-difference on fault id

Raise/clear iterate in sorted order: gen-1 ranged over a Go map here, so
its event order was nondeterministic. A regression test runs the diff
repeatedly because map iteration order varies per run.

A failed read cannot clear a fault — synth.Engine already suspends a facet
listed in snap.Errors. That is gen-1's worst bug in this area prevented
structurally: its synthetic snmp-unreachable reading cleared every real
fault and re-raised them on recovery."
```

---

### Task 5: `internal/synth` — detector differ

**Files:**
- Create: `internal/synth/detector.go`, `internal/synth/detector_test.go`

**Interfaces:**
- Consumes: `Differ`, `model.DetectorSamples`, `model.DetectorReport`, `model.DetectorReading`.
- Produces: `func NewDetectorDiffer() Differ`

- [ ] **Step 1: Write the failing test**

`internal/synth/detector_test.go`:
```go
package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func detSnap(at time.Time, samples ...model.DetectorSample) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at,
		Facets: []model.Facet{model.DetectorSamples{Samples: samples}}}
}

// No previous sample means no interval to attribute counts to. Gen-1 reported
// the raw cumulative counter here, producing a large bogus spike on every
// collector restart.
func TestFirstPollEmitsNoReport(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	evs := e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000, OccupancyTenths: 100}))
	if len(evs) != 0 {
		t.Fatalf("first poll must emit nothing, got %v", kinds(evs))
	}
}

func TestSecondPollReportsDelta(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000, OccupancyTenths: 100}))

	t1 := t0.Add(10 * time.Second)
	evs := e.Apply(detSnap(t1, model.DetectorSample{Channel: 1, VolumeCount: 5007, OccupancyTenths: 125}))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 report", kinds(evs))
	}
	r := evs[0].(model.DetectorReport)
	if !r.IntervalStart.Equal(t0) || r.IntervalDuration != 10*time.Second {
		t.Fatalf("interval = %v +%ds, want %v +10s", r.IntervalStart, r.IntervalDuration, t0)
	}
	if len(r.Readings) != 1 {
		t.Fatalf("readings = %+v", r.Readings)
	}
	got := r.Readings[0]
	// Volume is delta'd; occupancy is NOT — it is a fraction, not a counter.
	if got.VolumeDelta != 7 || got.OccupancyTenths != 125 {
		t.Fatalf("reading = %+v, want delta 7 occ 125", got)
	}
}

// At vehicle volumes a genuine Counter32 wrap is ~136 years away, so a
// decrease means the controller reset. Reporting the current value is correct
// for a reset.
func TestCounterDecreaseIsTreatedAsReset(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 5000}))
	evs := e.Apply(detSnap(t0.Add(time.Second), model.DetectorSample{Channel: 1, VolumeCount: 3}))
	r := evs[0].(model.DetectorReport)
	if r.Readings[0].VolumeDelta != 3 {
		t.Fatalf("after reset VolumeDelta = %d, want 3 (the current value)", r.Readings[0].VolumeDelta)
	}
}

// A channel with no previous sample has no interval basis — same rule as the
// first poll, applied per channel. It appears in the next report.
func TestNewChannelIsOmittedForOnePoll(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 100}))

	evs := e.Apply(detSnap(t0.Add(time.Second),
		model.DetectorSample{Channel: 1, VolumeCount: 105},
		model.DetectorSample{Channel: 2, VolumeCount: 900}))
	r := evs[0].(model.DetectorReport)
	if len(r.Readings) != 1 || r.Readings[0].Channel != 1 {
		t.Fatalf("readings = %+v, want channel 1 only", r.Readings)
	}

	// Next poll, channel 2 has a basis and appears.
	evs = e.Apply(detSnap(t0.Add(2*time.Second),
		model.DetectorSample{Channel: 1, VolumeCount: 108},
		model.DetectorSample{Channel: 2, VolumeCount: 904}))
	r = evs[0].(model.DetectorReport)
	if len(r.Readings) != 2 || r.Readings[1].Channel != 2 || r.Readings[1].VolumeDelta != 4 {
		t.Fatalf("readings = %+v, want ch1 and ch2(delta 4)", r.Readings)
	}
}

func TestReadingsAreSortedByChannel(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	mk := func(at time.Time, base uint32) *model.Snapshot {
		return detSnap(at,
			model.DetectorSample{Channel: 7, VolumeCount: base},
			model.DetectorSample{Channel: 2, VolumeCount: base},
			model.DetectorSample{Channel: 4, VolumeCount: base})
	}
	e.Apply(mk(t0, 10))
	r := e.Apply(mk(t0.Add(time.Second), 20))[0].(model.DetectorReport)
	want := []uint32{2, 4, 7}
	for i, w := range want {
		if r.Readings[i].Channel != w {
			t.Fatalf("channel order = %+v, want sorted %v", r.Readings, want)
		}
	}
}

// An empty facet is normal (a controller with no detectors), and must not
// produce a report full of nothing.
func TestEmptyFacetEmitsNoReport(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0))
	if evs := e.Apply(detSnap(t0.Add(time.Second))); len(evs) != 0 {
		t.Fatalf("empty facet must emit nothing, got %v", kinds(evs))
	}
}

// The iron rule, for this facet.
func TestFailedDetectorReadEmitsNothing(t *testing.T) {
	e := NewEngine(NewDetectorDiffer())
	e.Apply(detSnap(t0, model.DetectorSample{Channel: 1, VolumeCount: 100}))
	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindDetectorSamples, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}
	// Recovery deltas against the pre-failure sample, which survived.
	evs := e.Apply(detSnap(t0.Add(2*time.Second), model.DetectorSample{Channel: 1, VolumeCount: 106}))
	r := evs[0].(model.DetectorReport)
	if r.Readings[0].VolumeDelta != 6 {
		t.Fatalf("VolumeDelta = %d, want 6 (delta vs the surviving sample)", r.Readings[0].VolumeDelta)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/synth/ -run Detector`
Expected: FAIL — `NewDetectorDiffer` undefined.

- [ ] **Step 3: Implement**

**Design note you need before writing this — read it first.** `Differ.Diff(prev, curr model.Facet, base model.Base)` receives only the two *facets* and the base. It does **not** receive the previous snapshot's `SampledAt`, so the differ cannot derive `IntervalStart` from its arguments. This differ therefore keeps the previous sample time itself, keyed by device. That makes it the first stateful differ in the package — it must be a pointer receiver, and it must hold its own mutex rather than trusting that `synth.Engine` serializes `Apply` (it does today; a differ relying on a caller's locking is a trap for the next engine change).

`internal/synth/detector.go`:
```go
package synth

import (
	"sort"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewDetectorDiffer turns consecutive raw counter samples into per-interval
// reports.
//
// Unlike the other differs this one is stateful: Diff is not handed the
// previous snapshot's SampledAt, so it remembers the last sample time per
// device to bound the report interval.
func NewDetectorDiffer() Differ {
	return &detectorDiffer{last: make(map[string]time.Time)}
}

type detectorDiffer struct {
	mu   sync.Mutex
	last map[string]time.Time // deviceID -> previous SampledAt
}

func (d *detectorDiffer) Kind() model.Kind { return model.KindDetectorSamples }

func (d *detectorDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	// Record this poll's time and recover the previous one. Engine serializes
	// Apply today, but a differ must not depend on its caller's locking.
	d.mu.Lock()
	start, hadPrev := d.last[base.DeviceID]
	d.last[base.DeviceID] = base.OccurredAt
	d.mu.Unlock()

	// No previous sample: no interval to attribute counts to. Gen-1 reported
	// the raw cumulative counter here, spiking on every restart.
	if !hadPrev || prev == nil {
		return nil
	}

	prevByCh := byChannel(prev.(model.DetectorSamples))
	c := curr.(model.DetectorSamples)

	readings := make([]model.DetectorReading, 0, len(c.Samples))
	for _, s := range c.Samples {
		p, ok := prevByCh[s.Channel]
		if !ok {
			continue // new channel: no basis this poll, appears in the next
		}
		readings = append(readings, model.DetectorReading{
			Channel:         s.Channel,
			VolumeDelta:     delta(p.VolumeCount, s.VolumeCount),
			OccupancyTenths: s.OccupancyTenths, // a fraction, never delta'd
		})
	}
	if len(readings) == 0 {
		return nil
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].Channel < readings[j].Channel })

	return []model.Event{model.DetectorReport{
		Base:            base,
		IntervalStart:   start,
		IntervalDuration: base.OccurredAt.Sub(start),
		Readings:        readings,
	}}
}

// delta handles a counter that went backwards. At vehicle volumes a genuine
// Counter32 wrap is ~136 years out, so a decrease means the controller reset:
// the current value IS the count since that reset.
func delta(prev, curr uint32) uint32 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}

func byChannel(ds model.DetectorSamples) map[uint32]model.DetectorSample {
	out := make(map[uint32]model.DetectorSample, len(ds.Samples))
	for _, s := range ds.Samples {
		out[s.Channel] = s
	}
	return out
}
```

Two things about the time bookkeeping worth understanding before you write it:

**It sits before the early returns on purpose.** On the first poll `Diff` runs, emits nothing, but must still record the time — otherwise the second poll has no basis and nothing would ever report.

**A suspended facet does not advance the clock, and that is correct.** `Engine.Apply` iterates `snap.Facets`; a facet that failed to read is in `snap.Errors` instead, so `Diff` is never called for it. The clock therefore only advances on successful reads, and the first report after a failed poll correctly spans back to the last *successful* sample — a real 2-poll interval, reported as 2 polls' worth of seconds, with the matching delta. `TestFailedDetectorReadEmitsNothing` pins exactly this: 6 vehicles across the recovery span, not a count silently attributed to one poll period.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/synth/ -v -run Detector`
Expected: PASS.
Run: `go test ./internal/synth/ -race -count=2` — Expected: PASS (the differ now holds state).

- [ ] **Step 5: Commit**

```bash
git add internal/synth
git commit -m "feat(core): detector differ — per-interval volume deltas

First poll emits nothing: with no previous sample there is no interval to
attribute counts to. Gen-1 reported the raw cumulative counter here, which
spiked on every collector restart. A channel with no previous sample is
omitted for one poll under the same rule.

A counter decrease is treated as a controller reset rather than a wrap: at
vehicle volumes a genuine Counter32 wrap is ~136 years away. Occupancy is
never delta'd — it is a fraction, not a counter."
```

---

### Task 6: `internal/vendors/ntcip` — alarm bitmap → FaultSet

**Files:**
- Modify: `internal/vendors/ntcip/asc.go`, `internal/vendors/ntcip/asc_test.go`

**Interfaces:**
- Consumes: `model.FaultSet`, `model.Fault`, severity/category enums (Task 1).
- Produces: `asc.Read` additionally returns a `FaultSet` facet or a `FacetError` for it.

**CRITICAL — restructure first.** `asc.Read` currently does an early `return snap, nil` when the operation-status OID is unanswered. That path must NOT skip the other facets: each facet is an independent failure domain. Restructure `Read` into per-facet helpers before adding anything.

- [ ] **Step 1: Write the failing test**

Append to `internal/vendors/ntcip/asc_test.go`:
```go
const oidAlarm = ".1.3.6.1.4.1.1206.4.2.1.5.1.0"

func faultsIn(t *testing.T, snap *model.Snapshot) []model.Fault {
	t.Helper()
	f, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set facet")
	}
	return f.(model.FaultSet).Faults
}

func TestASCNoAlarmBitsMeansEmptyFaultSet(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := faultsIn(t, snap); len(got) != 0 {
		t.Fatalf("no alarm bits must yield an empty fault set, got %+v", got)
	}
}

func TestASCAlarmBitmapDecodeGolden(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	// bits 1 (mmu-fault) and 3 (power-loss), plus bit 12 which is undefined.
	fx[oidAlarm] = (1 << 1) | (1 << 3) | (1 << 12)

	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := faultsIn(t, snap)
	want := []model.Fault{
		{ID: "mmu-fault", Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "MMU fault detected"},
		{ID: "power-loss", Severity: model.SeverityMajor, Category: model.CategoryPower, Description: "Power loss / generator running"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("faults = %+v\nwant     %+v", got, want)
	}
}

func TestASCUnansweredAlarmIsFaultSetFacetError(t *testing.T) {
	// healthyFixture has no alarm OID: the fault facet must be reported failed
	// rather than defaulted to "no faults" — absence of evidence is never a
	// state change, and an empty set would clear every real fault downstream.
	snap, err := NewASC("asc-1", &snmptest.Static{Values: healthyFixture}).Read(context.Background())
	if err != nil {
		t.Fatalf("partial data must not be a hard error: %v", err)
	}
	if _, ok := snap.Facet(model.KindFaultSet); ok {
		t.Fatal("unreadable fault facet must not be present")
	}
	if !snap.FacetFailed(model.KindFaultSet) {
		t.Fatal("expected fault-set FacetError")
	}
	// The other facets are independent and must survive.
	if _, ok := snap.Facet(model.KindSignalStatus); !ok {
		t.Fatal("signal-status must still publish when the alarm OID fails")
	}
}

// Facets are independent failure domains: a dead operation-status OID must not
// suppress the fault set. (Read previously returned early here.)
func TestASCFacetsFailIndependently(t *testing.T) {
	fx := map[string]int64{
		".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3, // pattern only; operation-status absent
		oidAlarm:                        1 << 2, // cabinet-door
	}
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("signal-status should have failed")
	}
	got := faultsIn(t, snap)
	if len(got) != 1 || got[0].ID != "cabinet-door" {
		t.Fatalf("fault set must still publish, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vendors/...`
Expected: FAIL — no fault-set facet is produced; `TestASCFacetsFailIndependently` fails because `Read` returns early.

- [ ] **Step 3: Implement**

In `internal/vendors/ntcip/asc.go`, add to the OID const block:
```go
	oidShortAlarmStatus = ".1.3.6.1.4.1.1206.4.2.1.5.1.0"
```

Add the bitmap table:
```go
// alarmBitmap maps NTCIP 1202 short-alarm bit positions to collector faults.
//
// Carried verbatim from the gen-1 collector, INCLUDING its caveat, which is
// load-bearing rather than decorative:
//
//	Bit positions are conservative; real-world NTCIP 1202 deployments vary
//	per vendor. These are the well-known bits downstream dashboards
//	typically surface.
//
// This table has NEVER been validated against a physical controller. A wrong
// bit emits a confidently-mislabeled fault, and fixtures cannot catch it —
// they encode the same assumption. Validating against hardware is tracked
// follow-up work; per-vendor overlays are deferred until ~3 variant adapters
// exist (architecture spec, rule of three).
var alarmBitmap = []struct {
	bit         uint
	id          string
	severity    model.FaultSeverity
	category    model.FaultCategory
	description string
}{
	{0, "conflict-monitor", model.SeverityCritical, model.CategoryConflict, "Conflict monitor active"},
	{1, "mmu-fault", model.SeverityCritical, model.CategoryConflict, "MMU fault detected"},
	{2, "cabinet-door", model.SeverityMinor, model.CategoryCabinet, "Cabinet door open"},
	{3, "power-loss", model.SeverityMajor, model.CategoryPower, "Power loss / generator running"},
	{4, "low-battery", model.SeverityMajor, model.CategoryPower, "UPS battery low"},
	{5, "comm-loss", model.SeverityMajor, model.CategoryCommunication, "Communication loss"},
	{6, "detector-fault", model.SeverityMinor, model.CategoryDetector, "Detector failure"},
	{7, "lamp-out", model.SeverityMajor, model.CategoryLamp, "Signal head lamp failure"},
}
```

Replace `Read` entirely with a per-facet structure. Note the removed early return — each facet now fails independently:
```go
func (a *asc) Read(ctx context.Context) (*model.Snapshot, error) {
	vals, err := a.client.Get(ctx, []string{
		oidOperationStatus, oidFlashStatus, oidPatternStatus, oidPreemptStatus,
		oidShortAlarmStatus,
	})
	if err != nil {
		// The whole Get failed: the device is unreachable. That is a hard
		// Read error which the runner turns into a health event — not a fault.
		return nil, fmt.Errorf("ntcip-asc %s: %w", a.deviceID, err)
	}
	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}

	// Each facet is an independent failure domain: one unanswered OID must
	// never suppress a facet that WAS readable.
	a.readSignalStatus(snap, vals)
	a.readFaultSet(snap, vals)
	return snap, nil
}

func (a *asc) readSignalStatus(snap *model.Snapshot, vals map[string]int64) {
	op, ok := vals[oidOperationStatus]
	if !ok {
		// Mandatory OID unanswered: report the facet failed rather than
		// fabricating state (absence of evidence is never a state change).
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "operation-status OID unanswered",
		})
		return
	}
	st := model.SignalStatus{
		Mode:            modeFromOperation(op),
		InConflictFlash: vals[oidFlashStatus] == 2,
		ActivePlanID:    uint32(vals[oidPatternStatus]),
	}
	if p := vals[oidPreemptStatus]; p > 0 {
		st.PreemptionActive = true
		st.PreemptionSource = fmt.Sprintf("preempt-%d", p)
	}
	snap.Facets = append(snap.Facets, st)
}

func (a *asc) readFaultSet(snap *model.Snapshot, vals map[string]int64) {
	bits, ok := vals[oidShortAlarmStatus]
	if !ok {
		// Defaulting to "no faults" here would clear every real fault
		// downstream — the exact gen-1 failure this design exists to prevent.
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: "short-alarm-status OID unanswered",
		})
		return
	}
	// Zero bits is an EMPTY fault set, not an error: a healthy controller.
	fs := model.FaultSet{}
	for _, f := range alarmBitmap {
		if bits&(1<<f.bit) == 0 {
			continue
		}
		fs.Faults = append(fs.Faults, model.Fault{
			ID: f.id, Severity: f.severity, Category: f.category, Description: f.description,
		})
	}
	snap.Facets = append(snap.Facets, fs)
}
```

`alarmBitmap` is declared in bit order, so `fs.Faults` comes out deterministically ordered without an explicit sort. Undefined bits (anything above 7) are ignored.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/vendors/... -v`
Expected: PASS — including the pre-existing `TestASCReadGolden`, `TestASCReadPreemptionAndFlash`, `TestASCReadPartialFailureIsFacetError`, `TestASCReadTransportErrorIsHardError`.

Note `TestASCReadPartialFailureIsFacetError` asserts the signal facet is absent on an unanswered operation-status OID; that still holds — only the early return changed, not the facet-error behavior.

- [ ] **Step 5: Commit**

```bash
git add internal/vendors
git commit -m "feat(vendors): ntcip-asc reads the short-alarm bitmap into FaultSet

Read is restructured into per-facet helpers: it previously returned early
when operation-status was unanswered, which would have suppressed every
other facet. Facets are independent failure domains — a dead alarm OID
fails FaultSet alone and signal-status still publishes.

An unanswered alarm OID is a FacetError, never an empty fault set: an
empty set would clear every real fault downstream. Zero bits IS an empty
set — a healthy controller.

The bitmap is carried from gen-1 including its caveat that it was never
validated against a physical controller. Fixtures cannot catch a wrong bit
because they encode the same assumption."
```

---

### Task 7: `internal/vendors/ntcip` — detector table → DetectorSamples

**Files:**
- Modify: `internal/vendors/ntcip/asc.go`, `internal/vendors/ntcip/asc_test.go`

**Interfaces:**
- Consumes: `model.DetectorSamples`, `model.DetectorSample` (Task 2).
- Produces: `asc.Read` additionally returns a `DetectorSamples` facet or a `FacetError` for it.

- [ ] **Step 1: Write the failing test**

Append to `internal/vendors/ntcip/asc_test.go`:
```go
const (
	oidMaxDetectors = ".1.3.6.1.4.1.1206.4.2.1.2.3.0"
	volPrefix       = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.2"
	occPrefix       = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.4"
)

func withDetectors(base map[string]int64, maxCh int64, chans map[int]struct{ vol, occHalfPct int64 }) map[string]int64 {
	fx := map[string]int64{}
	for k, v := range base {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	fx[oidMaxDetectors] = maxCh
	for ch, d := range chans {
		fx[fmt.Sprintf("%s.%d", volPrefix, ch)] = d.vol
		fx[fmt.Sprintf("%s.%d", occPrefix, ch)] = d.occHalfPct
	}
	return fx
}

func samplesIn(t *testing.T, snap *model.Snapshot) []model.DetectorSample {
	t.Helper()
	f, ok := snap.Facet(model.KindDetectorSamples)
	if !ok {
		t.Fatal("missing detector-samples facet")
	}
	return f.(model.DetectorSamples).Samples
}

// NTCIP occupancy is half-percent (0..200); the domain carries tenths
// (0..1000). 40 half-percent = 20% = 200 tenths.
func TestASCDetectorGoldenAndOccupancyConversion(t *testing.T) {
	fx := withDetectors(healthyFixture, 2, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 1234, occHalfPct: 40},
		2: {vol: 99, occHalfPct: 200},
	})
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []model.DetectorSample{
		{Channel: 1, VolumeCount: 1234, OccupancyTenths: 200},
		{Channel: 2, VolumeCount: 99, OccupancyTenths: 1000},
	}
	if got := samplesIn(t, snap); !reflect.DeepEqual(got, want) {
		t.Fatalf("samples = %+v\nwant      %+v", got, want)
	}
}

// A sparse table is normal: absent channels are skipped, not zero-filled.
func TestASCSparseDetectorTable(t *testing.T) {
	fx := withDetectors(healthyFixture, 5, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 10, occHalfPct: 0},
		4: {vol: 40, occHalfPct: 20},
	})
	got := samplesIn(t, mustRead(t, fx))
	if len(got) != 2 || got[0].Channel != 1 || got[1].Channel != 4 {
		t.Fatalf("sparse table = %+v, want channels 1 and 4", got)
	}
}

// A channel answering volume but not occupancy is half-read; skip it rather
// than report a fabricated 0% occupancy.
func TestASCChannelWithoutOccupancyIsSkipped(t *testing.T) {
	fx := withDetectors(healthyFixture, 2, map[int]struct{ vol, occHalfPct int64 }{
		1: {vol: 10, occHalfPct: 20},
	})
	fx[fmt.Sprintf("%s.%d", volPrefix, 2)] = 55 // volume only, no occupancy
	got := samplesIn(t, mustRead(t, fx))
	if len(got) != 1 || got[0].Channel != 1 {
		t.Fatalf("half-read channel must be skipped, got %+v", got)
	}
}

// A controller with no detectors is a normal deployment: empty facet, NOT a
// FacetError, which would be a permanent false alarm.
func TestASCNoDetectorsIsEmptyFacetNotError(t *testing.T) {
	fx := withDetectors(healthyFixture, 0, nil)
	snap := mustRead(t, fx)
	if snap.FacetFailed(model.KindDetectorSamples) {
		t.Fatal("no detectors must not be a FacetError")
	}
	if got := samplesIn(t, snap); len(got) != 0 {
		t.Fatalf("want empty samples, got %+v", got)
	}
}

// An unanswered maxVehicleDetectors OID falls back to 32 channels rather than
// reporting the facet failed: the bound is an optimisation, not the data.
func TestASCMissingMaxDetectorsFallsBackTo32(t *testing.T) {
	fx := map[string]int64{}
	for k, v := range healthyFixture {
		fx[k] = v
	}
	fx[oidAlarm] = 0
	fx[fmt.Sprintf("%s.%d", volPrefix, 30)] = 7
	fx[fmt.Sprintf("%s.%d", occPrefix, 30)] = 2
	got := samplesIn(t, mustRead(t, fx))
	if len(got) != 1 || got[0].Channel != 30 {
		t.Fatalf("want channel 30 within the 32-channel fallback, got %+v", got)
	}
}

func mustRead(t *testing.T, fx map[string]int64) *model.Snapshot {
	t.Helper()
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return snap
}
```

Add `"fmt"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vendors/... -run Detector`
Expected: FAIL — no detector facet produced.

- [ ] **Step 3: Implement**

Add to the OID const block in `asc.go`:
```go
	oidMaxVehicleDetectors = ".1.3.6.1.4.1.1206.4.2.1.2.3.0"
	oidDetectorVolumeCol   = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.2"
	oidDetectorOccupancyCol = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.4"
)

// defaultMaxDetectorChannels bounds the detector table when the controller
// does not answer maxVehicleDetectors.
const defaultMaxDetectorChannels = 32
```

Add `oidMaxVehicleDetectors` to the scalar `Get` list in `Read`, and call the new helper after `readFaultSet`:
```go
	a.readDetectors(ctx, snap, vals)
```

```go
// readDetectors issues a SECOND Get for the detector table. The table is read
// as synthesized indexed OIDs in one batched Get rather than a walk: a GetNext
// walk multiplies round-trips by channel count.
func (a *asc) readDetectors(ctx context.Context, snap *model.Snapshot, scalars map[string]int64) {
	maxCh := int64(defaultMaxDetectorChannels)
	if v, ok := scalars[oidMaxVehicleDetectors]; ok && v > 0 && v < 256 {
		maxCh = v
	} else if ok && v == 0 {
		// The controller answered: it has no detectors. An empty facet, not an
		// error — a FacetError here would be a permanent false alarm.
		snap.Facets = append(snap.Facets, model.DetectorSamples{})
		return
	}

	oids := make([]string, 0, maxCh*2)
	for ch := int64(1); ch <= maxCh; ch++ {
		oids = append(oids,
			fmt.Sprintf("%s.%d", oidDetectorVolumeCol, ch),
			fmt.Sprintf("%s.%d", oidDetectorOccupancyCol, ch))
	}
	vals, err := a.client.Get(ctx, oids)
	if err != nil {
		// The device answered the scalars but not this table: it is reachable,
		// this facet is not readable. Exactly what FacetError means.
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorSamples, Err: "detector table read: " + err.Error(),
		})
		return
	}

	ds := model.DetectorSamples{}
	for ch := int64(1); ch <= maxCh; ch++ {
		vol, volOK := vals[fmt.Sprintf("%s.%d", oidDetectorVolumeCol, ch)]
		occ, occOK := vals[fmt.Sprintf("%s.%d", oidDetectorOccupancyCol, ch)]
		if !volOK || !occOK {
			// Absent (NoSuchInstance) or half-read: skip rather than fabricate.
			continue
		}
		ds.Samples = append(ds.Samples, model.DetectorSample{
			Channel:     uint32(ch),
			VolumeCount: uint32(vol),
			// NTCIP half-percent (0..200) -> tenths (0..1000).
			OccupancyTenths: uint16(occ * 5),
		})
	}
	snap.Facets = append(snap.Facets, ds)
}
```

Channels are appended in ascending order, so `ds.Samples` is sorted by construction.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/vendors/... -v`
Expected: PASS, all tests old and new.
Run: `make check` — Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/vendors
git commit -m "feat(vendors): ntcip-asc reads the detector table into DetectorSamples

The table is read as synthesized indexed OIDs in one batched Get rather
than a walk — a GetNext walk multiplies round-trips by channel count.
Bounds come from maxVehicleDetectors, falling back to 32 channels when
unanswered: the bound is an optimisation, not the data.

No detectors is an empty facet, not a FacetError — a controller without
detectors is a normal deployment and an error there would be a permanent
false alarm. A channel answering volume but not occupancy is skipped
rather than reported with a fabricated 0%. Occupancy converts from NTCIP
half-percent to the domain's tenths."
```

---

### Task 8: `internal/app` — register the differs

**Files:**
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: `synth.NewFaultDiffer` (Task 4), `synth.NewDetectorDiffer` (Task 5).
- Produces: no new API. The engine gains two differs.

- [ ] **Step 1: Make the change**

In `internal/app/app.go`, replace the engine construction:
```go
	engine := synth.NewEngine(
		synth.NewSignalDiffer(),
		synth.NewFaultDiffer(),
		synth.NewDetectorDiffer(),
	)
```

- [ ] **Step 2: Verify the whole suite**

Run: `make check` — Expected: green.
Run: `go test ./... -race -count=1` — Expected: all pass.
Run: `go test ./internal/app/ -count=2` — Expected: PASS.

The existing end-to-end test still asserts only health events reach JetStream, which remains correct: with no openits-models emitter wired, the new fault and detector events hit the loud-drop path exactly as `SignalStatus`'s events already do. That is Plan 2's job, not a defect.

- [ ] **Step 3: Confirm the loud drop is real, not silent**

Run: `go test ./internal/app/ -run TestEndToEnd -v 2>&1 | grep -c "event dropped"`
Expected: a non-zero count — the fixture adapter only produces `SignalStatus`, so this proves the drop path logs rather than silently discarding. If it prints 0, the drop is silent and that is a defect worth reporting.

- [ ] **Step 4: Commit**

```bash
git add internal/app
git commit -m "feat(core): register the fault and detector differs

The new domain events have no emitter until Plan 2, so they take the
loud-drop path exactly as signal-status events already do — logged and
counted, never silently discarded."
```

---

## Final verification

```bash
make check                       # vet, tests, boundary lint + selftest
go test ./... -race              # concurrency (the detector differ holds state)
go test ./internal/synth/ -count=5   # determinism tests vs randomized map order
go build ./... && go run ./cmd/collector -version
```

Confirm the boundary rule still holds — this plan must add no openits-models dependency:

```bash
go list -deps ./... | grep -c openits-models    # expect 0
```

## Follow-on (not in this plan)

- **Validate the alarm bitmap against a physical controller.** The table is gen-1's unverified guess; a wrong bit emits a confidently-mislabeled fault and no fixture can catch it.
- **RSU:** `RSUBroadcastCounters` + `ntcip-rsu` — separate spec. The catalog has no broadcast-counter event, only `rsu-broadcast-sample.v1` carrying `rate_hz` over a window, so that facet defines a shape rather than matching one.
- **Plan 2 — `wire/openitsv1`:** blocked on openits-models settling its module path (`Vikasa2M/openits-models` vs the declared `openits/openits-models`) and cutting a tag. Note for that work: `TestCETypesMatchesWhatTheEmitterActuallyEmits` derives from a hand-maintained `samples` list, which is adequate at 2 ce-types and not at ~20.
