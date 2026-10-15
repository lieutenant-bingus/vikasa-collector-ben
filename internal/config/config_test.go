package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

func regWith(vendor, kind string) *adapter.Registry {
	r := adapter.NewRegistry()
	r.Register(adapter.Descriptor{Vendor: vendor, DeviceKind: kind, Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return nil, nil })
	return r
}

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// baseYAML omits collector_id deliberately: the required-field test needs a
// fixture that is valid in every other respect, so its failure can only be
// about the missing field.
const baseYAML = `
region: us-ga
agency: metro-atlanta
agency_unit: d01
site: cabinet-042
model_version: openits/v1
devices:
  - id: asc-1
    vendor: ntcip
    device_kind: asc
    poll_interval: 1s
    connection:
      snmp: { address: "10.0.0.12:161", community: public }
`

const validYAML = baseYAML + "collector_id: cabinet-042-collector\n"

func TestLoadValid(t *testing.T) {
	cfg, err := Load(write(t, validYAML), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agency != "metro-atlanta" || cfg.Site != "cabinet-042" || cfg.ModelVersion != "openits/v1" {
		t.Fatalf("header: %+v", cfg)
	}
	d := cfg.Devices[0]
	if d.ID != "asc-1" || d.Vendor != "ntcip" || d.DeviceKind != "asc" || d.PollInterval != time.Second {
		t.Fatalf("device: %+v", d)
	}
	snmp := d.Connection["snmp"].(map[string]any)
	if snmp["address"] != "10.0.0.12:161" {
		t.Fatalf("connection not preserved: %+v", d.Connection)
	}
}

func TestLoadDefaultsPollInterval(t *testing.T) {
	yaml := `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
devices:
  - { id: asc-1, vendor: ntcip, device_kind: asc, connection: {} }
`
	cfg, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].PollInterval != 5*time.Second {
		t.Fatalf("default poll_interval = %v, want 5s", cfg.Devices[0].PollInterval)
	}
}

func TestLoadRejects(t *testing.T) {
	reg := regWith("ntcip", "asc")
	cases := map[string]string{
		"unknown adapter": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
devices: [{ id: d1, vendor: acme, device_kind: asc, connection: {} }]`,
		"bad agency token": `
agency: Metro.Atlanta
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"missing model_version": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"duplicate device id": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
devices:
  - { id: d1, vendor: ntcip, device_kind: asc, connection: {} }
  - { id: d1, vendor: ntcip, device_kind: asc, connection: {} }`,
		"no devices": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
devices: []`,
	}
	for name, yaml := range cases {
		if _, err := Load(write(t, yaml), reg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadSubjectDefaults(t *testing.T) {
	cfg, err := Load(write(t, validYAML), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// No subject block: template empty (subject.New applies the default) and
	// the stream name matches the pre-template scheme.
	if cfg.Subject.Template != "" {
		t.Errorf("Template = %q, want empty", cfg.Subject.Template)
	}
}

func TestLoadSubjectCustom(t *testing.T) {
	yaml := `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
subject:
  template: "{prefix}.{geo}.{agency}.{service}.{event}.{version}"
  vars:
    prefix: traffic
    geo: southeast
devices:
  - { id: asc-1, vendor: ntcip, device_kind: asc, connection: {} }
`
	cfg, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Subject.Vars["geo"] != "southeast" {
		t.Errorf("vars not preserved: %+v", cfg.Subject.Vars)
	}
	sc := cfg.SubjectConfig()
	if sc.Template != "{prefix}.{geo}.{agency}.{service}.{event}.{version}" {
		t.Errorf("SubjectConfig().Template = %q", sc.Template)
	}
}

func TestLoadSubjectStreamOnly(t *testing.T) {
	yaml := `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
subject:
devices:
  - { id: asc-1, vendor: ntcip, device_kind: asc, connection: {} }
`
	cfg, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Setting only stream: custom stream is used, template remains empty (defaults apply).
	if cfg.Subject.Template != "" {
		t.Errorf("Template = %q, want empty (defaults apply)", cfg.Subject.Template)
	}
}

func TestLoadSubjectTemplateOnly(t *testing.T) {
	yaml := `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
subject:
  template: "{prefix}.{agency}.{service}.{event}.{version}"
devices:
  - { id: asc-1, vendor: ntcip, device_kind: asc, connection: {} }
`
	cfg, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Setting only template: custom template is stored, stream defaults to pre-template scheme.
	if cfg.Subject.Template != "{prefix}.{agency}.{service}.{event}.{version}" {
		t.Errorf("Template = %q", cfg.Subject.Template)
	}
}

// A bad template must fail at boot, not at first publish.
func TestLoadRejectsBadSubjectTemplate(t *testing.T) {
	cases := map[string]string{
		"unknown placeholder": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
subject: { template: "{prefix}.{nope}.{service}.{event}.{version}" }
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"no static prefix": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
subject: { template: "{service}.{event}.{version}" }
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"illegal var token": `
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
collector_id: cabinet-042-collector
subject:
  template: "{prefix}.{agency}.{service}.{event}.{version}"
  vars: { prefix: "has.dot" }
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, yaml), regWith("ntcip", "asc")); err == nil {
				t.Error("expected boot rejection")
			}
		})
	}
}

func TestLoadRequiresCollectorID(t *testing.T) {
	// collector_id is stamped on every published event as observed-by: who
	// saw this. There is no honest default — deriving it from agency/site
	// would be silently wrong the moment a cabinet runs two collectors — so
	// it is required at the trust boundary rather than invented at publish
	// time.
	if _, err := Load(write(t, baseYAML), regWith("ntcip", "asc")); err == nil {
		t.Fatal("Load accepted a config with no collector_id")
	}
}

func TestLoadRejectsMalformedCollectorID(t *testing.T) {
	// Must satisfy the wire's device-id pattern [a-zA-Z0-9_-]+, since it is
	// carried in observed-by. A dot or space here produces a payload that
	// fails schema validation at the consumer, long after we could act on it.
	for _, bad := range []string{"has space", "has.dot", "has/slash", ""} {
		yaml := baseYAML + "collector_id: \"" + bad + "\"\n"
		if _, err := Load(write(t, yaml), regWith("ntcip", "asc")); err == nil {
			t.Errorf("Load accepted collector_id %q", bad)
		}
	}
}

func TestLoadAcceptsValidCollectorID(t *testing.T) {
	yaml := baseYAML + "collector_id: cabinet-042-collector_1\n"
	cfg, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CollectorID != "cabinet-042-collector_1" {
		t.Fatalf("CollectorID = %q", cfg.CollectorID)
	}
}

// TestLoadRejectsUnrecognizedKeys pins ADR 0014's "boot fails on the
// unrecognized": a misspelled key must not be dropped in favour of a default.
// The poll_intreval case is the dangerous one — before strict decoding it
// booted fine and polled at the 5s default the operator never asked for.
func TestLoadRejectsUnrecognizedKeys(t *testing.T) {
	cases := map[string]string{
		"top-level typo":    validYAML + "colector_id: x\n",
		"device-level typo": strings.Replace(validYAML, "poll_interval: 1s", "poll_intreval: 1s", 1),
		"subject typo":      validYAML + "subject: { tempalte: \"{namespace}.{region}\" }\n", //nolint:misspell // the misspelling is the test input
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, yaml), regWith("ntcip", "asc"))
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("Load() error = %v, want an unknown-field error", err)
			}
		})
	}
}

// A second YAML document is content the collector would otherwise ignore
// without a word — the operator edited it believing it took effect.
func TestLoadRejectsTrailingDocument(t *testing.T) {
	yaml := validYAML + "---\ncollector_id: other\n"
	if _, err := Load(write(t, yaml), regWith("ntcip", "asc")); err == nil {
		t.Fatal("Load() accepted a config with a second YAML document")
	}
}

func TestLoadRejectsEmptyFile(t *testing.T) {
	if _, err := Load(write(t, ""), regWith("ntcip", "asc")); err == nil {
		t.Fatal("Load() accepted an empty config")
	}
}

// model_version selects the catalog emitter (ADR 0005), so a value no
// emitter in this binary implements cannot boot: before, any non-empty
// string did, and changed nothing.
func TestLoadRejectsUnsupportedModelVersion(t *testing.T) {
	yaml := strings.Replace(validYAML, "model_version: openits/v1", "model_version: openits/v2", 1)
	_, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err == nil || !strings.Contains(err.Error(), "openits/v2") || !strings.Contains(err.Error(), "openits/v1") {
		t.Fatalf("Load() error = %v, want one naming the bad value and the supported ones", err)
	}
}

// heartbeat_interval defaults to ADR 0019's 300s, and a value too small to be
// a fleet-health cadence is refused at boot rather than flooding the uplink.
func TestHeartbeatInterval(t *testing.T) {
	cfg, err := Load(write(t, validYAML), regWith("ntcip", "asc"))
	if err != nil || cfg.HeartbeatInterval != 300*time.Second {
		t.Fatalf("default = %v (err %v), want 300s", cfg.HeartbeatInterval, err)
	}
	cfg, err = Load(write(t, validYAML+"heartbeat_interval: 60s\n"), regWith("ntcip", "asc"))
	if err != nil || cfg.HeartbeatInterval != time.Minute {
		t.Fatalf("60s = %v (err %v)", cfg.HeartbeatInterval, err)
	}
	for _, bad := range []string{"5s", "-1m"} {
		if _, err := Load(write(t, validYAML+"heartbeat_interval: "+bad+"\n"), regWith("ntcip", "asc")); err == nil {
			t.Errorf("heartbeat_interval %s accepted", bad)
		}
	}
}

// log_ship_level defaults to warn (ADR 0019 clause 10) and is validated at
// boot like every other value.
func TestLogShipLevel(t *testing.T) {
	cfg, err := Load(write(t, validYAML), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := cfg.LogShipFloor(); l != slog.LevelWarn {
		t.Fatalf("default floor = %v, want WARN", l)
	}
	cfg, err = Load(write(t, validYAML+"log_ship_level: error\n"), regWith("ntcip", "asc"))
	if l, _ := cfg.LogShipFloor(); err != nil || l != slog.LevelError {
		t.Fatalf("error floor = %v (err %v)", l, err)
	}
	if _, err := Load(write(t, validYAML+"log_ship_level: loud\n"), regWith("ntcip", "asc")); err == nil {
		t.Fatal("log_ship_level: loud accepted")
	}
}
