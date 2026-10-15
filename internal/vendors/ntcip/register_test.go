package ntcip

import (
	"strings"
	"testing"
)

// The connection block is opaque to config.Load, so this parser is the only
// place ADR 0014's "boot fails on the unrecognized" can be enforced for it.
// Each case is a value the operator wrote that, before this test, booted on
// a substituted default instead: `version: 1` (YAML int) ran as v2c, a
// non-string community ran as "public".
func TestParseSNMPBlockRejectsUnrecognized(t *testing.T) {
	cases := []struct {
		name string
		snmp map[string]any
		want string
	}{
		{"address not a string", map[string]any{"address": 161}, "address"},
		{"community not a string", map[string]any{"address": "h:161", "community": 1234}, "community"},
		{"version not a string", map[string]any{"address": "h:161", "version": 1}, "version"},
		{"unknown key", map[string]any{"address": "h:161", "comunity": "private"}, "comunity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSNMPBlock(map[string]any{"snmp": tc.snmp})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseSNMPBlock() error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestParseSNMPBlockRejectsUnknownConnectionKey(t *testing.T) {
	conn := map[string]any{
		"snmp":    map[string]any{"address": "h:161"},
		"timeout": "5s",
	}
	_, err := parseSNMPBlock(conn)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("parseSNMPBlock() error = %v, want one naming %q", err, "timeout")
	}
}

func TestParseSNMPBlockDefaults(t *testing.T) {
	cfg, err := parseSNMPBlock(map[string]any{"snmp": map[string]any{"address": "h:161"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Community != "public" || cfg.Version != "" {
		t.Fatalf("defaults = community %q version %q, want \"public\" and unset", cfg.Community, cfg.Version)
	}
}
