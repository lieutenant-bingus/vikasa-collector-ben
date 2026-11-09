package stats

import (
	"slices"
	"sync"
	"time"
)

// Liveness records when each poll loop last completed a round, so the
// systemd watchdog can tell a hung collector from a quiet cabinet. A round
// completes whether or not the device answered: a device that is down is the
// collector working, and only a loop that stops turning — a deadlock, a
// blocked sink — is the collector hung.
type Liveness struct {
	mu    sync.Mutex
	loops map[string]loop
}

type loop struct {
	last   time.Time
	within time.Duration
}

// NewLiveness returns an empty Liveness.
func NewLiveness() *Liveness { return &Liveness{loops: map[string]loop{}} }

// Expect registers a loop that must complete a round within the given bound,
// counting from now.
func (l *Liveness) Expect(id string, within time.Duration, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loops[id] = loop{last: now, within: within}
}

// Beat records a completed round.
func (l *Liveness) Beat(id string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lp, ok := l.loops[id]; ok {
		lp.last = at
		l.loops[id] = lp
	}
}

// Stalled returns, sorted, the loops that have not completed a round within
// their bound.
func (l *Liveness) Stalled(now time.Time) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for id, lp := range l.loops {
		if now.Sub(lp.last) > lp.within {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
