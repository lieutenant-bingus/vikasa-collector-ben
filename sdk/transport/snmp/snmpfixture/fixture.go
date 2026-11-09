// Package snmpfixture is the recorded-fixture file format for SNMP adapters
// (ADR 0008): what an adapter's Read asked a real device, what the device
// answered, and where the recording came from. The collector's record-fixture
// mode writes it; snmptest.LoadFixture replays it in a golden test.
package snmpfixture

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// Fixture is one recorded poll. Ints and Octets hold exactly what the device
// answered, keyed by numeric OID with a leading dot — the maps snmptest.Static
// replays. An OID the adapter asked for that the device did not answer is in
// Unanswered and in neither map, which is how Static replays "no such object".
type Fixture struct {
	Provenance Provenance       `json:"provenance"`
	Ints       map[string]int64 `json:"ints"`
	Octets     map[string]Octet `json:"octets"`
	Unanswered []string         `json:"unanswered,omitempty"`
	// Redacted lists OIDs whose recorded value was replaced before the file
	// was written, because it identified the deployment. Their values are
	// synthetic.
	Redacted []string `json:"redacted,omitempty"`
}

// Provenance says where a recording came from. Every field but Note is
// required: a fixture that cannot say which device it describes is not
// evidence of anything.
type Provenance struct {
	Adapter     string `json:"adapter"`      // registry key, e.g. "ntcip-dms"
	Make        string `json:"make"`         // device manufacturer
	Model       string `json:"model"`        // device model
	Firmware    string `json:"firmware"`     // firmware version as the device reports it
	SNMPVersion string `json:"snmp_version"` // "v1" or "v2c", as dialled
	Recorded    string `json:"recorded"`     // UTC date, YYYY-MM-DD
	RecordedBy  string `json:"recorded_by"`  // the tool and its version
	Note        string `json:"note,omitempty"`
}

// Octet is an OCTET STRING value. Hex is the value. Text repeats it as a
// string when every byte is printable, for a reviewer's benefit; a file whose
// Text disagrees with its Hex was edited by hand and fails to load.
type Octet struct {
	Hex  string `json:"hex"`
	Text string `json:"text,omitempty"`
}

// NewOctet encodes b, adding Text when b is printable.
func NewOctet(b []byte) Octet {
	o := Octet{Hex: hex.EncodeToString(b)}
	if printable(b) {
		o.Text = string(b)
	}
	return o
}

// Bytes decodes the value, checking Text against it.
func (o Octet) Bytes() ([]byte, error) {
	b, err := hex.DecodeString(o.Hex)
	if err != nil {
		return nil, fmt.Errorf("hex: %w", err)
	}
	if o.Text != "" && o.Text != string(b) {
		return nil, fmt.Errorf("text %q does not match hex %s", o.Text, o.Hex)
	}
	return b, nil
}

func printable(b []byte) bool {
	if len(b) == 0 || !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Read loads and validates a fixture file.
func Read(path string) (Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var f Fixture
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Fixture{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return Fixture{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Write validates f and writes it to path, indented and with sorted keys, so
// a re-recording diffs line by line against the last one.
func Write(path string, f Fixture) error {
	if err := f.Validate(); err != nil {
		return err
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644) //nolint:gosec // a fixture is meant to be committed and read
}

// Validate checks provenance is complete and every value decodes.
func (f Fixture) Validate() error {
	p := f.Provenance
	var errs []error
	for name, v := range map[string]string{
		"adapter": p.Adapter, "make": p.Make, "model": p.Model, "firmware": p.Firmware,
		"snmp_version": p.SNMPVersion, "recorded": p.Recorded, "recorded_by": p.RecordedBy,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("provenance.%s is required", name))
		}
	}
	if _, err := time.Parse(time.DateOnly, p.Recorded); p.Recorded != "" && err != nil {
		errs = append(errs, fmt.Errorf("provenance.recorded %q is not YYYY-MM-DD", p.Recorded))
	}
	for oid := range f.Ints {
		if !strings.HasPrefix(oid, ".") {
			errs = append(errs, fmt.Errorf("ints: OID %q needs a leading dot", oid))
		}
		if _, dup := f.Octets[oid]; dup {
			errs = append(errs, fmt.Errorf("OID %s is in both ints and octets", oid))
		}
	}
	for oid, o := range f.Octets {
		if !strings.HasPrefix(oid, ".") {
			errs = append(errs, fmt.Errorf("octets: OID %q needs a leading dot", oid))
		}
		if _, err := o.Bytes(); err != nil {
			errs = append(errs, fmt.Errorf("octets %s: %w", oid, err))
		}
	}
	for _, oid := range f.Unanswered {
		_, isInt := f.Ints[oid]
		_, isOct := f.Octets[oid]
		if isInt || isOct {
			errs = append(errs, fmt.Errorf("OID %s is both answered and unanswered", oid))
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
}

// OctetBytes returns Octets decoded. It assumes f is valid.
func (f Fixture) OctetBytes() map[string][]byte {
	out := make(map[string][]byte, len(f.Octets))
	for oid, o := range f.Octets {
		out[oid], _ = o.Bytes()
	}
	return out
}

// Recorder is an snmp.Client that passes every call through to a real one
// and remembers what was asked and answered.
type Recorder struct {
	inner snmp.Client

	mu     sync.Mutex
	asked  map[string]bool
	ints   map[string]int64
	octets map[string][]byte
}

// NewRecorder wraps c.
func NewRecorder(c snmp.Client) *Recorder {
	return &Recorder{inner: c, asked: map[string]bool{}, ints: map[string]int64{}, octets: map[string][]byte{}}
}

var _ snmp.Client = (*Recorder)(nil)

// Get records through GetAll, as the real client's Get is GetAll's integers:
// an OID answered with an octet string is recorded as such, not as missing.
func (r *Recorder) Get(ctx context.Context, oids []string) (map[string]int64, error) {
	v, err := r.GetAll(ctx, oids)
	if err != nil {
		return nil, err
	}
	return v.Ints, nil
}

// GetAll passes the call through. A failed call records nothing: it says
// nothing about what the device holds.
func (r *Recorder) GetAll(ctx context.Context, oids []string) (snmp.Values, error) {
	v, err := r.inner.GetAll(ctx, oids)
	if err != nil {
		return v, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, oid := range oids {
		r.asked[oid] = true
	}
	maps.Copy(r.ints, v.Ints)
	for oid, b := range v.Octets {
		r.octets[oid] = bytes.Clone(b)
	}
	return v, nil
}

func (r *Recorder) Close() error { return r.inner.Close() }

// Fixture returns what was recorded, with p as its provenance.
func (r *Recorder) Fixture(p Provenance) Fixture {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := Fixture{Provenance: p, Ints: maps.Clone(r.ints), Octets: map[string]Octet{}}
	for oid, b := range r.octets {
		f.Octets[oid] = NewOctet(b)
	}
	for _, oid := range slices.Sorted(maps.Keys(r.asked)) {
		_, isInt := r.ints[oid]
		_, isOct := r.octets[oid]
		if !isInt && !isOct {
			f.Unanswered = append(f.Unanswered, oid)
		}
	}
	return f
}
