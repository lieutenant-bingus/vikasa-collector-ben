package cameleon

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// RegisterTo registers cameleon-acs. The connection block is
//
//	connection:
//	  srtp: { address: "host:18245", timeout: "5s" }   # timeout optional
//	  dsw:  { start: 90, length: 110 }
//	  gates:
//	    - { id: "WG-111", dsw: 101, kind: "warning" }
//	  cabinet:                                         # optional
//	    lf_fault: 179
//	    sp_fault: 180
//	    door: 181
//	    gate_estop: 182
//	  plc:                                             # optional
//	    cabinet_id: 1                                  # fault if %R90 bits 1–6 differ
//	    program_crc: "0x00000000"                      # fault while live %R91/%R92 differ
//	    config_crc: "0x00000000"                       # fault while live %R93/%R94 differ
//	    watchdog_stall: "5s"                           # default 5s; 0 disables plc/not-scanning
//
// %R90–%R94 are the PLC status words when they fall inside dsw. Only bit 0 of
// each cabinet word is the input. %R95–%R99 (user name) are not read. %R183
// and every DCW address are never written.
func RegisterTo(r *adapter.Registry) {
	r.Register(acsDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseACSBlock(conn)
		if err != nil {
			return nil, fmt.Errorf("cameleon-acs %s: %w", deviceID, err)
		}
		// Lazy dial: unreachable cabinets must not fail collector boot.
		return newACSLazy(deviceID, cfg.addr, cfg.timeout, cfg.dswStart, cfg.dswLen, cfg.gates, cfg.cabinet, cfg.plc), nil
	})
}

type acsConn struct {
	addr             string
	timeout          time.Duration
	dswStart, dswLen int
	gates            []gateCfg
	cabinet          cabinetCfg
	plc              plcCfg
}

func parseACSBlock(conn map[string]any) (acsConn, error) {
	var out acsConn
	rawSRTP, ok := conn["srtp"].(map[string]any)
	if !ok {
		return out, fmt.Errorf("connection.srtp block required")
	}
	out.addr, _ = rawSRTP["address"].(string)
	if out.addr == "" {
		return out, fmt.Errorf("connection.srtp.address required")
	}
	out.timeout = 5 * time.Second
	if t, ok := rawSRTP["timeout"].(string); ok && t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return out, fmt.Errorf("connection.srtp.timeout: %w", err)
		}
		if d <= 0 {
			return out, fmt.Errorf("connection.srtp.timeout must be > 0")
		}
		out.timeout = d
	}

	rawDSW, ok := conn["dsw"].(map[string]any)
	if !ok {
		return out, fmt.Errorf("connection.dsw block required")
	}
	start, err := asInt(rawDSW["start"])
	if err != nil || start < 1 {
		return out, fmt.Errorf("connection.dsw.start must be a positive %%R address")
	}
	length, err := asInt(rawDSW["length"])
	if err != nil || length < 1 {
		return out, fmt.Errorf("connection.dsw.length must be a positive word count")
	}
	out.dswStart, out.dswLen = start, length
	out.plc.watchdogStall = 5 * time.Second

	rawGates, ok := conn["gates"].([]any)
	if !ok || len(rawGates) == 0 {
		return out, fmt.Errorf("connection.gates must list at least one gate")
	}
	seen := map[string]bool{}
	for i, raw := range rawGates {
		m, ok := raw.(map[string]any)
		if !ok {
			return out, fmt.Errorf("connection.gates[%d]: must be a map", i)
		}
		id, _ := m["id"].(string)
		if id == "" {
			return out, fmt.Errorf("connection.gates[%d]: id required", i)
		}
		if seen[id] {
			return out, fmt.Errorf("connection.gates: duplicate id %q", id)
		}
		seen[id] = true
		dsw, err := asInt(m["dsw"])
		if err != nil || dsw < start || dsw >= start+length {
			return out, fmt.Errorf("connection.gates[%d]: dsw must fall inside the DSW block", i)
		}
		kind, err := parseGateKind(m["kind"])
		if err != nil {
			return out, fmt.Errorf("connection.gates[%d]: %w", i, err)
		}
		out.gates = append(out.gates, gateCfg{id: id, kind: kind, dsw: dsw})
	}

	if rawCab, ok := conn["cabinet"].(map[string]any); ok {
		out.cabinet.lfFault, err = optionalAddr(rawCab, "lf_fault", start, length)
		if err != nil {
			return out, err
		}
		out.cabinet.spFault, err = optionalAddr(rawCab, "sp_fault", start, length)
		if err != nil {
			return out, err
		}
		out.cabinet.door, err = optionalAddr(rawCab, "door", start, length)
		if err != nil {
			return out, err
		}
		out.cabinet.gateEstop, err = optionalAddr(rawCab, "gate_estop", start, length)
		if err != nil {
			return out, err
		}
	}

	for k := range conn {
		switch k {
		case "srtp", "dsw", "gates", "cabinet", "plc":
		default:
			return out, fmt.Errorf("connection: unrecognized key %q", k)
		}
	}
	if rawPLC, ok := conn["plc"]; ok {
		block, ok := rawPLC.(map[string]any)
		if !ok {
			return out, fmt.Errorf("connection.plc must be a map")
		}
		if err := parsePLCBlock(&out, block); err != nil {
			return out, err
		}
	}
	return out, nil
}

func parsePLCBlock(out *acsConn, m map[string]any) error {
	if out.dswStart > 90 || 94 >= out.dswStart+out.dswLen {
		return fmt.Errorf("connection.plc requires %%R90-%%R94 inside the DSW block")
	}
	for k := range m {
		switch k {
		case "cabinet_id", "program_crc", "config_crc", "watchdog_stall":
		default:
			return fmt.Errorf("connection.plc: unrecognized key %q", k)
		}
	}
	if v, ok := m["cabinet_id"]; ok {
		id, err := asInt(v)
		if err != nil || id < 1 || id > 63 {
			return fmt.Errorf("connection.plc.cabinet_id must be an integer 1..63")
		}
		out.plc.hasCabinetID = true
		out.plc.cabinetID = id
	}
	if v, ok := m["program_crc"]; ok {
		crc, err := parseCRC(v)
		if err != nil {
			return fmt.Errorf("connection.plc.program_crc: %w", err)
		}
		out.plc.hasProgramCRC = true
		out.plc.programCRC = crc
	}
	if v, ok := m["config_crc"]; ok {
		crc, err := parseCRC(v)
		if err != nil {
			return fmt.Errorf("connection.plc.config_crc: %w", err)
		}
		out.plc.hasConfigCRC = true
		out.plc.configCRC = crc
	}
	if v, ok := m["watchdog_stall"]; ok {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("connection.plc.watchdog_stall must be a duration string")
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return fmt.Errorf("connection.plc.watchdog_stall must be a duration >= 0")
		}
		out.plc.watchdogStall = d
	}
	return nil
}

func parseCRC(v any) (uint32, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("must be a hex string")
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" || len(s) > 8 {
		return 0, fmt.Errorf("must be a 32-bit hex value")
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("must be a 32-bit hex value")
	}
	return uint32(n), nil
}

func optionalAddr(m map[string]any, key string, start, length int) (int, error) {
	if _, ok := m[key]; !ok {
		return 0, nil
	}
	v, err := asInt(m[key])
	if err != nil || v < start || v >= start+length {
		return 0, fmt.Errorf("connection.cabinet.%s must fall inside the DSW block", key)
	}
	return v, nil
}

func parseGateKind(v any) (model.GateKind, error) {
	s, _ := v.(string)
	switch s {
	case "warning":
		return model.GateKindWarning, nil
	case "barrier":
		return model.GateKindBarrier, nil
	case "gate":
		// ACS 1A Cameleon export uses bare "gate" instead of warning/barrier.
		return model.GateKindUnknown, nil
	case "", "unknown":
		return model.GateKindUnknown, nil
	default:
		return 0, fmt.Errorf("kind %q: want warning|barrier|gate", s)
	}
}

// asInt accepts YAML ints decoded as int or (from some loaders) int64/float64.
func asInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("not an integer: %v", v)
		}
		return int(n), nil
	case uint64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("not an integer: %T", v)
	}
}
