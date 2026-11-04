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
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpfixture"
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
	events := fs.Bool("events", false, "also record one read of the device's event log (adapters with CapEvents, HTTP only)")
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
	var (
		httpRec    *httpfixture.Recorder
		httpDialed httpapi.DialConfig
	)
	undo := snmp.InterceptDial(func(c snmp.DialConfig, inner snmp.Client) snmp.Client {
		rec, dialed = snmpfixture.NewRecorder(inner), c
		return rec
	})
	undoHTTP := httpapi.InterceptDial(func(c httpapi.DialConfig, inner httpapi.Client) httpapi.Client {
		httpRec, httpDialed = httpfixture.NewRecorder(inner), c
		return httpRec
	})
	a, err := reg.Build(dev.Vendor, dev.DeviceKind, dev.ID, dev.Connection)
	undo()
	undoHTTP()
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	key := adapter.Descriptor{Vendor: dev.Vendor, DeviceKind: dev.DeviceKind}.Key()
	if rec == nil && httpRec == nil {
		return fmt.Errorf("%s uses neither the SNMP nor the HTTP transport; this recorder records those only", key)
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

	if *events {
		er, ok := a.(adapter.EventReader)
		if !ok || !a.Descriptor().Caps.Has(adapter.CapEvents) || httpRec == nil {
			return fmt.Errorf("-events: %s on device %s does not read an event log over HTTP (nothing written)", key, dev.ID)
		}
		// A first read: the whole log, with no cursor, so nothing is filtered.
		if _, _, err := er.ReadEvents(pollCtx, nil); err != nil {
			return fmt.Errorf("event log %s: %w (nothing written)", dev.ID, err)
		}
	}

	if httpRec != nil {
		return writeHTTPFixture(stdout, *out, httpRec, httpDialed, cfg, dev, snap, httpfixture.Provenance{
			Adapter: key, Make: *mk, Model: *mdl, Firmware: *firmware,
			Recorded:   time.Now().UTC().Format(time.DateOnly),
			RecordedBy: "collector record-fixture " + ver,
			Note:       *note,
		})
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

// writeHTTPFixture finishes a recording made through the HTTP transport. The
// recorder has already left out every header, request body and non-GET
// exchange; what is left is checked for the deployment's own names and
// address the same way an SNMP recording is. There is no per-exchange
// -redact: replacing a JSON or XML body would leave the adapter nothing to
// parse, so a recording that names the deployment is refused and the
// operator records a device state that does not.
func writeHTTPFixture(w io.Writer, path string, rec *httpfixture.Recorder, dialed httpapi.DialConfig, cfg *config.Config, dev config.Device, snap *model.Snapshot, p httpfixture.Provenance) error {
	ident := identifying(cfg, dev, snmp.DialConfig{})
	if u, err := url.Parse(dialed.BaseURL); err == nil {
		p.Scheme = u.Scheme
		if h := u.Hostname(); h != "" {
			ident["device address"] = h
		}
	}
	f := rec.Fixture(p)
	var found []string
	for _, k := range slices.Sorted(maps.Keys(f.Exchanges)) {
		body := strings.ToLower(f.Exchanges[k].Body)
		for _, what := range slices.Sorted(maps.Keys(ident)) {
			if strings.Contains(body, strings.ToLower(ident[what])) {
				found = append(found, fmt.Sprintf("%s contains the %s", k, what))
			}
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("the recording identifies the deployment: %s (nothing written)", strings.Join(found, "; "))
	}
	if err := httpfixture.Write(path, f); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "wrote %s: %d exchanges, %d unrecorded (non-GET)\n", path, len(f.Exchanges), len(f.Unrecorded))
	for _, facet := range snap.Facets {
		_, _ = fmt.Fprintf(w, "facet %s: %+v\n", facet.FacetKind(), facet)
	}
	for _, e := range snap.Errors {
		_, _ = fmt.Fprintf(w, "facet %s failed: %s\n", e.Kind, e.Err)
	}
	return nil
}
