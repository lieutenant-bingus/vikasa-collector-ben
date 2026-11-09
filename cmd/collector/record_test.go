package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/Vikasa2M/vikasa-collector/internal/vendors/ntcip"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmpfixture"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// A controller with two detector channels: the detector table GET is derived
// from the first GET's answer, which a recorder working from a static OID
// list would miss.
var ascInts = map[string]int64{
	".1.3.6.1.4.1.1206.4.2.1.2.7.0":     4, // operation: flash
	".1.3.6.1.4.1.1206.4.2.1.2.5.0":     2, // flash: conflict monitor
	".1.3.6.1.4.1.1206.4.2.1.3.2.0":     3,
	".1.3.6.1.4.1.1206.4.2.1.6.5.0":     0,
	".1.3.6.1.4.1.1206.4.2.1.5.1.0":     0,
	".1.3.6.1.4.1.1206.4.2.1.2.3.0":     2, // two detector channels
	".1.3.6.1.4.1.1206.4.2.1.2.4.1.2.1": 17,
	".1.3.6.1.4.1.1206.4.2.1.2.4.1.4.1": 120,
	".1.3.6.1.4.1.1206.4.2.1.2.4.1.2.2": 9,
	// channel 2 occupancy unanswered
}

var dmsInts = map[string]int64{
	".1.3.6.1.4.1.1206.4.2.3.6.1.0":        4,
	".1.3.6.1.4.1.1206.4.2.3.6.7.0":        8,
	".1.3.6.1.4.1.1206.4.2.3.6.17.0":       2,
	".1.3.6.1.4.1.1206.4.2.3.6.18.0":       2,
	".1.3.6.1.4.1.1206.4.2.3.6.19.0":       0,
	".1.3.6.1.4.1.1206.4.2.3.7.2.0":        65535,
	".1.3.6.1.4.1.1206.4.2.3.7.3.0":        0,
	".1.3.6.1.4.1.1206.4.2.3.7.4.0":        16,
	".1.3.6.1.4.1.1206.4.2.3.7.5.0":        8,
	".1.3.6.1.4.1.1206.4.2.3.9.6.0":        0,
	".1.3.6.1.4.1.1206.4.2.3.9.7.36.1.3.1": 27,
	".1.3.6.1.4.1.1206.4.2.3.9.7.36.1.3.2": 32,
	".1.3.6.1.4.1.1206.4.2.3.9.7.32.0":     0,
}

const oidMULTI = ".1.3.6.1.4.1.1206.4.2.3.5.8.1.3.5.1"

func dmsOctets(multi string) map[string][]byte {
	return map[string][]byte{
		".1.3.6.1.4.1.1206.4.2.3.6.5.0": {3, 0, 3, 0x29, 0x2e},
		oidMULTI:                        []byte(multi),
	}
}

// writeConfig writes a collector.yaml with one device at addr.
func writeConfig(t *testing.T, kind, addr, community string) string {
	t.Helper()
	cfg := fmt.Sprintf(`
collector_id: cabinet-042-collector
region: us-ga
agency: metro
agency_unit: d01
site: cabinet-042
model_version: openits/v1
devices:
  - id: %s-1
    vendor: ntcip
    device_kind: %s
    connection: { snmp: { address: %q, community: %q } }
`, kind, kind, addr, community)
	path := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func record(t *testing.T, cfgPath, out string, extra ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := append([]string{"record-fixture", "-config", cfgPath, "-out", out,
		"-make", "Acme", "-model", "X-1", "-firmware", "2.3", "-timeout", "5s"}, extra...)
	err := run(t.Context(), args, &stdout, &stderr)
	return stdout.String() + stderr.String(), err
}

// The recording is only worth committing if replaying it gives the adapter
// the poll the device gave it: same facets, same facet errors.
func TestRecordedFixtureReplaysAsTheDevicePolled(t *testing.T) {
	cases := []struct {
		kind, version string
		ints          map[string]int64
		octets        map[string][]byte
		build         func(string, snmp.Client) adapter.StateReader
	}{
		{"asc", "v2c", ascInts, nil, ntcip.NewASC},
		{"dms", "v1", dmsInts, dmsOctets("[jl3]LEFT[nl]LANE CLOSED"), ntcip.NewDMS},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			addr := snmptest.ServeAgent(t, "s3cret", c.ints, c.octets)
			out := filepath.Join(t.TempDir(), c.kind+".json")
			if log, err := record(t, writeConfig(t, c.kind, addr, "s3cret"), out, "-note", "test"); err != nil {
				t.Fatalf("record: %v\n%s", err, log)
			}

			f, err := snmpfixture.Read(out)
			if err != nil {
				t.Fatal(err)
			}
			if p := f.Provenance; p.Adapter != "ntcip-"+c.kind || p.SNMPVersion != c.version || p.Make != "Acme" ||
				p.Recorded != time.Now().UTC().Format(time.DateOnly) || !strings.HasPrefix(p.RecordedBy, "collector record-fixture ") {
				t.Fatalf("provenance %+v", p)
			}

			live, err := snmp.Dial(snmp.DialConfig{Address: addr, Community: "s3cret", Version: c.version, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			want, err := c.build(c.kind+"-1", live).Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.build(c.kind+"-1", snmptest.LoadFixture(t, out)).Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(model.Snapshot{}, "SampledAt")); diff != "" {
				t.Fatalf("replay differs from the live poll (-live +replay):\n%s", diff)
			}
			if len(want.Facets) == 0 {
				t.Fatal("the live poll decoded nothing; the comparison proves nothing")
			}

			raw, _ := os.ReadFile(out)
			if bytes.Contains(raw, []byte("s3cret")) || bytes.Contains(raw, []byte("127.0.0.1")) {
				t.Fatalf("the fixture carries the community or the address:\n%s", raw)
			}
		})
	}
}

func TestRecordingKeepsWhatWasNotAnswered(t *testing.T) {
	addr := snmptest.ServeAgent(t, "public", ascInts, nil)
	out := filepath.Join(t.TempDir(), "asc.json")
	if log, err := record(t, writeConfig(t, "asc", addr, "public"), out); err != nil {
		t.Fatalf("record: %v\n%s", err, log)
	}
	f, err := snmpfixture.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.Unanswered, ".1.3.6.1.4.1.1206.4.2.1.2.4.1.4.2") {
		t.Fatalf("unanswered = %v; want channel 2 occupancy", f.Unanswered)
	}
}

// A value naming the deployment stops the recording until the operator says
// which OIDs to redact; then the file says which values are synthetic.
func TestRecordingRefusesIdentifyingValuesUntilRedacted(t *testing.T) {
	addr := snmptest.ServeAgent(t, "public", dmsInts, dmsOctets("CABINET-042 AHEAD"))
	cfgPath := writeConfig(t, "dms", addr, "public")
	out := filepath.Join(t.TempDir(), "dms.json")

	log, err := record(t, cfgPath, out)
	if err == nil || !strings.Contains(err.Error(), oidMULTI+" contains the site") {
		t.Fatalf("err = %v; want the site named in %s refused\n%s", err, oidMULTI, log)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a refused recording must write nothing")
	}

	if log, err := record(t, cfgPath, out, "-redact", oidMULTI); err != nil {
		t.Fatalf("record with -redact: %v\n%s", err, log)
	}
	f, err := snmpfixture.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if f.Octets[oidMULTI].Text != "REDACTED" || !slices.Equal(f.Redacted, []string{oidMULTI}) {
		t.Fatalf("octet %+v, redacted %v", f.Octets[oidMULTI], f.Redacted)
	}
}

func TestRecordingAnUnreachableDeviceWritesNothing(t *testing.T) {
	addr := snmptest.ServeAgent(t, "other", dmsInts, nil) // wrong community: never answers
	out := filepath.Join(t.TempDir(), "dms.json")
	if _, err := record(t, writeConfig(t, "dms", addr, "public"), out, "-timeout", "1s"); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed poll must write nothing")
	}
}

func TestRecordFixtureUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"record-fixture", "-config", "x.yaml"}, &stdout, &stderr)
	if exitCode(err) != 2 || !strings.Contains(err.Error(), "-out is required") {
		t.Fatalf("err = %v (exit %d); want a usage error naming the first missing flag", err, exitCode(err))
	}
}

// A secret is a secret at any length: the short-value skip that keeps a
// three-letter agency unit from matching binary data must not let a short
// community through.
func TestRecordingRefusesAShortCommunity(t *testing.T) {
	addr := snmptest.ServeAgent(t, "k9x", dmsInts, dmsOctets("USE K9X"))
	out := filepath.Join(t.TempDir(), "dms.json")
	_, err := record(t, writeConfig(t, "dms", addr, "k9x"), out)
	if err == nil || !strings.Contains(err.Error(), oidMULTI+" contains the community") {
		t.Fatalf("err = %v; want the community in %s refused", err, oidMULTI)
	}
}
