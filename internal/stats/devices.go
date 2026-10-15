package stats

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Devices tracks the last poll outcome per configured device: the facts a
// technician needs at /status to answer "why is this cabinet quiet". They
// are device facts, so they stay off the ADR 0019 heartbeat (clause 9),
// which describes the collector as a process.
type Devices struct {
	mu         sync.Mutex
	byID       map[string]*DeviceStatus
	everPolled atomic.Bool
}

// DeviceStatus is one device's last known poll outcome.
type DeviceStatus struct {
	ID                  string    `json:"id"`
	LastSuccess         time.Time `json:"last_success,omitzero"`
	LastError           string    `json:"last_error,omitempty"`
	LastErrorAt         time.Time `json:"last_error_at,omitzero"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

// NewDevices tracks the given device IDs, so a device that has never been
// polled is still listed — "configured but silent" is itself a finding.
func NewDevices(ids []string) *Devices {
	d := &Devices{byID: make(map[string]*DeviceStatus, len(ids))}
	for _, id := range ids {
		d.byID[id] = &DeviceStatus{ID: id}
	}
	return d
}

// PollOK records a successful poll.
func (d *Devices) PollOK(id string, at time.Time) {
	d.mu.Lock()
	s := d.entry(id)
	s.LastSuccess = at
	s.ConsecutiveFailures = 0
	d.mu.Unlock()
	d.everPolled.Store(true)
}

// PollFailed records a failed poll. The last error is kept after a recovery,
// so a technician can still see what went wrong.
func (d *Devices) PollFailed(id string, at time.Time, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.entry(id)
	s.LastError = err.Error()
	s.LastErrorAt = at
	s.ConsecutiveFailures++
}

// LastSuccess returns the time of the last successful poll of id, the
// DeviceStatus.LastSuccess field. The zero time means this process has not
// yet received a valid response from the device. A later failure does not
// clear it.
func (d *Devices) LastSuccess(id string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.byID[id]
	if s == nil {
		return time.Time{}
	}
	return s.LastSuccess
}

// EverPolled reports whether any device has ever been polled successfully.
// It latches: the readiness rule (management-surface spec §1) depends on a
// later outage never making it false again.
func (d *Devices) EverPolled() bool { return d.everPolled.Load() }

// Snapshot returns every device's status, sorted by ID.
func (d *Devices) Snapshot() []DeviceStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DeviceStatus, 0, len(d.byID))
	for _, s := range d.byID {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b DeviceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (d *Devices) entry(id string) *DeviceStatus {
	s, ok := d.byID[id]
	if !ok {
		s = &DeviceStatus{ID: id}
		d.byID[id] = s
	}
	return s
}
