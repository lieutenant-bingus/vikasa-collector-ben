package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

// asyncAPIDoc is the sliver of the AsyncAPI document this test needs.
type asyncAPIDoc struct {
	Channels map[string]struct {
		Address string `yaml:"address"`
	} `yaml:"channels"`
	Components struct {
		Schemas struct {
			CloudEventHeaders struct {
				Properties struct {
					CEID struct {
						Description string `yaml:"description"`
					} `yaml:"ce-id"`
					CESource struct {
						Description string `yaml:"description"`
					} `yaml:"ce-source"`
				} `yaml:"properties"`
			} `yaml:"cloudEventHeaders"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadAsyncAPIDoc(t *testing.T) asyncAPIDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, "asyncapi.yaml"))
	if err != nil {
		t.Fatalf("read asyncapi.yaml: %v", err)
	}
	var doc asyncAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse asyncapi.yaml: %v", err)
	}
	return doc
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
	doc := loadAsyncAPIDoc(t)
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

// ceSourceScheme is the URN template asyncapi.yaml documents for ce-source.
// It must appear in the doc verbatim, and substituting its placeholders must
// reproduce what internal/cloudevents.SourceFor actually builds (ADR 0015).
const ceSourceScheme = "urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>"

// TestAsyncAPICESourceDescriptionMatchesSourceFor renders a real ce-source
// URN through internal/cloudevents.SourceFor and checks it against the
// scheme asyncapi.yaml documents. ce-source is load-bearing beyond the
// document: SourceFor's output feeds EventID's digest verbatim, so a stale
// description here is exactly the kind of drift that goes unnoticed until
// someone builds an id-verification tool against the wrong shape.
func TestAsyncAPICESourceDescriptionMatchesSourceFor(t *testing.T) {
	doc := loadAsyncAPIDoc(t)
	desc := doc.Components.Schemas.CloudEventHeaders.Properties.CESource.Description
	if desc == "" {
		t.Fatal("asyncapi.yaml has no ce-source description; the check would be vacuous")
	}
	if !strings.Contains(desc, ceSourceScheme) {
		t.Fatalf("ce-source description does not contain the documented scheme %q:\n%s", ceSourceScheme, desc)
	}

	tenant := cloudevents.Tenant{
		Region: testIdentity.Region, Agency: testIdentity.Agency,
		AgencyUnit: testIdentity.AgencyUnit, Site: testIdentity.Site,
	}

	for name, tc := range map[string]struct {
		deviceKind, entityKind, deviceID, wantID string
	}{
		// asc -> controller mirrors internal/cloudevents.entityKindFor's
		// table (also exercised directly by TestSourceFor_BuildsTheProfileURN
		// in internal/cloudevents).
		"device event": {"asc", "controller", "asc-1", "asc-1"},
		"device-less":  {"", "collector", "", testIdentity.Site}, // SourceFor substitutes site as the id.
	} {
		t.Run(name, func(t *testing.T) {
			got := cloudevents.SourceFor(tenant, tc.deviceKind, tc.deviceID)
			want := strings.NewReplacer(
				"<entity-kind>", tc.entityKind,
				"<region>", tenant.Region,
				"<agency>", tenant.Agency,
				"<agency_unit>", tenant.AgencyUnit,
				"<id>", tc.wantID,
			).Replace(ceSourceScheme)
			if got != want {
				t.Errorf("SourceFor = %q, documented scheme renders %q", got, want)
			}
		})
	}
}

// ceIDDigestFormula is the digest formula asyncapi.yaml documents for
// ce-id. It must appear in the doc verbatim, field order included: this is
// the exact string that catches a future edit reverting the field order to
// the original defect (SHA-256(type, source, …) instead of the correct
// SHA-256(source ‖ ce-type ‖ stable-time ‖ payload-bytes)). The "ULID" and
// shape checks below do not catch that regression — a reordered digest
// input still produces a 26-character Crockford ULID — so this literal
// substring check is the only guard on the field order itself.
const ceIDDigestFormula = "SHA-256(source ‖ ce-type ‖ stable-time ‖\npayload-bytes)"

// crockfordULIDRe matches a 26-character Crockford base32 ULID: exactly what
// asyncapi.yaml's ce-id description now claims EventID produces.
var crockfordULIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// TestAsyncAPICEIDDescriptionMatchesEventIDShape checks the ce-id
// description against a real EventID output. It cannot cheaply assert the
// full digest formula (source/ce-type/stable-time/payload, unit-separated) —
// that's exhausted by internal/cloudevents's own vector tests against
// openits-models' ce-id-spec.md — but it can catch the two ways this
// description has drifted before: reverting to "bare content hash" prose,
// and the rendered id ever stopping being a 26-character Crockford ULID.
func TestAsyncAPICEIDDescriptionMatchesEventIDShape(t *testing.T) {
	doc := loadAsyncAPIDoc(t)
	desc := doc.Components.Schemas.CloudEventHeaders.Properties.CEID.Description
	if desc == "" {
		t.Fatal("asyncapi.yaml has no ce-id description; the check would be vacuous")
	}
	if !strings.Contains(desc, "ULID") {
		t.Errorf("ce-id description no longer mentions ULID; it must not describe the id as a bare content hash:\n%s", desc)
	}
	if !strings.Contains(desc, ceIDDigestFormula) {
		t.Errorf("ce-id description does not contain the documented digest formula %q (field order matters — this is the guard against reverting to SHA-256(type, source, …)):\n%s", ceIDDigestFormula, desc)
	}

	id := cloudevents.EventID("urn:openits:controller:us-ga:metro-atlanta:d01:asc-1",
		"openits-collector.health.device-status-changed.v1", time.Now(), []byte("payload"))
	if !crockfordULIDRe.MatchString(id) {
		t.Errorf("EventID = %q, want a 26-character Crockford base32 ULID as documented", id)
	}
}
