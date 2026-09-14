package cameleon

import (
	"fmt"
	"sort"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// DecodeGateWord maps one Cameleon ACS gate DSW word into a GateReading.
// Out-of-range mode/position bitfields clamp to Unknown (with a test).
func DecodeGateWord(id string, kind model.GateKind, w uint16) model.GateReading {
	modeBits := w & 0b11
	posBits := (w >> 4) & 0b111
	g := model.GateReading{
		ID:            id,
		Kind:          kind,
		Mode:          gateModeFromBits(modeBits),
		Position:      gatePositionFromBits(posBits),
		LockOpen:      w&(1<<2) != 0,
		LockClose:     w&(1<<3) != 0,
		FailedClose:   w&(1<<10) != 0,
		FailedOpen:    w&(1<<11) != 0,
		Estop:         w&(1<<12) != 0,
		CmdRefused:    w&(1<<13) != 0,
		InvalidInputs: w&(1<<14) != 0,
		Raw:           w,
	}
	return g
}

func gateModeFromBits(bits uint16) model.GateMode {
	switch bits {
	case 1:
		return model.GateModeAuto
	case 2:
		return model.GateModeManual
	case 3:
		return model.GateModeOffline
	default:
		return model.GateModeUnknown
	}
}

func gatePositionFromBits(bits uint16) model.GatePosition {
	switch bits {
	case 1:
		return model.GatePositionClosing
	case 2:
		return model.GatePositionClosed
	case 3:
		return model.GatePositionOpening
	case 4:
		return model.GatePositionOpened
	default:
		// 0 and anything >4 → unknown
		return model.GatePositionUnknown
	}
}

// gateFaults returns raised Fault entries for a gate's alarm bits.
func gateFaults(g model.GateReading) []model.Fault {
	var out []model.Fault
	add := func(suffix, desc string, sev model.FaultSeverity) {
		out = append(out, model.Fault{
			ID:          "gate/" + g.ID + "/" + suffix,
			Severity:    sev,
			Category:    model.CategoryCabinet,
			Description: desc,
		})
	}
	if g.FailedClose {
		add("failed-to-close", "gate failed to close", model.SeverityMajor)
	}
	if g.FailedOpen {
		add("failed-to-open", "gate failed to open", model.SeverityMajor)
	}
	if g.Estop {
		add("estop", "gate emergency stop", model.SeverityCritical)
	}
	if g.CmdRefused {
		add("cmd-refused", "gate command refused", model.SeverityWarning)
	}
	if g.InvalidInputs {
		add("invalid-inputs", "gate invalid inputs", model.SeverityWarning)
	}
	return out
}

// cabinetFaults maps non-zero cabinet DI words to Fault entries.
// Treat non-zero as active (Cameleon DSW pattern from the ACS handoff).
func cabinetFaults(lf, sp, door, estop uint16) []model.Fault {
	var out []model.Fault
	if lf != 0 {
		out = append(out, model.Fault{
			ID: "cabinet/lf-fault", Severity: model.SeverityMajor,
			Category: model.CategoryCabinet, Description: "ACS LF fault active",
		})
	}
	if sp != 0 {
		out = append(out, model.Fault{
			ID: "cabinet/sp-fault", Severity: model.SeverityMajor,
			Category: model.CategoryCabinet, Description: "ACS SP fault active",
		})
	}
	if door != 0 {
		out = append(out, model.Fault{
			ID: "cabinet/door", Severity: model.SeverityWarning,
			Category: model.CategoryCabinet, Description: "ACS cabinet door switch active",
		})
	}
	if estop != 0 {
		out = append(out, model.Fault{
			ID: "cabinet/gate-estop", Severity: model.SeverityCritical,
			Category: model.CategoryCabinet, Description: "ACS cabinet-wide gate E-STOP active",
		})
	}
	return out
}

func sortFaults(fs []model.Fault) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
}

func sortGates(gs []model.GateReading) {
	sort.Slice(gs, func(i, j int) bool { return gs[i].ID < gs[j].ID })
}

func wordAt(block []uint16, dswStart, addr int) (uint16, error) {
	idx := addr - dswStart
	if idx < 0 || idx >= len(block) {
		return 0, fmt.Errorf("%%R%d outside DSW block starting at %d length %d", addr, dswStart, len(block))
	}
	return block[idx], nil
}
