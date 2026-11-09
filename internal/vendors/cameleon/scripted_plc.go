package cameleon

import (
	"context"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// ScriptedPLC is an in-memory ACS reader for tests. It uses the same
// watchdog window as a configured adapter. The test moves the clock and
// the counter; nothing is dialed.
type ScriptedPLC struct {
	a      *acs
	words  []uint16
	clock  time.Time
	reader *staticWords
}

// NewScriptedPLC returns a reader whose %R90 watchdog counter starts at
// counter and whose stall window is stall. start is the first sample's clock.
func NewScriptedPLC(counter uint16, stall time.Duration, start time.Time) *ScriptedPLC {
	words := make([]uint16, 110)
	words[0] = counter << 11
	s := &ScriptedPLC{words: words, clock: start}
	s.reader = &staticWords{start: 90, words: words}
	a := NewACS("acs-1", s.reader, 90, 110,
		[]gateCfg{{id: "WG-1", kind: model.GateKindWarning, dsw: 101}},
		cabinetCfg{},
	).(*acs)
	a.plc = plcCfg{watchdogStall: stall}
	a.now = func() time.Time { return s.clock }
	s.a = a
	return s
}

// SetClock moves the fake sample clock. It does not sleep.
func (s *ScriptedPLC) SetClock(t time.Time) { s.clock = t }

// SetCounter replaces the five-bit watchdog counter in %R90. Other status
// bits, including the PLC fault bit, stay clear.
func (s *ScriptedPLC) SetCounter(counter uint16) { s.words[0] = counter << 11 }

// FailReads makes the next Read return err, the same way a timed-out SRTP
// read does. An already-raised stall is kept.
func (s *ScriptedPLC) FailReads(err error) { s.a.client = failWords{err: err} }

// Resume restores scripted word reads after FailReads.
func (s *ScriptedPLC) Resume() { s.a.client = s.reader }

// Read decodes one sample and marks it as an ACS snapshot.
func (s *ScriptedPLC) Read(ctx context.Context) (*model.Snapshot, error) {
	snap, err := s.a.Read(ctx)
	if snap != nil {
		snap.DeviceKind = "acs"
	}
	return snap, err
}

type failWords struct{ err error }

func (f failWords) ReadWords(context.Context, byte, int, int) ([]uint16, error) {
	return nil, f.err
}

func (f failWords) Close() error { return nil }
