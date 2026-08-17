// Package config loads the collector YAML and enforces the boot trust
// boundary: bad tenant tokens, unknown adapters, or malformed devices
// refuse to start (ADR 0014).
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// Device is one polled device.
type Device struct {
	ID           string         `yaml:"id"`
	Vendor       string         `yaml:"vendor"`
	DeviceKind   string         `yaml:"device_kind"`
	PollInterval time.Duration  `yaml:"poll_interval"`
	Connection   map[string]any `yaml:"connection"`
}

// Subject is the operator's subject grammar. Every field is optional; the
// zero value reproduces the pre-template scheme exactly (ADR 0009).
type Subject struct {
	Template string            `yaml:"template"`
	Vars     map[string]string `yaml:"vars"`
}

// Config is the collector instance configuration.
type Config struct {
	// Region, Agency and AgencyUnit are the profile's identity triple and
	// appear in every ce-source URN. Site is not in the URN; it stays because
	// the collector still uses it for stream naming and health context.
	Region     string `yaml:"region"`
	Agency     string `yaml:"agency"`
	AgencyUnit string `yaml:"agency_unit"`
	Site       string `yaml:"site"`
	// CollectorID identifies THIS collector as the observer of the events it
	// synthesizes. It is stamped into every openits payload's observed-by,
	// which exists precisely to distinguish "the device reported this" from
	// "a poller inferred it by diffing". Required, with no default: deriving
	// it from agency/site would be silently wrong the moment a cabinet runs
	// two collectors, and the error would surface as mislabelled provenance
	// in the data lake rather than as a failure to start.
	CollectorID  string   `yaml:"collector_id"`
	ModelVersion string   `yaml:"model_version"`
	Subject      Subject  `yaml:"subject"`
	Devices      []Device `yaml:"devices"`
}

// deviceIDRe is the wire's device-id pattern. CollectorID rides in observed-by,
// which is typed device-id upstream, so a value that violates it produces a
// payload the consumer rejects long after we could have acted on it.
var deviceIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Tenant returns the validated-shape tenant identity.
func (c *Config) Tenant() cloudevents.Tenant {
	return cloudevents.Tenant{
		Region: c.Region, Agency: c.Agency, AgencyUnit: c.AgencyUnit, Site: c.Site,
	}
}

// SubjectConfig is the subject-package view of this config.
func (c *Config) SubjectConfig() subject.Config {
	return subject.Config{Template: c.Subject.Template, Vars: c.Subject.Vars}
}

// SubjectIdentity is the subject-package view of the tenant identity.
func (c *Config) SubjectIdentity() subject.Identity {
	return subject.Identity{
		Region: c.Region, Agency: c.Agency, AgencyUnit: c.AgencyUnit, Site: c.Site,
	}
}

// DeviceIDs lists every configured device, for exhaustive subject validation.
func (c *Config) DeviceIDs() []string {
	ids := make([]string, 0, len(c.Devices))
	for _, d := range c.Devices {
		ids = append(ids, d.ID)
	}
	return ids
}

// Load parses and validates. Any validation failure is fatal at boot.
func Load(path string, reg *adapter.Registry) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.validate(reg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) validate(reg *adapter.Registry) error {
	if err := c.Tenant().Validate(); err != nil {
		return err
	}
	if c.ModelVersion == "" {
		return fmt.Errorf("model_version is required")
	}
	if !deviceIDRe.MatchString(c.CollectorID) {
		return fmt.Errorf("collector_id %q is required and must match %s "+
			"(it is published as observed-by on every event)", c.CollectorID, deviceIDRe)
	}
	// Build the template now so a bad grammar is a boot failure rather than a
	// 3am unroutable event. The result is rebuilt in app.Run (which also has
	// the emitter ce-types to validate against); this is the early, cheap half.
	tmpl, err := subject.New(c.SubjectConfig(), c.SubjectIdentity())
	if err != nil {
		return err
	}
	// Structural check only: whether the grammar can ever yield a static
	// stream binding. The exhaustive per-ce-type validation happens in
	// app.Run, which knows what the emitters produce; this is the early,
	// cheap half that keeps a hopeless template from passing the trust
	// boundary.
	if err := tmpl.ValidateBindable(); err != nil {
		return err
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("at least one device is required")
	}
	seen := make(map[string]bool)
	for i := range c.Devices {
		d := &c.Devices[i]
		if d.ID == "" {
			return fmt.Errorf("devices[%d]: id is required", i)
		}
		if seen[d.ID] {
			return fmt.Errorf("duplicate device id %q", d.ID)
		}
		seen[d.ID] = true
		if !reg.Known(d.Vendor, d.DeviceKind) {
			return fmt.Errorf("device %q: no adapter registered for %s-%s", d.ID, d.Vendor, d.DeviceKind)
		}
		if d.PollInterval < 0 {
			return fmt.Errorf("device %q: poll_interval must be positive", d.ID)
		}
		if d.PollInterval == 0 {
			d.PollInterval = 5 * time.Second
		}
	}
	return nil
}
