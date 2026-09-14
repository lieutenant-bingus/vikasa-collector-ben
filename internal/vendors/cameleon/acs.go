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
	ReadWords(memType byte, address1Based, count int) ([]uint16, error)
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
	now              func() time.Time
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
func newACSLazy(deviceID, addr string, timeout time.Duration, dswStart, dswLen int, gates []gateCfg, cab cabinetCfg) adapter.StateReader {
	return &acs{
		deviceID: deviceID, dialAddr: addr, timeout: timeout,
		dswStart: dswStart, dswLen: dswLen,
		gates: gates, cabinet: cab, now: time.Now,
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
	return err
}

func (a *acs) ensureClient() (wordClient, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	if a.dialAddr == "" {
		return nil, fmt.Errorf("no SRTP client and no dial address")
	}
	c, err := gesrtp.Dial(a.dialAddr, a.timeout)
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
}

func (a *acs) Read(ctx context.Context) (*model.Snapshot, error) {
	_ = ctx
	client, err := a.ensureClient()
	if err != nil {
		return nil, fmt.Errorf("cameleon-acs %s: %w", a.deviceID, err)
	}
	words, err := client.ReadWords(gesrtp.MemR, a.dswStart, a.dswLen)
	if err != nil {
		a.dropClient()
		return nil, fmt.Errorf("cameleon-acs %s: %w", a.deviceID, err)
	}
	if len(words) != a.dswLen {
		a.dropClient()
		return nil, fmt.Errorf("cameleon-acs %s: short DSW read: got %d want %d", a.deviceID, len(words), a.dswLen)
	}

	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}

	gates := make([]model.GateReading, 0, len(a.gates))
	var faults []model.Fault
	for _, g := range a.gates {
		w, err := wordAt(words, a.dswStart, g.dsw)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindGateBank, Err: err.Error(),
			})
			return snap, nil
		}
		gr := DecodeGateWord(g.id, g.kind, w)
		gates = append(gates, gr)
		faults = append(faults, gateFaults(gr)...)
	}
	sortGates(gates)
	snap.Facets = append(snap.Facets, model.GateBank{Gates: gates})

	if a.cabinet.lfFault != 0 || a.cabinet.spFault != 0 ||
		a.cabinet.door != 0 || a.cabinet.gateEstop != 0 {
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
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindFaultSet, Err: cerr.Error(),
			})
		} else {
			faults = append(faults, cabinetFaults(lf, sp, door, estop)...)
		}
	}

	sortFaults(faults)
	snap.Facets = append(snap.Facets, model.FaultSet{Faults: faults})
	return snap, nil
}
