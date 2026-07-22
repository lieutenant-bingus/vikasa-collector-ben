package ntcip

import (
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// RegisterTo registers ntcip-asc. The connection block is
//
//	connection:
//	  snmp: { address: "host:161", community: "public" }
func RegisterTo(r *adapter.Registry) {
	r.Register(ascDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseSNMPBlock(conn)
		if err != nil {
			return nil, fmt.Errorf("ntcip-asc %s: %w", deviceID, err)
		}
		c, err := snmp.Dial(cfg)
		if err != nil {
			return nil, err
		}
		return NewASC(deviceID, c), nil
	})
}

func parseSNMPBlock(conn map[string]any) (snmp.DialConfig, error) {
	raw, ok := conn["snmp"].(map[string]any)
	if !ok {
		return snmp.DialConfig{}, fmt.Errorf("connection.snmp block required")
	}
	addr, _ := raw["address"].(string)
	if addr == "" {
		return snmp.DialConfig{}, fmt.Errorf("connection.snmp.address required")
	}
	community, _ := raw["community"].(string)
	if community == "" {
		community = "public"
	}
	return snmp.DialConfig{Address: addr, Community: community, Timeout: 2 * time.Second, Retries: 1}, nil
}
