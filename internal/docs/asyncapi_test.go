package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

// asyncAPIDoc is the sliver of the AsyncAPI document this test needs.
type asyncAPIDoc struct {
	Channels map[string]struct {
		Address string `yaml:"address"`
	} `yaml:"channels"`
}

// testIdentity is the identity the addresses are rendered against. Any
// values work; they only have to be the same on both sides of the
// comparison.
var testIdentity = subject.Identity{
	Region: "us-ga", Agency: "metro-atlanta", AgencyUnit: "d01", Site: "cabinet-042",
}

// deviceIDFor mirrors what the publisher passes: health events about the
// collector itself carry no device.
func deviceIDFor(ceType string) string {
	if strings.Contains(ceType, "collector-started") {
		return ""
	}
	return "asc-1"
}

// TestAsyncAPIAddressesMatchRenderedSubjects renders every channel address
// declared in asyncapi.yaml through the real subject.Template and checks it
// against what the collector actually publishes. asyncapi.yaml is the only
// machine-readable contract for the collector's health events; if its
// addresses drift from the real renderer, this is the only thing that
// notices.
func TestAsyncAPIAddressesMatchRenderedSubjects(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "asyncapi.yaml"))
	if err != nil {
		t.Fatalf("read asyncapi.yaml: %v", err)
	}
	var doc asyncAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse asyncapi.yaml: %v", err)
	}
	if len(doc.Channels) == 0 {
		t.Fatal("asyncapi.yaml declares no channels; the check would be vacuous")
	}

	tmpl, err := subject.New(subject.Config{}, testIdentity)
	if err != nil {
		t.Fatalf("build default template: %v", err)
	}

	for ceType, ch := range doc.Channels {
		dev := deviceIDFor(ceType)
		want, err := tmpl.Render(ceType, dev)
		if err != nil {
			t.Errorf("channel %q: renderer rejected it: %v", ceType, err)
			continue
		}
		got := strings.NewReplacer(
			"{region}", testIdentity.Region,
			"{agency}", testIdentity.Agency,
			"{agency_unit}", testIdentity.AgencyUnit,
			"{site}", testIdentity.Site,
			"{device_id}", dev,
		).Replace(ch.Address)
		if got != want {
			t.Errorf("channel %q address drifted:\n  asyncapi: %s\n  rendered: %s",
				ceType, got, want)
		}
	}
}
