package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmpfixture"
)

// recordFixture is `collector record-fixture`: one poll of one configured
// device by its real adapter, written as a fixture for that adapter's golden
// tests (ADR 0008). The adapter is built through the same registry and
// connection block the collector polls with, and the recording is made by
// wrapping the SNMP client it dials, so the fixture holds exactly the GETs
// that adapter makes — including any it derives from an earlier answer.
//
// It lives in the shipped binary so the work at the device is running a
// binary already on the cabinet, with the config already there: the
// community never goes on a command line.
func recordFixture(ctx context.Context, args []string, stdout, stderr io.Writer, ver string) error {
	fs := flag.NewFlagSet("collector record-fixture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "path to collector.yaml naming the device (required)")
	deviceID := fs.String("device", "", "id of the device to record; may be omitted when the config has one device")
	out := fs.String("out", "", "fixture file to write, e.g. internal/vendors/<vendor>/testdata/<kind>-<state>.json (required)")
	mk := fs.String("make", "", "device manufacturer (required)")
	mdl := fs.String("model", "", "device model (required)")
	firmware := fs.String("firmware", "", "firmware version as the device reports it (required)")
	note := fs.String("note", "", "what state the device was in, e.g. \"flash, door open\"")
	timeout := fs.Duration("timeout", 30*time.Second, "time allowed for the poll")
	var redact oidList
	fs.Var(&redact, "redact", "OID whose octet-string value identifies the deployment; replaced before writing (repeatable)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: collector record-fixture -config collector.yaml -device <id> -make <m> -model <m> -firmware <v> -out <file.json>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err}
	}
	for _, req := range []struct{ name, v string }{
		{"-config", *cfgPath}, {"-out", *out}, {"-make", *mk}, {"-model", *mdl}, {"-firmware", *firmware},
	} {
		if strings.TrimSpace(req.v) == "" {
			return usageError{fmt.Errorf("%s is required", req.name)}
		}
	}

	reg := adapter.NewRegistry()
	RegisterAdapters(reg)
	cfg, err := config.Load(*cfgPath, reg)
	if err != nil {
		return err
	}
	dev, err := pickDevice(cfg, *deviceID)
	if err != nil {
		return usageError{err}
	}

	var (
		rec    *snmpfixture.Recorder
		dialed snmp.DialConfig
	)
	undo := snmp.InterceptDial(func(c snmp.DialConfig, inner snmp.Client) snmp.Client {
		rec, dialed = snmpfixture.NewRecorder(inner), c
		return rec
	})
	a, err := reg.Build(dev.Vendor, dev.DeviceKind, dev.ID, dev.Connection)
	undo()
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	key := adapter.Descriptor{Vendor: dev.Vendor, DeviceKind: dev.DeviceKind}.Key()
	if rec == nil {
		return fmt.Errorf("%s does not use the SNMP transport; this recorder records SNMP only", key)
	}
	reader, ok := a.(adapter.StateReader)
	if !ok {
		return fmt.Errorf("%s is not a StateReader; there is no poll to record", key)
	}

	pollCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	snap, err := reader.Read(pollCtx)
	if err != nil {
		return fmt.Errorf("poll %s: %w (nothing written)", dev.ID, err)
	}

	snmpVersion, err := versionName(dialed.Version)
	if err != nil {
		return err
	}
	f := rec.Fixture(snmpfixture.Provenance{
		Adapter: key, Make: *mk, Model: *mdl, Firmware: *firmware,
		SNMPVersion: snmpVersion,
		Recorded:    time.Now().UTC().Format(time.DateOnly),
		RecordedBy:  "collector record-fixture " + ver,
		Note:        *note,
	})
	if err := scrub(&f, redact, identifying(cfg, dev, dialed)); err != nil {
		return fmt.Errorf("%w (nothing written)", err)
	}
	if err := snmpfixture.Write(*out, f); err != nil {
		return err
	}
	report(stdout, *out, f, snap)
	return nil
}

func pickDevice(cfg *config.Config, id string) (config.Device, error) {
	if id == "" {
		if len(cfg.Devices) == 1 {
			return cfg.Devices[0], nil
		}
		return config.Device{}, fmt.Errorf("-device is required: the config has %d devices", len(cfg.Devices))
	}
	for _, d := range cfg.Devices {
		if d.ID == id {
			return d, nil
		}
	}
	return config.Device{}, fmt.Errorf("no device %q in the config", id)
}

func versionName(v string) (string, error) {
	parsed, err := snmp.ParseVersion(v)
	if err != nil {
		return "", err
	}
	if parsed == gosnmp.Version1 {
		return "v1", nil
	}
	return "v2c", nil
}

// identifying is what the recording must not carry: the device's address,
// a non-default community, and the deployment's own names. Names shorter
// than four bytes are skipped — three bytes match binary data by chance —
// but the address and community are checked at any length: they are
// secrets, and a false refusal costs one -redact.
func identifying(cfg *config.Config, dev config.Device, dialed snmp.DialConfig) map[string]string {
	out := map[string]string{
		"site": cfg.Site, "agency": cfg.Agency, "agency_unit": cfg.AgencyUnit,
		"collector_id": cfg.CollectorID, "device id": dev.ID,
	}
	maps.DeleteFunc(out, func(_, v string) bool { return len(v) < 4 })
	if host, _, err := net.SplitHostPort(dialed.Address); err == nil && host != "" {
		out["device address"] = host
	}
	if dialed.Community != "" && dialed.Community != "public" {
		out["community"] = dialed.Community
	}
	return out
}

// scrub replaces each -redact OID's value, then refuses the recording if any
// value still contains something that identifies the deployment. A name in a
// sign message or sysLocation is not something a tool can judge, so the
// operator decides, per OID, and the file records which values are synthetic.
func scrub(f *snmpfixture.Fixture, redact []string, identifying map[string]string) error {
	for _, oid := range redact {
		if _, ok := f.Octets[oid]; !ok {
			return fmt.Errorf("-redact %s: the device did not answer that OID with an octet string", oid)
		}
		f.Octets[oid] = snmpfixture.NewOctet([]byte("REDACTED"))
		f.Redacted = append(f.Redacted, oid)
	}
	slices.Sort(f.Redacted)

	var found []string
	for _, oid := range slices.Sorted(maps.Keys(f.Octets)) {
		b, _ := f.Octets[oid].Bytes()
		for _, what := range slices.Sorted(maps.Keys(identifying)) {
			if bytes.Contains(bytes.ToLower(b), []byte(strings.ToLower(identifying[what]))) {
				found = append(found, fmt.Sprintf("%s contains the %s", oid, what))
			}
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("the recording identifies the deployment: %s; re-run with -redact for each OID", strings.Join(found, "; "))
	}
	return nil
}

// report shows what was written and what the adapter made of it, so the
// person at the device can see the poll decoded before leaving the cabinet.
func report(w io.Writer, path string, f snmpfixture.Fixture, snap *model.Snapshot) {
	_, _ = fmt.Fprintf(w, "wrote %s: %d integer and %d octet-string values, %d unanswered, %d redacted\n",
		path, len(f.Ints), len(f.Octets), len(f.Unanswered), len(f.Redacted))
	for _, oid := range slices.Sorted(maps.Keys(f.Octets)) {
		if t := f.Octets[oid].Text; t != "" {
			_, _ = fmt.Fprintf(w, "  text %s = %q\n", oid, t)
		}
	}
	for _, facet := range snap.Facets {
		_, _ = fmt.Fprintf(w, "facet %s: %+v\n", facet.FacetKind(), facet)
	}
	for _, e := range snap.Errors {
		_, _ = fmt.Fprintf(w, "facet %s failed: %s\n", e.Kind, e.Err)
	}
}

// oidList is a repeatable flag.
type oidList []string

func (l *oidList) String() string { return strings.Join(*l, ",") }

func (l *oidList) Set(v string) error {
	if !strings.HasPrefix(v, ".") {
		return fmt.Errorf("OID %q needs a leading dot, as the fixture keys it", v)
	}
	*l = append(*l, v)
	return nil
}
