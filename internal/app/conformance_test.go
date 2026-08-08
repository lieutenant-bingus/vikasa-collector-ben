package app

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"os"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// The NATS reference profile's Tier 2 rules, transcribed from
// openits-models tools/conformance/tests/{subject,cetype}.go. Kept as literal
// regexes rather than importing the harness: the harness is a Go program in a
// module we must not import outside internal/wire (ADR 0002), and these are
// the assertions it makes.
var (
	profileToken  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	profileCEType = regexp.MustCompile(`^openits\.[a-z0-9-]+\.[a-z0-9-]+\.v\d+$`)
	profileURN    = regexp.MustCompile(`^urn:openits:[a-z][a-z0-9-]*:[a-z0-9-]+:[a-z0-9-]+:[a-z0-9-]+:[A-Za-z0-9_-]+$`)
)

type observed struct {
	subject string
	headers nats.Header
	body    []byte
}

// runCollectorAndCollect boots the real spine against embedded JetStream and
// returns everything it published within the window. Nothing is stubbed below
// the emitter: these are the bytes a consumer would actually receive.
func runCollectorAndCollect(t *testing.T, window time.Duration) []observed {
	t.Helper()

	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &flakyASC{}, nil })

	cfgYAML := `
collector_id: metro-cab-1-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - id: asc-1
    vendor: fixture
    device_kind: asc
    poll_interval: 100ms
    connection: {}
`
	path := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(path, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	var (
		mu  = make(chan []observed, 1)
		got []observed
	)
	mu <- got
	// Subscribes to ">" rather than to the two known roots on purpose: a
	// third namespace appearing unannounced should be visible to these tests,
	// not filtered out before they run. Messages with no ce-type are the
	// broker's own JetStream API traffic ($JS.API.*), not collector output.
	sub, err := nc.Subscribe(">", func(m *nats.Msg) {
		if m.Header.Get("ce-type") == "" {
			return
		}
		cur := <-mu
		cur = append(cur, observed{subject: m.Subject, headers: m.Header, body: m.Data})
		mu <- cur
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()
	if err := Run(ctx, cfg, reg, ns.ClientURL(), "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return <-mu
}

func TestTier2ProfileConformance(t *testing.T) {
	msgs := runCollectorAndCollect(t, 900*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("no events published; the conformance assertions below would pass vacuously")
	}

	sawOpenITS := false
	for _, m := range msgs {
		// Tier 2 is assessed over the CATALOG subject space. Collector-owned
		// health lives on its own root (ADR 0011) and is asserted separately.
		if !strings.HasPrefix(m.subject, "openits.") {
			continue
		}
		ceType := m.headers.Get("ce-type")

		// --- Subject grammar (TestSubject_SevenTokenShape / _OpenitsPrefix)
		parts := strings.Split(m.subject, ".")
		if len(parts) != 7 {
			t.Errorf("%s: subject has %d tokens, want 7", m.subject, len(parts))
		}
		for _, p := range parts {
			if !profileToken.MatchString(p) {
				t.Errorf("%s: token %q is not lowercase-alnum-hyphen", m.subject, p)
			}
		}

		// --- Envelope (TestCESource_URN / TestCEID_Present)
		if src := m.headers.Get("ce-source"); !profileURN.MatchString(src) {
			t.Errorf("%s: ce-source %q is not a well-formed openits URN", ceType, src)
		}
		if strings.TrimSpace(m.headers.Get("ce-id")) == "" {
			t.Errorf("%s: ce-id is empty; replay dedup is impossible", ceType)
		}
		if v := m.headers.Get("ce-specversion"); v != "1.0" {
			t.Errorf("%s: ce-specversion = %q", ceType, v)
		}

		// --- ce-type shape (TestCEType_OpenitsFormat)
		if strings.HasPrefix(ceType, "openits.") {
			sawOpenITS = true
			if !profileCEType.MatchString(ceType) {
				t.Errorf("ce-type %q does not match openits.<service>.<event>.v<major>", ceType)
			}
			if ds := m.headers.Get("ce-dataschema"); ds == "" {
				t.Errorf("%s: catalog events must carry ce-dataschema", ceType)
			}
			if ct := m.headers.Get("ce-datacontenttype"); ct != "application/protobuf" {
				t.Errorf("%s: ce-datacontenttype = %q, want application/protobuf", ceType, ct)
			}
			if len(m.body) == 0 {
				t.Errorf("%s: empty body", ceType)
			}
		}
	}

	if !sawOpenITS {
		t.Error("only health events were published; the catalog-event assertions never ran")
	}
}

// TestHealthEventsAreOffTheCatalogSubjectSpace is what makes the separation
// real rather than incidental.
//
// Health carries openits-collector.* ce-types, which the profile's ce-type
// regex rejects by design (ADR 0007). While health shared the catalog subject
// root, a Tier 2 harness pointed at `openits.>` flagged every health event as
// non-conformant. Rooting health on its own namespace removes that false
// negative — but only for as long as nothing drifts back, which is what this
// test holds in place.
func TestHealthEventsAreOffTheCatalogSubjectSpace(t *testing.T) {
	msgs := runCollectorAndCollect(t, 900*time.Millisecond)

	var sawCatalog, sawHealth bool
	for _, m := range msgs {
		ceType := m.headers.Get("ce-type")
		switch {
		case strings.HasPrefix(ceType, "openits-collector."):
			sawHealth = true
			if !strings.HasPrefix(m.subject, "openits-collector.") {
				t.Errorf("health ce-type %q published on %q, which is inside the catalog space",
					ceType, m.subject)
			}
			// Still profile-SHAPED, just on its own root: health stays
			// routable and permissionable by the same grammar.
			if n := len(strings.Split(m.subject, ".")); n != 7 {
				t.Errorf("health subject %q has %d tokens, want 7", m.subject, n)
			}
			if ds := m.headers.Get("ce-dataschema"); ds != "" {
				t.Errorf("health event carries ce-dataschema %q; it has no registry entry", ds)
			}
		case strings.HasPrefix(ceType, "openits."):
			sawCatalog = true
			if strings.HasPrefix(m.subject, "openits-collector.") {
				t.Errorf("catalog ce-type %q published on the health root %q", ceType, m.subject)
			}
		default:
			t.Errorf("unrecognised ce-type namespace: %q", ceType)
		}
	}
	if !sawHealth || !sawCatalog {
		t.Fatalf("need both families to assert the split: health=%v catalog=%v", sawHealth, sawCatalog)
	}
}
