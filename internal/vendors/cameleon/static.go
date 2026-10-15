package cameleon

import (
	"context"
	"fmt"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// GateWord is one gate inside a scripted DSW block.
type GateWord struct {
	ID   string
	Kind model.GateKind
	DSW  int
}

// CabinetWords are the optional cabinet fault registers. Zero means unset.
type CabinetWords struct {
	LFFault, SPFault, Door, GateEstop int
}

// ReadBlock decodes a fixed %R image the way a live Read would. Tests
// outside this package use it so they do not need a PLC.
func ReadBlock(deviceID string, dswStart int, words []uint16, gates []GateWord, cab CabinetWords) (*model.Snapshot, error) {
	gs := make([]gateCfg, len(gates))
	for i, g := range gates {
		gs[i] = gateCfg{id: g.ID, kind: g.Kind, dsw: g.DSW}
	}
	a := NewACS(deviceID, &staticWords{start: dswStart, words: words}, dswStart, len(words), gs, cabinetCfg{
		lfFault: cab.LFFault, spFault: cab.SPFault, door: cab.Door, gateEstop: cab.GateEstop,
	})
	return a.Read(context.Background())
}

// staticWords is a fixture SRTP client: ReadWords returns a slice of the
// preloaded block starting at address1Based. Addresses outside the block
// error so misconfigured tests fail loudly.
type staticWords struct {
	start int // 1-based %R of words[0]
	words []uint16
}

func (s *staticWords) ReadWords(_ context.Context, _ byte, address1Based, count int) ([]uint16, error) {
	if address1Based < s.start {
		return nil, fmt.Errorf("address %%R%d before fixture start %%R%d", address1Based, s.start)
	}
	off := address1Based - s.start
	if off+count > len(s.words) {
		return nil, fmt.Errorf("read %%R%d count %d past fixture end", address1Based, count)
	}
	out := make([]uint16, count)
	copy(out, s.words[off:off+count])
	return out, nil
}

func (s *staticWords) Close() error { return nil }
