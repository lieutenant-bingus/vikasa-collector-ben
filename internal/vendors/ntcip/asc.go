// Package ntcip implements the generic standards-only vendor: pure NTCIP
// with no vendor quirks. It is the compatibility target other ASC vendors
// start from (ADR 0003).
package ntcip

import (
	"context"
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// NTCIP 1202 OIDs polled per cycle.
const (
	oidOperationStatus      = ".1.3.6.1.4.1.1206.4.2.1.2.7.0"
	oidFlashStatus          = ".1.3.6.1.4.1.1206.4.2.1.2.5.0"
	oidPatternStatus        = ".1.3.6.1.4.1.1206.4.2.1.3.2.0"
	oidPreemptStatus        = ".1.3.6.1.4.1.1206.4.2.1.6.5.0"
	oidShortAlarmStatus     = ".1.3.6.1.4.1.1206.4.2.1.5.1.0"
	oidMaxVehicleDetectors  = ".1.3.6.1.4.1.1206.4.2.1.2.3.0"
	oidDetectorVolumeCol    = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.2"
	oidDetectorOccupancyCol = ".1.3.6.1.4.1.1206.4.2.1.2.4.1.4"
)

// defaultMaxDetectorChannels bounds the detector table when the controller
// does not answer maxVehicleDetectors.
const defaultMaxDetectorChannels = 32

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

var ascDescriptor = adapter.Descriptor{
	Vendor: "ntcip", DeviceKind: "asc", Caps: adapter.CapState,
}

type asc struct {
	deviceID string
	client   snmp.Client
	now      func() time.Time
}

// NewASC wraps an SNMP client as the ntcip-asc StateReader. Exported so
// fixture tests (and vendor adapters embedding the NTCIP base) can inject
// a client.
func NewASC(deviceID string, client snmp.Client) adapter.StateReader {
	return &asc{deviceID: deviceID, client: client, now: time.Now}
}

func (a *asc) Descriptor() adapter.Descriptor { return ascDescriptor }
func (a *asc) Close() error                   { return a.client.Close() }

func (a *asc) Read(ctx context.Context) (*model.Snapshot, error) {
	vals, err := a.client.Get(ctx, []string{
		oidOperationStatus, oidFlashStatus, oidPatternStatus, oidPreemptStatus,
		oidShortAlarmStatus, oidMaxVehicleDetectors,
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
	a.readDetectors(ctx, snap, vals)
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

// readDetectors issues a SECOND Get for the detector table. The table is read
// as synthesized indexed OIDs in a single Get call rather than a walk: the
// transport chunks that call into batches of 16 varbinds per round-trip, but
// even ~32 round-trips for a full 255-channel table is far fewer than the
// ~510 a GetNext/BulkWalk would take walking the table one OID at a time.
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
		// vol has no documented range (unlike occupancy below), so it is not
		// narrowed here. A negative vol wraps to ~4.29e9 in the uint32, which
		// the differ's delta() reads as a counter reset on the next poll — it
		// self-heals after emitting one garbage reading, so it is left as is
		// rather than inventing a clamp the domain does not specify.
		//
		// occ, in contrast, has a documented domain contract: OccupancyTenths
		// is 0..1000 (sdk/model/detector.go). occ arrives as a signed int64
		// from a device we do not control (gosnmp.Integer is signed), so an
		// unclamped uint16(occ*5) would silently violate that contract:
		// negative values wrap to a huge unsigned tenths value (e.g. -1 ->
		// 65531, a 6553% occupancy) and out-of-spec-high values overflow
		// uint16 and wrap to something small and misleading. Clamp at the
		// point the vendor's value enters the domain rather than propagate
		// either failure mode. Volume is unaffected and still valuable, so
		// the sample is clamped, not skipped.
		ds.Samples = append(ds.Samples, model.DetectorSample{
			Channel:         uint32(ch),
			VolumeCount:     uint32(vol),
			OccupancyTenths: clampOccupancyTenths(occ),
		})
	}
	snap.Facets = append(snap.Facets, ds)
}

// clampOccupancyTenths converts NTCIP half-percent occupancy (nominally
// 0..200) to the domain's documented 0..1000 tenths, clamping rather than
// trusting the device: occ is a signed int64 read off the wire, and a
// controller reporting outside spec is lying or broken, not a case to
// silently mis-widen.
func clampOccupancyTenths(occ int64) uint16 {
	switch {
	case occ < 0:
		return 0
	case occ > 200:
		return 1000
	default:
		return uint16(occ * 5)
	}
}

func modeFromOperation(v int64) model.ControllerMode {
	switch v {
	case 2:
		return model.ModeNormal
	case 3:
		return model.ModeStandby
	case 4:
		return model.ModeFlash
	default:
		return model.ModeUnknown
	}
}
