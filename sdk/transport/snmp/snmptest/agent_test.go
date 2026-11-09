package snmptest

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

const (
	oidInt   = ".1.3.6.1.4.1.1206.4.2.3.6.1.0"
	oidOctet = ".1.3.6.1.4.1.1206.4.2.3.6.5.0"
	oidEmpty = ".1.3.6.1.4.1.1206.4.2.3.6.6.0"
	oidNone  = ".1.3.6.1.4.1.1206.4.2.3.6.99.0"
)

func dialAgent(t *testing.T, version, community string) snmp.Client {
	t.Helper()
	addr := ServeAgent(t, "public",
		map[string]int64{oidInt: 4},
		map[string][]byte{oidOctet: {3, 0, 3, 0x29, 0x2e}, oidEmpty: {}})
	c, err := snmp.Dial(snmp.DialConfig{Address: addr, Community: community, Version: version, Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestAgentAnswersThroughTheRealClient(t *testing.T) {
	for _, version := range []string{"v1", "v2c"} {
		t.Run(version, func(t *testing.T) {
			got, err := dialAgent(t, version, "public").GetAll(t.Context(), []string{oidInt, oidOctet, oidEmpty})
			if err != nil {
				t.Fatal(err)
			}
			want := snmp.Values{
				Ints:   map[string]int64{oidInt: 4},
				Octets: map[string][]byte{oidOctet: {3, 0, 3, 0x29, 0x2e}, oidEmpty: {}},
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

// An unknown OID costs v2c one varbind and v1 the whole request — the
// difference ntcip-dms's separate humidity GET exists for.
func TestAgentUnknownOIDPerVersion(t *testing.T) {
	got, err := dialAgent(t, "v2c", "public").GetAll(t.Context(), []string{oidInt, oidNone})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Ints[oidNone]; ok || got.Ints[oidInt] != 4 {
		t.Fatalf("v2c: got %+v; want only the known OID answered", got)
	}

	got, err = dialAgent(t, "v1", "public").GetAll(t.Context(), []string{oidInt, oidNone})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ints)+len(got.Octets) != 0 {
		t.Fatalf("v1: got %+v; want nothing answered (noSuchName fails the request)", got)
	}
}

func TestAgentIgnoresTheWrongCommunity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if got, err := dialAgent(t, "v2c", "private").GetAll(ctx, []string{oidInt}); err == nil {
		t.Fatalf("got %+v with the wrong community; want a timeout", got)
	}
}
