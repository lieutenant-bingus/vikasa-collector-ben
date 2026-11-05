// Package maxtime is the adapter for Q-Free (Intelight) MaxTime signal
// controllers, read through the controller's own JSON MIB API rather than
// SNMP. The objects it reads are the controller's NTCIP 1202 objects under
// MaxTime's short names (FlashSta is unitFlashStatus, PatrnSta is
// coordPatternStatus, and so on), so every value below is interpreted by its
// NTCIP 1202 definition; the MaxTime-specific part is only how they are
// fetched.
package maxtime

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync/atomic"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// MaxTime short names for the NTCIP 1202 objects this adapter reads.
const (
	mibFlashStatus    = "FlashSta"               // unitFlashStatus
	mibPatternStatus  = "PatrnSta"               // coordPatternStatus
	mibPreemptStatus  = "preemptStatus"          // active preempt, 0 = none
	mibShortAlarms    = "SAlarms"                // shortAlarmStatus
	mibAlarms1        = "Alarms1"                // unitAlarmStatus1
	mibAlarms2        = "Alarms2"                // unitAlarmStatus2
	mibDetectorAlarms = "DtAlrms"                // vehicleDetectorStatusGroupAlarms, one row per group of 8
	mibVolume         = "Volume"                 // detectorVolume: the last completed period's count, uncapped
	mibOccupancy      = "Occupanc"               // detectorOccupancy: 0-200 half-percent, 210-255 detector faults
	mibPeriodSequence = "VOccSeq"                // volumeOccupancySequence: changes when a period completes
	mibSampleTime     = "detectorSampleTime"     // the period's end, local wall-clock seconds since 1970
	mibSampleDuration = "detectorSampleDuration" // the period's length, tenths of a second
)

// pollMIBs is everything one Read asks for, in one request when batching.
var pollMIBs = []string{
	mibFlashStatus, mibPatternStatus, mibPreemptStatus,
	mibShortAlarms, mibAlarms1, mibAlarms2, mibDetectorAlarms,
	mibVolume, mibOccupancy, mibPeriodSequence, mibSampleTime, mibSampleDuration,
}

// ascDescriptor is what is registered: the adapter can read the
// high-resolution log. A device configured without it is state-only, and
// its adapter says so (stateDescriptor), so no event loop starts for it.
var ascDescriptor = adapter.Descriptor{
	Vendor: "maxtime", DeviceKind: "asc", Caps: adapter.CapState | adapter.CapEvents,
}

var stateDescriptor = adapter.Descriptor{
	Vendor: "maxtime", DeviceKind: "asc", Caps: adapter.CapState,
}

type asc struct {
	deviceID string
	api      *mibAPI
	now      func() time.Time
	// loc is the controller clock's zone, for detectorSampleTime. Nil (no
	// asclog block, so no timezone configured) leaves the period's end
	// unknown, and the report is anchored at the poll that saw it.
	loc *time.Location
	// perDetector records that this controller has answered DtAlrms, so a
	// later poll without it cannot fall back to the summary bit and clear
	// the per-detector faults it raised (absence of evidence).
	perDetector atomic.Bool
}

var _ adapter.StateReader = (*asc)(nil)

func (a *asc) Descriptor() adapter.Descriptor { return stateDescriptor }

func (a *asc) Close() error { return a.api.client.Close() }

// Read is one poll. A request that fails outright — the controller is
// unreachable, or refused the credentials — is a hard error, which the runner
// turns into comm health; an object the controller did not answer fails only
// the facet that needed it.
func (a *asc) Read(ctx context.Context) (*model.Snapshot, error) {
	got, err := a.api.get(ctx, pollMIBs...)
	if err != nil {
		return nil, fmt.Errorf("maxtime-asc %s: %w", a.deviceID, err)
	}
	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}
	readSignalStatus(snap, got)
	readFaultSet(snap, got, &a.perDetector)
	readDetectors(snap, got, a.loc)
	return snap, nil
}

// --- signal status -------------------------------------------------------

// unitFlashStatus (NTCIP 1202 §2.4.6): what put the cabinet in flash, or
// notFlash. It is the controller's own answer to "are the heads flashing",
// so it alone decides CabinetOutput.
const flashNotFlash = 2

var flashCause = map[int64]model.FlashCause{
	1: model.FlashOther,           // other
	3: model.FlashAutomatic,       // automatic
	4: model.FlashManual,          // localManual
	5: model.FlashControllerFault, // faultMonitor
	6: model.FlashMonitor,         // mmu
	7: model.FlashStartup,         // startup
	8: model.FlashPreempt,         // preempt
}

// coordPatternStatus (NTCIP 1202 §2.5.10): 1..253 the running pattern, 254
// free, 255 flash, 0 not used.
const (
	patternFree  = 254
	patternFlash = 255
)

func readSignalStatus(snap *model.Snapshot, got map[string]rows) {
	fail := func(msg string) {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindSignalStatus, Err: msg})
	}
	var vals [3]int64
	for i, name := range []string{mibFlashStatus, mibPatternStatus, mibPreemptStatus} {
		r, ok := got[name]
		if !ok {
			// Absence of evidence is never a state change.
			fail(name + " unanswered")
			return
		}
		v, err := r.scalar(name)
		if err != nil {
			fail(err.Error())
			return
		}
		vals[i] = v
	}
	flash, pattern, preempt := vals[0], vals[1], vals[2]

	st := model.SignalStatus{}
	switch {
	case flash == flashNotFlash:
		st.CabinetOutput = model.OutputNormal
	case flashCause[flash] != model.FlashCauseUnknown:
		st.CabinetOutput, st.FlashCause = model.OutputFlash, flashCause[flash]
	default:
		// An out-of-range value says nothing about the heads. Unknown, not
		// normal: claiming normal would clear a real flash downstream.
		st.CabinetOutput = model.OutputUnknown
	}
	// Coordination is independent of the output (ADR 0024): a controller in
	// flash is not "free", it is flashing, and its coordination is unknown.
	switch {
	case pattern >= 1 && pattern < patternFree:
		st.Mode, st.ActivePlanID = model.ModeCoordinated, uint32(pattern)
	case pattern == patternFree:
		st.Mode = model.ModeFree
	default: // 0 (not used) or 255 (flash)
		st.Mode = model.ModeUnknown
	}
	if preempt > 0 {
		st.PreemptionActive = true
		st.PreemptionSource = fmt.Sprintf("preempt-%d", preempt)
	}
	snap.Facets = append(snap.Facets, st)
}

// --- faults --------------------------------------------------------------

type faultBit struct {
	bit         uint
	id          string
	severity    model.FaultSeverity
	category    model.FaultCategory
	description string
}

// The NTCIP 1202 alarm bitmaps, restricted to bits that are faults. Bits that
// report an operating state the signal-status facet already carries —
// shortAlarmStatus preempt, local override and cycle zero, unitAlarmStatus1
// local flash, local free and coord active, unitAlarmStatus2 external start,
// stop time and offset transitioning — are deliberately not faults: raising
// them would report a preemption, or a technician's flash switch, twice,
// once as a state change and once as an alarm. The summary bits (critical,
// non-critical) are roll-ups of the detail bits and are not raised either.
var (
	shortAlarmFaults = []faultBit{
		{1, "tf-flash", model.SeverityCritical, model.CategoryCabinet, "Terminal & facilities flash input active"},
		{4, "coord-alarm", model.SeverityMajor, model.CategoryController, "Coordination alarm"},
		// Bit 5 (detector fault) is raised per detector from DtAlrms when the
		// controller answers it; see readFaultSet.
	}
	alarms1Faults = []faultBit{
		{0, "cycle-fault", model.SeverityMajor, model.CategoryController, "Cycle fault: coordination cycle diagnostic failed"},
		{1, "coord-fault", model.SeverityMajor, model.CategoryController, "Coordination fault"},
		{2, "coord-fail", model.SeverityMajor, model.CategoryController, "Coordination failed; running free"},
		{3, "cycle-fail", model.SeverityMajor, model.CategoryController, "Cycle failure"},
		{4, "mmu-fault", model.SeverityCritical, model.CategoryConflict, "MMU flash"},
	}
	alarms2Faults = []faultBit{
		{0, "power-restart", model.SeverityMinor, model.CategoryPower, "Power restored after an outage"},
		{1, "low-battery", model.SeverityMajor, model.CategoryPower, "Low battery"},
		{2, "response-fault", model.SeverityMajor, model.CategoryCommunication, "Communication response fault"},
	}
)

const shortAlarmDetectorFault = 5

func readFaultSet(snap *model.Snapshot, got map[string]rows, answered *atomic.Bool) {
	fail := func(msg string) {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindFaultSet, Err: msg})
	}
	var bits [3]int64
	for i, name := range []string{mibShortAlarms, mibAlarms1, mibAlarms2} {
		r, ok := got[name]
		if !ok {
			// Defaulting to "no faults" would clear every real fault downstream.
			fail(name + " unanswered")
			return
		}
		v, err := r.scalar(name)
		if err != nil {
			fail(err.Error())
			return
		}
		bits[i] = v
	}
	fs := model.FaultSet{}
	for i, table := range [][]faultBit{shortAlarmFaults, alarms1Faults, alarms2Faults} {
		for _, f := range table {
			if bits[i]&(1<<f.bit) != 0 {
				fs.Faults = append(fs.Faults, model.Fault{ID: f.id, Severity: f.severity, Category: f.category, Description: f.description})
			}
		}
	}

	// Detector faults: one per failed detector when the controller answers
	// the per-group alarm table, else the summary bit. Either way a detector
	// fault is raised exactly once per detector, never both forms.
	perDetector := false
	r, ok := got[mibDetectorAlarms]
	if !ok && answered.Load() && bits[0]&(1<<shortAlarmDetectorFault) != 0 {
		// It answered before and a detector is failed: which ones is
		// unknown this poll, and the summary fault would clear them.
		fail(mibDetectorAlarms + " unanswered")
		return
	}
	if ok {
		groups, err := r.table(mibDetectorAlarms)
		if err != nil {
			fail(err.Error())
			return
		}
		perDetector = true
		answered.Store(true)
		for _, g := range slices.Sorted(maps.Keys(groups)) {
			for b := range uint32(8) {
				if groups[g]&(1<<b) != 0 {
					det := (g-1)*8 + b + 1
					fs.Faults = append(fs.Faults, model.Fault{
						ID: fmt.Sprintf("detector-%d-fault", det), Severity: model.SeverityMinor,
						Category: model.CategoryDetector, Description: fmt.Sprintf("Vehicle detector %d fault", det),
					})
				}
			}
		}
	}
	if !perDetector && bits[0]&(1<<shortAlarmDetectorFault) != 0 {
		fs.Faults = append(fs.Faults, model.Fault{ID: "detector-fault", Severity: model.SeverityMinor, Category: model.CategoryDetector, Description: "Detector fault"})
	}
	snap.Facets = append(snap.Facets, fs)
}

// --- detectors -----------------------------------------------------------

// readDetectors reads one completed collection period (ADR 0026).
//
// Lab, firmware 2.15.1, SNMP-driven actuations against SNMP v1 reads of
// the same objects: Volume and Occupanc equal NTCIP detectorVolume and
// detectorOccupancy after each roll (det 1 pulsed 1 s in 5: 180 and 40;
// det 3 held: 198), except that Volume is not capped at 254. They hold the
// last completed period, which rolled every 900 s (detectorSampleDuration
// 9000; VOccPer 60 times auxVolOccNumPeriods 15), so they are period
// totals, not counters. percentOccupancy, read before this, stayed 0
// throughout and is not occupancy.
func readDetectors(snap *model.Snapshot, got map[string]rows, loc *time.Location) {
	fail := func(msg string) {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindDetectorSamples, Err: msg})
	}
	vr, vok := got[mibVolume]
	or, ook := got[mibOccupancy]
	if !vok || !ook {
		fail("detector volume/occupancy unanswered")
		return
	}
	period, err := readPeriod(got, loc)
	if err != nil {
		// Without the period the totals cannot be attributed: publishing
		// them as a counter's deltas is the error ADR 0026 fixes.
		fail(err.Error())
		return
	}
	vol, err := vr.table(mibVolume)
	if err != nil {
		fail(err.Error())
		return
	}
	occ, err := or.table(mibOccupancy)
	if err != nil {
		fail(err.Error())
		return
	}
	// A controller with no detector rows yields an EMPTY facet, not an error.
	ds := model.DetectorSamples{Period: period}
	for _, ch := range slices.Sorted(maps.Keys(vol)) {
		o, ok := occ[ch]
		if !ok || vol[ch] < 0 || vol[ch] > math.MaxUint32 || o < 0 || o > 200 {
			// Half-read, out of range, or an NTCIP occupancy fault code
			// (210-255): skip the channel rather than fabricate a sample.
			continue
		}
		ds.Samples = append(ds.Samples, model.DetectorSample{
			Channel:         ch,
			VolumeCount:     uint32(vol[ch]), //nolint:gosec // range-checked above
			OccupancyTenths: uint16(o * 5),   //nolint:gosec // half-percent 0..200 checked above
		})
	}
	snap.Facets = append(snap.Facets, ds)
}

// readPeriod is the completed period the detector values total.
func readPeriod(got map[string]rows, loc *time.Location) (*model.CollectionPeriod, error) {
	need := func(name string) (int64, error) {
		r, ok := got[name]
		if !ok {
			return 0, fmt.Errorf("detector collection period: %s unanswered", name)
		}
		return r.scalar(name)
	}
	seq, err := need(mibPeriodSequence)
	if err != nil {
		return nil, err
	}
	tenths, err := need(mibSampleDuration)
	if err != nil {
		return nil, err
	}
	if seq < 0 || seq > math.MaxUint32 || tenths <= 0 {
		return nil, fmt.Errorf("detector collection period: sequence %d, duration %d tenths", seq, tenths)
	}
	p := &model.CollectionPeriod{
		Sequence: uint32(seq), //nolint:gosec // range-checked above
		Duration: time.Duration(tenths) * time.Second / 10,
	}
	// The end is optional: absent, or no zone to read it in, and the
	// differ anchors the report at the poll.
	if r, ok := got[mibSampleTime]; ok && loc != nil {
		if secs, err := r.scalar(mibSampleTime); err == nil && secs > 0 {
			p.End = localWallClock(secs, loc)
		}
	}
	return p, nil
}

// localWallClock reads seconds since 1970 counted on the controller's
// local clock (1793871000 was 09:30:00 EST, not 09:30 UTC) as that local
// time. In the repeated hour at a DST step back, Go picks the first.
func localWallClock(secs int64, loc *time.Location) time.Time {
	w := time.Unix(secs, 0).UTC()
	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, loc)
}
