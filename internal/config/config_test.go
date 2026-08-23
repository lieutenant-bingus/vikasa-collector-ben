package config

import (
	"os"
	"path/filepath"
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
