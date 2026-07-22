// Package snmptest provides the fixture-replay fake used by adapter golden
// tests (ADR 0008): recorded OID→value maps stand in for a live agent.
package snmptest

import (
	"context"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// Static replays a fixed OID→value map. OIDs absent from Values are
// omitted from Get results, mirroring a live agent's NoSuchObject.
type Static struct {
	Values map[string]int64
	Err    error

	// FailCall fails a specific Get call by its 1-based call number, while
	// earlier (and later, non-listed) calls succeed normally against Values.
	// This exists for adapters that issue multiple Gets per Read (e.g. a
	// scalar Get followed by a table Get) and need to exercise a partial-
	// failure path: one facet reads fine, a later facet's Get does not.
	// Err, when set, still fails EVERY call and takes precedence over
	// FailCall — existing single-Get fixtures are unaffected by this field.
	//
	// Example: FailCall: map[int]error{2: errors.New("timeout")} fails only
	// the second Get on this Static instance; the first succeeds.
	FailCall map[int]error

	calls int
}

// The fake must stay substitutable for the real client.
var _ snmp.Client = (*Static)(nil)

func (s *Static) Get(_ context.Context, oids []string) (map[string]int64, error) {
	s.calls++
	if s.Err != nil {
		return nil, s.Err
	}
	if err, ok := s.FailCall[s.calls]; ok {
		return nil, err
	}
	out := make(map[string]int64)
	for _, oid := range oids {
		if v, ok := s.Values[oid]; ok {
			out[oid] = v
		}
	}
	return out, nil
}

func (s *Static) Close() error { return nil }
