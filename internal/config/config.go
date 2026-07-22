// Package config loads the collector YAML and enforces the boot trust
// boundary: bad tenant tokens, unknown adapters, or malformed devices
// refuse to start (spec §6).
package config

import (
	"fmt"
	"os"
	"strings"
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
	Stream   string            `yaml:"stream"`
	Vars     map[string]string `yaml:"vars"`
}

// Config is the collector instance configuration.
type Config struct {
	Agency       string   `yaml:"agency"`
	Site         string   `yaml:"site"`
	ModelVersion string   `yaml:"model_version"`
	Subject      Subject  `yaml:"subject"`
	Devices      []Device `yaml:"devices"`
}

// Tenant returns the validated-shape tenant identity.
func (c *Config) Tenant() cloudevents.Tenant {
	return cloudevents.Tenant{Agency: c.Agency, Site: c.Site}
}

// SubjectConfig is the subject-package view of this config.
func (c *Config) SubjectConfig() subject.Config {
	return subject.Config{Template: c.Subject.Template, Vars: c.Subject.Vars}
}

// StreamName is the JetStream stream to provision. Defaults to the
// pre-template name so existing deployments keep their stream.
func (c *Config) StreamName() string {
	if c.Subject.Stream != "" {
		return c.Subject.Stream
	}
	return "OPENITS-" + strings.ToUpper(c.Agency) + "-" + strings.ToUpper(c.Site)
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
	// Build the template now so a bad grammar is a boot failure rather than a
	// 3am unroutable event. The result is rebuilt in app.Run (which also has
	// the emitter ce-types to validate against); this is the early, cheap half.
	if _, err := subject.New(c.SubjectConfig(), c.Agency, c.Site); err != nil {
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
