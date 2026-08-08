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
	sub, err := nc.Subscribe("openits.>", func(m *nats.Msg) {
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
		ceType := m.headers.Get("ce-type")

		// --- Subject grammar (TestSubject_SevenTokenShape / _OpenitsPrefix)
		if !strings.HasPrefix(m.subject, "openits.") {
			t.Errorf("%s: subject lacks the openits. authority prefix", m.subject)
		}
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
		//
		// Scoped to catalog events on purpose. Collector-owned health events
		// use the openits-collector.* namespace by design (ADR 0007) so they
		// stay publishable when the wire model is in flux, and they do NOT
		// satisfy the profile's ce-type regex. See the test below, which pins
		// that as a known, deliberate divergence rather than letting it pass
		// silently here.
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

// TestHealthEventsAreKnowinglyOutsideTheProfile pins a divergence rather than
// asserting conformance.
//
// Health events ride the SAME subject space as catalog events — a conformant
// seven-token openits.* subject — but carry openits-collector.* ce-types,
// which the profile's ce-type regex rejects. That is deliberate (ADR 0007:
// device reachability must stay reportable precisely when the wire model is in
// flux), but it means a Tier 2 harness pointed at `openits.>` will flag them.
//
// Recorded here so the trade-off is a known property with a test naming it,
// not a surprise the first time someone runs the harness against a live
// cabinet.
func TestHealthEventsAreKnowinglyOutsideTheProfile(t *testing.T) {
	msgs := runCollectorAndCollect(t, 900*time.Millisecond)

	sawHealth := false
	for _, m := range msgs {
		ceType := m.headers.Get("ce-type")
		if !strings.HasPrefix(ceType, "openits-collector.") {
			continue
		}
		sawHealth = true

		// The subject IS profile-shaped — health is routable alongside
		// everything else, which is the point of ADR 0009's split between
		// routing and identity.
		if n := len(strings.Split(m.subject, ".")); n != 7 {
			t.Errorf("health subject %q has %d tokens, want 7", m.subject, n)
		}
		// The ce-type is NOT, and must not silently become so.
		if profileCEType.MatchString(ceType) {
			t.Errorf("health ce-type %q now matches the profile regex; if health "+
				"joined the catalog that is a real change, not a test fix", ceType)
		}
		// It must still omit ce-dataschema: there is no registry entry to name.
		if ds := m.headers.Get("ce-dataschema"); ds != "" {
			t.Errorf("health event carries ce-dataschema %q; it has no registry entry", ds)
		}
	}
	if !sawHealth {
		t.Fatal("no health events published; this test asserted nothing")
	}
}
