# P1a — Golden Characterization Harness Implementation Plan


**Goal:** Pin the current SNMP poll-pipeline transform (Translator → `driver.Reading` → `synth.Diff` → events) with characterization tests, so the P1b–P1d refactors can prove they changed nothing.

**Architecture:** White-box tests in the existing packages that drive each stage with fixed, canned inputs and assert the meaningful output fields. Time is held fixed by constructing `driver.Reading` values with a fixed `SampledAt` (rather than routing through `time.Now()`), so assertions are deterministic without any production clock-seam change.

**Tech Stack:** Go 1.26, stdlib `testing`, `github.com/gosnmp/gosnmp` (fake SNMP client).

## P1 decomposition (context — this plan is P1a only)

P1 (core extraction, per `docs/specs/2026-07-10-vendor-adapter-architecture-design.md` §8) is split into four independently-shippable sub-plans:

- **P1a (this plan)** — golden/characterization harness for the poll transform. Safety net; no production code changes.
- **P1b** — collapse the ~75 envelope constructors into one generic `emit.New` (+ shims); its own subject/ce-id goldens.
- **P1c** — `adapter/` interfaces + registry + `core/reading`; wrap the SNMP translators as `ntcip/asc` + `ntcip/rsu` `SnapshotReader` adapters.
- **P1d** — config schema (`vendor`/`device_kind`/`connection`) + compat shim + inventory mapping + the unified `core/schedule` driving loop; also folds in the P1 guard-completeness follow-ups (inventory pollLoops, ratelimit cleanup, scheduler supervisor).

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- **No production code changes in P1a** — test files only. If a test cannot be written without a production change, STOP and report it (that is a P1b+ design input, not a P1a edit).
- Tests are white-box (same package as the code under test) so they can reference unexported OID constants.
- Assert meaningful fields, not timestamp values. `driver.Reading.SampledAt` and `Fault.FirstObserved` are set from `time.Now()` inside `TranslateRaw`; do not assert their exact values — assert they are non-zero where relevant, and assert everything else exactly.
- Commit after each task; `gofmt -s -w .` before committing.
- This is the safety net for later refactors — the assertions must be specific enough that a behavior change fails them.

---

### Task 1: Characterize the signal-control SNMP translator

**Files:**
- Test: `internal/translator/signalcontrol/translator_test.go` (create)

**Interfaces:**
- Consumes: `signalcontrol.NewTranslator(deviceID string) *Translator`; `(*Translator).TranslateRaw(ctx, client snmp.SNMPClient) (*driver.Reading, error)`; the unexported OID constants in `translator.go` (`oidOperationStatus`, `oidFlashStatus`, `oidPatternStatus`, `oidPreemptStatus`, `oidShortAlarmStatus`, `oidMaxVehicleDetectors`, `oidVehicleDetectorVolumePrefix`, `oidVehicleDetectorOccupancyPrefix`, `oidVehicleDetectorStatusPrefix`).
- `snmp.SNMPClient` interface methods to stub: `Get(ctx, oids []string) (*gosnmp.SnmpPacket, error)`, `GetBulk(ctx, oids []string, nonRepeaters uint8, maxRepetitions uint32) (*gosnmp.SnmpPacket, error)`, `Set(ctx, pdus []gosnmp.SnmpPDU) (*gosnmp.SnmpPacket, error)`, `Walk(ctx, oid string, handler gosnmp.WalkFunc) error`, `Close() error`.

- [ ] **Step 1: Write the characterization test.** Create `internal/translator/signalcontrol/translator_test.go`:

```go
package signalcontrol

import (
	"context"
	"fmt"
	"testing"

	"github.com/gosnmp/gosnmp"

	openitspb "github.com/Vikasa2M/openits-models/pkg/proto/openits/v1"
)

// fakeSNMP returns canned Integer PDUs for OIDs present in vals; unknown OIDs
// are omitted from the response (indexByOID then has no entry, which the
// translator treats as "absent" — the same as a real NoSuchInstance skip).
type fakeSNMP struct{ vals map[string]int }

func (f *fakeSNMP) Get(_ context.Context, oids []string) (*gosnmp.SnmpPacket, error) {
	var vars []gosnmp.SnmpPDU
	for _, oid := range oids {
		if v, ok := f.vals[oid]; ok {
			vars = append(vars, gosnmp.SnmpPDU{Name: oid, Type: gosnmp.Integer, Value: v})
		}
	}
	return &gosnmp.SnmpPacket{Variables: vars}, nil
}
func (f *fakeSNMP) GetBulk(context.Context, []string, uint8, uint32) (*gosnmp.SnmpPacket, error) {
	return &gosnmp.SnmpPacket{}, nil
}
func (f *fakeSNMP) Set(context.Context, []gosnmp.SnmpPDU) (*gosnmp.SnmpPacket, error) {
	return &gosnmp.SnmpPacket{}, nil
}
func (f *fakeSNMP) Walk(context.Context, string, gosnmp.WalkFunc) error { return nil }
func (f *fakeSNMP) Close() error                                       { return nil }

func TestTranslateRaw_Characterization(t *testing.T) {
	vals := map[string]int{
		oidOperationStatus:     2, // normal  -> MODE_NORMAL
		oidFlashStatus:         0, // not flashing
		oidPatternStatus:       3, // ActivePlanID 3
		oidPreemptStatus:       0, // no preempt
		oidShortAlarmStatus:    5, // bits 0 and 2 set -> conflict-monitor + cabinet-door
		oidMaxVehicleDetectors: 2, // two detector channels
		// detector channel 1
		fmt.Sprintf("%s.%d", oidVehicleDetectorVolumePrefix, 1):    100,
		fmt.Sprintf("%s.%d", oidVehicleDetectorOccupancyPrefix, 1): 20, // *5 -> OccupancyX10 100
		fmt.Sprintf("%s.%d", oidVehicleDetectorStatusPrefix, 1):    0,
		// detector channel 2
		fmt.Sprintf("%s.%d", oidVehicleDetectorVolumePrefix, 2):    200,
		fmt.Sprintf("%s.%d", oidVehicleDetectorOccupancyPrefix, 2): 40, // *5 -> OccupancyX10 200
		fmt.Sprintf("%s.%d", oidVehicleDetectorStatusPrefix, 2):    0,
	}
	r, err := NewTranslator("asc-001").TranslateRaw(context.Background(), &fakeSNMP{vals: vals})
	if err != nil {
		t.Fatalf("TranslateRaw: %v", err)
	}

	if r.ControllerID != "asc-001" {
		t.Errorf("ControllerID = %q, want asc-001", r.ControllerID)
	}
	if r.SampledAt.IsZero() {
		t.Error("SampledAt should be set")
	}
	if r.Mode != openitspb.OperationalStatus_MODE_NORMAL {
		t.Errorf("Mode = %v, want MODE_NORMAL", r.Mode)
	}
	if r.InConflictFlash {
		t.Error("InConflictFlash = true, want false")
	}
	if r.ActivePlanID != 3 {
		t.Errorf("ActivePlanID = %d, want 3", r.ActivePlanID)
	}
	if r.PreemptionType != openitspb.PreemptionActivated_PREEMPTION_TYPE_UNSPECIFIED {
		t.Errorf("PreemptionType = %v, want UNSPECIFIED", r.PreemptionType)
	}

	// Faults: bits 0 and 2 of shortAlarmStatus=5 -> conflict-monitor, cabinet-door.
	wantFaults := map[string]bool{"conflict-monitor": true, "cabinet-door": true}
	if len(r.Faults) != len(wantFaults) {
		t.Errorf("Faults count = %d, want %d (%v)", len(r.Faults), len(wantFaults), r.Faults)
	}
	for id := range wantFaults {
		if _, ok := r.Faults[id]; !ok {
			t.Errorf("missing expected fault %q", id)
		}
	}

	// Detectors: two channels, occupancy scaled *5.
	if len(r.Detectors) != 2 {
		t.Fatalf("Detectors count = %d, want 2", len(r.Detectors))
	}
	if d := r.Detectors[1]; d.Channel != 1 || d.Volume != 100 || d.OccupancyX10 != 100 {
		t.Errorf("Detector 1 = %+v, want {ch1 vol100 occ100}", d)
	}
	if d := r.Detectors[2]; d.Channel != 2 || d.Volume != 200 || d.OccupancyX10 != 200 {
		t.Errorf("Detector 2 = %+v, want {ch2 vol200 occ200}", d)
	}
}

func TestTranslateRaw_SNMPFailureBecomesSyntheticFault(t *testing.T) {
	// A client whose Get always errors must yield a Reading carrying the
	// synthetic "snmp-unreachable" fault (not an error return).
	r, err := NewTranslator("asc-001").TranslateRaw(context.Background(), errSNMP{})
	if err != nil {
		t.Fatalf("TranslateRaw should not return an error on SNMP failure: %v", err)
	}
	if _, ok := r.Faults["snmp-unreachable"]; !ok {
		t.Errorf("expected snmp-unreachable fault, got %v", r.Faults)
	}
}

type errSNMP struct{}

func (errSNMP) Get(context.Context, []string) (*gosnmp.SnmpPacket, error) {
	return nil, fmt.Errorf("dial timeout")
}
func (errSNMP) GetBulk(context.Context, []string, uint8, uint32) (*gosnmp.SnmpPacket, error) {
	return nil, fmt.Errorf("dial timeout")
}
func (errSNMP) Set(context.Context, []gosnmp.SnmpPDU) (*gosnmp.SnmpPacket, error) {
	return nil, fmt.Errorf("dial timeout")
}
func (errSNMP) Walk(context.Context, string, gosnmp.WalkFunc) error { return fmt.Errorf("dial timeout") }
func (errSNMP) Close() error                                        { return nil }
```

- [ ] **Step 2: Run the test to verify it passes against current code.**

Run: `go test ./internal/translator/signalcontrol/... -v`
Expected: PASS (this characterizes existing behavior — it should be green immediately). If any assertion fails, STOP: either the canned expectation is wrong (fix the test to match real current behavior and note it) or you found a genuine pre-existing bug (report it, do not "fix" production code in P1a).

- [ ] **Step 3: Commit.**

```bash
git add internal/translator/signalcontrol/translator_test.go
git commit -m "test(signalcontrol): characterize SNMP translator output (P1a golden)"
```

> Follow-up (not this task): an analogous characterization test for the RSU translator (`internal/translator/rsu/translator.go`) mirrors this structure with the RSU OID set and `RSUBroadcast` assertions. Detailed in the P1a-continuation brief once the RSU OID constants are read.

---

### Task 2: Characterize `synth.Diff` transitions

**Files:**
- Test: `internal/events/synth/synth_diff_test.go` (create; `synth_test.go` already exists from the ce-id fix — use a new file to avoid churn)

**Interfaces:**
- Consumes: `synth.Diff(prev, curr *driver.Reading) synth.Events`; `driver.Reading`, `driver.Fault`, `driver.Detector` (all fields exported).

- [ ] **Step 1: Write the characterization test.** Create `internal/events/synth/synth_diff_test.go`. Construct Readings with a FIXED `SampledAt` so payload timestamps are deterministic:

```go
package synth

import (
	"testing"
	"time"

	openitspb "github.com/Vikasa2M/openits-models/pkg/proto/openits/v1"
	"github.com/Vikasa2M/openits-collector/sdk/driver"
)

var fixedTime = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func TestDiff_FirstPollEmitsOperationalStatusAndRaisesFaults(t *testing.T) {
	curr := &driver.Reading{
		ControllerID: "asc-001",
		SampledAt:    fixedTime,
		Mode:         openitspb.OperationalStatus_MODE_NORMAL,
		ActivePlanID: 3,
		Faults: map[string]driver.Fault{
			"conflict-monitor": {ID: "conflict-monitor", Severity: openitspb.FaultRaised_SEVERITY_CRITICAL, Category: openitspb.FaultRaised_CATEGORY_CONFLICT, Description: "Conflict monitor active", FirstObserved: fixedTime},
		},
	}
	evs := Diff(nil, curr)

	if evs.OperationalStatus == nil {
		t.Fatal("first poll must emit OperationalStatus")
	}
	if evs.OperationalStatus.Mode != openitspb.OperationalStatus_MODE_NORMAL {
		t.Errorf("OperationalStatus.Mode = %v, want MODE_NORMAL", evs.OperationalStatus.Mode)
	}
	if evs.OperationalStatus.ActivePlanId != 3 {
		t.Errorf("ActivePlanId = %d, want 3", evs.OperationalStatus.ActivePlanId)
	}
	// prev == nil, so no ModeChanged is emitted.
	if len(evs.ModeChanged) != 0 {
		t.Errorf("ModeChanged = %v, want none on first poll", evs.ModeChanged)
	}
	if len(evs.FaultsRaised) != 1 || evs.FaultsRaised[0].GetFaultId() != "conflict-monitor" {
		t.Errorf("FaultsRaised = %v, want one conflict-monitor", evs.FaultsRaised)
	}
	if len(evs.FaultsCleared) != 0 {
		t.Errorf("FaultsCleared = %v, want none", evs.FaultsCleared)
	}
}

func TestDiff_ModeChangeAndFaultClear(t *testing.T) {
	prev := &driver.Reading{
		ControllerID: "asc-001", SampledAt: fixedTime,
		Mode:   openitspb.OperationalStatus_MODE_NORMAL,
		Faults: map[string]driver.Fault{"conflict-monitor": {ID: "conflict-monitor", FirstObserved: fixedTime}},
	}
	curr := &driver.Reading{
		ControllerID: "asc-001", SampledAt: fixedTime.Add(time.Second),
		Mode:   openitspb.OperationalStatus_MODE_FLASH, // changed
		Faults: map[string]driver.Fault{},              // conflict-monitor cleared
	}
	evs := Diff(prev, curr)

	if len(evs.ModeChanged) != 1 {
		t.Fatalf("ModeChanged count = %d, want 1", len(evs.ModeChanged))
	}
	if evs.ModeChanged[0].GetFrom() != openitspb.OperationalStatus_MODE_NORMAL ||
		evs.ModeChanged[0].GetTo() != openitspb.OperationalStatus_MODE_FLASH {
		t.Errorf("ModeChanged = %+v, want NORMAL->FLASH", evs.ModeChanged[0])
	}
	if len(evs.FaultsRaised) != 0 {
		t.Errorf("FaultsRaised = %v, want none", evs.FaultsRaised)
	}
	if len(evs.FaultsCleared) != 1 || evs.FaultsCleared[0].GetFaultId() != "conflict-monitor" {
		t.Errorf("FaultsCleared = %v, want one conflict-monitor", evs.FaultsCleared)
	}
}
```

- [ ] **Step 2: Run the test to verify it passes against current code.**

Run: `go test ./internal/events/synth/... -v`
Expected: PASS (characterizes existing `Diff` behavior). If an assertion fails, STOP and report — do not change `synth.go` in P1a.

> If any getter name (`GetFrom`/`GetTo`/`GetFaultId`/`GetActivePlanId`) does not exist on the generated proto, check the actual generated type in `openits-models` and use the correct accessor; report the correction in your task report.

- [ ] **Step 3: Commit.**

```bash
git add internal/events/synth/synth_diff_test.go
git commit -m "test(synth): characterize Diff transitions (P1a golden)"
```

---

## Self-Review

**Spec coverage:** P1a's goal is a characterization safety net for the poll transform. Task 1 pins Translator→Reading (the SNMP-decode stage, including the graceful-failure fault path); Task 2 pins Reading→events (`synth.Diff`, both first-poll and transition cases). The subject/ce-id stage is intentionally deferred to P1b (envelope collapse), where the envelope path is the code under refactor and brings its own subject/ce-id goldens — noted in the decomposition. RSU translator characterization is flagged as an explicit fast-follow, not silently dropped.

**Placeholder scan:** No "TODO"/"similar to Task N". The one deferred item (RSU golden) is called out as out-of-plan with the reason (RSU OID constants not yet read), not written as a stub task.

**Type consistency:** `fakeSNMP`/`errSNMP` implement all five `snmp.SNMPClient` methods with the signatures from `internal/snmp` (`Get`/`GetBulk`/`Set`/`Walk`/`Close`). Proto accessors are guarded with a "verify the generated getter" note since the exact generated method names live in `openits-models`. `driver.Reading`/`Fault`/`Detector` field names match `sdk/driver/driver.go`.
