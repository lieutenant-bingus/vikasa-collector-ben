package cameleon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon/gesrtp"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var acsDescriptor = adapter.Descriptor{
	Vendor: "cameleon", DeviceKind: "acs", Caps: adapter.CapState,
}

// wordClient is the read surface the ACS adapter needs from SRTP.
// Production dials lazily; tests inject a static block.
type wordClient interface {
	ReadWords(ctx context.Context, memType byte, address1Based, count int) ([]uint16, error)
	Close() error
}

type gateCfg struct {
	id   string
	kind model.GateKind
	dsw  int
}

type cabinetCfg struct {
	lfFault, spFault, door, gateEstop int // 0 = not configured
}

// plcCfg is the optional comparison against %R90–%R94. A zero watchdogStall
// disables plc/not-scanning. The config parser sets the 5s default; NewACS
// leaves it disabled so a scripted block does not grow a stall fault.
type plcCfg struct {
	hasCabinetID  bool
	cabinetID     int
	hasProgramCRC bool
	programCRC    uint32
	hasConfigCRC  bool
	configCRC     uint32
	watchdogStall time.Duration
}

type acs struct {
	deviceID string

	// dialAddr/timeout used when client is nil (lazy connect). Tests that
	// inject a client leave dialAddr empty.
	dialAddr string
	timeout  time.Duration

	mu               sync.Mutex
	client           wordClient
	dswStart, dswLen int
	gates            []gateCfg
	cabinet          cabinetCfg
	plc              plcCfg
	now              func() time.Time
	// Watchdog observation. wdHave is false until a published poll has seen
	// the counter, and after a failed read or a reconnect.
	wdHave  bool
	wdLast  uint16
	wdSince time.Time
}

// NewACS wraps an already-open word reader (fixtures / live tests).
func NewACS(deviceID string, client wordClient, dswStart, dswLen int, gates []gateCfg, cab cabinetCfg) adapter.StateReader {
	return &acs{
		deviceID: deviceID, client: client,
		dswStart: dswStart, dswLen: dswLen,
		gates: gates, cabinet: cab, now: time.Now,
	}
}

// newACSLazy builds an adapter that dials on first Read and re-dials after
// a failed poll. Unreachable cabinets do not fail collector boot — the
// runner turns hard Read errors into device-unreachable health events.
func newACSLazy(deviceID, addr string, timeout time.Duration, dswStart, dswLen int, gates []gateCfg, cab cabinetCfg, plc plcCfg) adapter.StateReader {
	return &acs{
		deviceID: deviceID, dialAddr: addr, timeout: timeout,
		dswStart: dswStart, dswLen: dswLen,
		gates: gates, cabinet: cab, plc: plc, now: time.Now,
	}
}

func (a *acs) Descriptor() adapter.Descriptor { return acsDescriptor }

func (a *acs) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	err := a.client.Close()
	a.client = nil
	a.wdHave = false
	return err
}

func (a *acs) ensureClient(ctx context.Context) (wordClient, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	if a.dialAddr == "" {
		return nil, fmt.Errorf("no SRTP client and no dial address")
	}
	c, err := gesrtp.Dial(ctx, a.dialAddr, a.timeout)
	if err != nil {
		return nil, err
	}
	a.client = c
	return a.client, nil
}

func (a *acs) dropClient() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		_ = a.client.Close()
		a.client = nil
	}
	a.wdHave = false
}

func (a *acs) Read(ctx context.Context) (*model.Snapshot, error) {
	client, err := a.ensureClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cameleon-acs %s: %w", a.deviceID, err)
	}
	words, err := client.ReadWords(ctx, gesrtp.MemR, a.dswStart, a.dswLen)
	if err != nil {
		a.dropClient()
		return nil, fmt.Errorf("cameleon-acs %s: %w", a.deviceID, err)
	}
	if len(words) != a.dswLen {
		a.dropClient()
		return nil, fmt.Errorf("cameleon-acs %s: short DSW read: got %d want %d", a.deviceID, len(words), a.dswLen)
	}

	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}

	// A facet that cannot be fully decoded is left out of Facets and named
	// in Errors (ADR 0013): absence of evidence is never a state change.
	// One bad word must not discard the facets that did decode.
	gates := make([]model.GateReading, 0, len(a.gates))
	var faults []model.Fault
	var faultErr error
	gatesOK := true
	for _, g := range a.gates {
		w, err := wordAt(words, a.dswStart, g.dsw)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindGateBank, Err: err.Error(),
			})
			gatesOK = false
			faultErr = err
			break
		}
		gr := DecodeGateWord(g.id, g.kind, w)
		gates = append(gates, gr)
		faults = append(faults, gateFaults(gr)...)
	}
	if gatesOK {
		sortGates(gates)
		snap.Facets = append(snap.Facets, model.GateBank{Gates: gates})
	}

	if faultErr == nil && (a.cabinet.lfFault != 0 || a.cabinet.spFault != 0 ||
		a.cabinet.door != 0 || a.cabinet.gateEstop != 0) {
		lf, sp, door, estop := uint16(0), uint16(0), uint16(0), uint16(0)
		var cerr error
		if a.cabinet.lfFault != 0 {
			lf, cerr = wordAt(words, a.dswStart, a.cabinet.lfFault)
		}
		if cerr == nil && a.cabinet.spFault != 0 {
			sp, cerr = wordAt(words, a.dswStart, a.cabinet.spFault)
		}
		if cerr == nil && a.cabinet.door != 0 {
			door, cerr = wordAt(words, a.dswStart, a.cabinet.door)
		}
		if cerr == nil && a.cabinet.gateEstop != 0 {
			estop, cerr = wordAt(words, a.dswStart, a.cabinet.gateEstop)
		}
		if cerr != nil {
			faultErr = cerr
		} else {
			faults = append(faults, cabinetFaults(lf, sp, door, estop)...)
		}
	}

	if faultErr == nil && a.seesPLCStatus() {
		pf, perr := a.plcFaults(words, snap.SampledAt)
		if perr != nil {
			faultErr = perr
		} else {
			faults = append(faults, pf...)
		}
	}

	// The fault set is one facet built from gate alarm bits, cabinet words,
	// and the PLC status words together. If any of that is unreadable the
	// set is incomplete, and publishing the partial list would look like the
	// missing faults cleared. A failed observation also drops the watchdog
	// window so the next good poll cannot raise or clear plc/not-scanning
	// from time the PLC was not seen.
	if faultErr != nil {
		a.resetWatchdog()
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: faultErr.Error(),
		})
		return snap, nil
	}

	sortFaults(faults)
	snap.Facets = append(snap.Facets, model.FaultSet{Faults: faults})
	return snap, nil
}

func (a *acs) seesPLCStatus() bool {
	return a.dswStart <= 90 && 94 < a.dswStart+a.dswLen
}

func (a *acs) plcFaults(words []uint16, now time.Time) ([]model.Fault, error) {
	dsw0, err := wordAt(words, a.dswStart, 90)
	if err != nil {
		return nil, err
	}
	progLo, err := wordAt(words, a.dswStart, 91)
	if err != nil {
		return nil, err
	}
	progHi, err := wordAt(words, a.dswStart, 92)
	if err != nil {
		return nil, err
	}
	cfgLo, err := wordAt(words, a.dswStart, 93)
	if err != nil {
		return nil, err
	}
	cfgHi, err := wordAt(words, a.dswStart, 94)
	if err != nil {
		return nil, err
	}
	var out []model.Fault
	add := func(id, desc string, sev model.FaultSeverity) {
		out = append(out, model.Fault{
			ID: id, Severity: sev, Category: model.CategoryCabinet, Description: desc,
		})
	}
	if dsw0&(1<<10) != 0 {
		add("plc/fault", "PLC fault bit active", model.SeverityMajor)
	}
	if dsw0&(1<<7) != 0 {
		add("plc/cabinet-id-changed", "PLC cabinet ID changed", model.SeverityMajor)
	}
	if a.plc.hasCabinetID {
		got := int((dsw0 >> 1) & 0x3f)
		if got != a.plc.cabinetID {
			add("plc/cabinet-id-mismatch", "PLC cabinet ID does not match configuration", model.SeverityMajor)
		}
	}
	if a.plc.hasProgramCRC {
		prog := uint32(progLo) | uint32(progHi)<<16
		if prog != a.plc.programCRC {
			add("plc/program-crc-mismatch", "PLC program CRC does not match configuration", model.SeverityMajor)
		}
	}
	if a.plc.hasConfigCRC {
		cfg := uint32(cfgLo) | uint32(cfgHi)<<16
		if cfg != a.plc.configCRC {
			add("plc/config-crc-mismatch", "PLC config CRC does not match configuration", model.SeverityMajor)
		}
	}
	if a.stalled(dsw0>>11, now) {
		add("plc/not-scanning", "PLC watchdog has not advanced", model.SeverityCritical)
	}
	return out, nil
}

func (a *acs) stalled(counter uint16, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.wdHave || counter != a.wdLast {
		a.wdHave = true
		a.wdLast = counter
		a.wdSince = now
		return false
	}
	if a.plc.watchdogStall <= 0 {
		return false
	}
	return !now.Before(a.wdSince.Add(a.plc.watchdogStall))
}

func (a *acs) resetWatchdog() {
	a.mu.Lock()
	a.wdHave = false
	a.mu.Unlock()
}
