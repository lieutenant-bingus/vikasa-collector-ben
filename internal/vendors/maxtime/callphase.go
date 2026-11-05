package maxtime

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"
)

// The phase each detector calls (NTCIP 1202 vehicleDetectorCallPhase) is
// per detector plan on MaxTime. The plain NTCIP name answers an empty table;
// the MIB API serves the plan's table as auxVehicleDetectorCallPhase-<plan>,
// the hyphenated form the controller's own save path uses (2.15.1 lab).
const (
	mibActiveDetectorPlan = "activeVehicleDetectorPlan" // 1..10, the plan in force
	mibCallPhase          = "auxVehicleDetectorCallPhase"
)

// callPhaseRefresh is how often an unchanged plan's assignment is re-read.
// A plan change is seen on the next poll regardless; this bounds how long
// an operator's edit to the running plan goes unseen.
const callPhaseRefresh = 15 * time.Minute

// callPhases is the detector-to-phase assignment the state poll last read,
// shared with the log reader so a detector row carries the phase it called.
// The assignment is an attribute, never evidence of a state change: when it
// cannot be read the phase is unknown (0) and simply omitted.
type callPhases struct {
	mu      sync.Mutex
	plan    int64
	phases  map[uint32]uint8 // channel -> phase; channels calling none are absent
	since   time.Time        // first read of this assignment; rows logged earlier are not joined
	fetched time.Time        // last read, for callPhaseRefresh
}

// refresh re-reads the assignment when the active plan changed or the last
// read is older than callPhaseRefresh. got is the poll's batch answer, which
// carries the active plan. Every failure leaves the phases unknown or as
// last read for the same plan; none fails the poll.
func (c *callPhases) refresh(ctx context.Context, api *mibAPI, got map[string]rows, now time.Time) {
	if c == nil {
		return
	}
	pr, ok := got[mibActiveDetectorPlan]
	if !ok {
		return
	}
	plan, err := pr.scalar(mibActiveDetectorPlan)
	if err != nil || plan < 1 {
		return
	}
	c.mu.Lock()
	fresh := plan == c.plan && c.phases != nil && now.Sub(c.fetched) < callPhaseRefresh
	c.mu.Unlock()
	if fresh {
		return
	}

	name := fmt.Sprintf("%s-%d", mibCallPhase, plan)
	phases, err := readCallPhases(ctx, api, name)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if plan != c.plan {
			// A new plan whose assignment could not be read: the old one
			// no longer applies, and the new one is not known.
			c.plan, c.phases, c.since = plan, nil, time.Time{}
		}
		return
	}
	if plan != c.plan || !maps.Equal(phases, c.phases) {
		c.since = now
	}
	c.plan, c.phases, c.fetched = plan, phases, now
}

func readCallPhases(ctx context.Context, api *mibAPI, name string) (map[uint32]uint8, error) {
	got, err := api.get(ctx, name)
	if err != nil {
		return nil, err
	}
	r, ok := got[name]
	if !ok {
		return nil, fmt.Errorf("%s unanswered", name)
	}
	t, err := r.table(name)
	if err != nil {
		return nil, err
	}
	phases := make(map[uint32]uint8, len(t))
	for ch, ph := range t {
		if ph >= 1 && ph <= 255 { // 0 calls no phase; anything else is not a phase
			phases[ch] = uint8(ph) //nolint:gosec // range-checked above
		}
	}
	return phases, nil
}

// phase is the phase channel called at instant at, or 0 when it called none
// or that is not known: no assignment read, or at predates the read, when an
// earlier plan may have been in force.
func (c *callPhases) phase(channel uint32, at time.Time) uint8 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phases == nil || at.Before(c.since) {
		return 0
	}
	return c.phases[channel]
}
