package snmpfixture_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	. "github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmpfixture"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

func provenance() Provenance {
	return Provenance{
		Adapter: "ntcip-dms", Make: "Acme", Model: "VMS-1", Firmware: "1.0",
		SNMPVersion: "v1", Recorded: "2026-10-21", RecordedBy: "collector record-fixture test",
	}
}

func TestRecorderKeepsWhatWasAskedAndAnswered(t *testing.T) {
	r := NewRecorder(&snmptest.Static{
		Values: map[string]int64{".1.1": 4},
		Octets: map[string][]byte{".1.2": []byte("LANE CLOSED"), ".1.3": {3, 0, 3}},
	})
	if _, err := r.GetAll(t.Context(), []string{".1.1", ".1.2", ".1.9"}); err != nil {
		t.Fatal(err)
	}
	// Get is how ntcip-asc reads; an octet answer to it is still recorded.
	if _, err := r.Get(t.Context(), []string{".1.3"}); err != nil {
		t.Fatal(err)
	}
	got := r.Fixture(provenance())
	want := Fixture{
		Provenance: provenance(),
		Ints:       map[string]int64{".1.1": 4},
		Octets: map[string]Octet{
			".1.2": {Hex: "4c414e4520434c4f534544", Text: "LANE CLOSED"},
			".1.3": {Hex: "030003"},
		},
		Unanswered: []string{".1.9"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
}

// A failed GET says nothing about what the device holds, so the OIDs it
// asked for are not "unanswered".
func TestRecorderIgnoresAFailedCall(t *testing.T) {
	r := NewRecorder(&snmptest.Static{Err: errors.New("timeout")})
	if _, err := r.GetAll(t.Context(), []string{".1.1"}); err == nil {
		t.Fatal("want the inner error")
	}
	if f := r.Fixture(provenance()); len(f.Unanswered)+len(f.Ints)+len(f.Octets) != 0 {
		t.Fatalf("recorded %+v from a failed call", f)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	f := Fixture{
		Provenance: provenance(),
		Ints:       map[string]int64{".1.1": -5},
		Octets:     map[string]Octet{".1.2": NewOctet([]byte{0, 0xff}), ".1.3": NewOctet(nil)},
		Unanswered: []string{".1.9"},
	}
	if err := Write(path, f); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(f, got); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
	if b := got.OctetBytes()[".1.3"]; b == nil || len(b) != 0 {
		t.Fatalf("empty octet string came back as %#v; an answered empty value must stay present", b)
	}
}

func TestReadRejects(t *testing.T) {
	valid := `{"provenance":{"adapter":"ntcip-dms","make":"Acme","model":"VMS-1","firmware":"1.0","snmp_version":"v1","recorded":"2026-10-21","recorded_by":"t"},"ints":{".1.1":4},"octets":{".1.2":{"hex":"4142","text":"AB"}}}`
	cases := map[string]struct{ json, want string }{
		"no firmware":      {strings.Replace(valid, `"firmware":"1.0"`, `"firmware":""`, 1), "provenance.firmware is required"},
		"bad date":         {strings.Replace(valid, `2026-10-21`, `21/10/2026`, 1), "not YYYY-MM-DD"},
		"hand-edited text": {strings.Replace(valid, `"text":"AB"`, `"text":"AC"`, 1), "does not match hex"},
		"bad hex":          {strings.Replace(valid, `"hex":"4142"`, `"hex":"41z2"`, 1), "hex"},
		"no leading dot":   {strings.Replace(valid, `".1.1":4`, `"1.1":4`, 1), "leading dot"},
		"unknown field":    {strings.Replace(valid, `"ints"`, `"extra":1,"ints"`, 1), "unknown field"},
		"in both maps":     {strings.Replace(valid, `".1.1":4`, `".1.2":4`, 1), "both ints and octets"},
		"answered and not": {strings.Replace(valid, `}}}`, `}},"unanswered":[".1.1"]}`, 1), "both answered and unanswered"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.json")
			if err := os.WriteFile(path, []byte(c.json), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(path); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Read = %v; want an error containing %q", err, c.want)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err != nil {
		t.Fatalf("the valid base case must load: %v", err)
	}
}
