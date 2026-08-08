package subject

import (
	"strings"
	"testing"
)

// The default template's exact output, per ce-type. These strings are the
// contract: a consumer's subscription wildcards encode this token layout, so
// changing them silently breaks subscribers. If you are editing these values,
// the change had better be a deliberate grammar reset with the stream
// reprovisioned to match — see ADR 0009's consequences and the Plan 2 spec.
//
// This replaced a five-token tenant-first golden when the profile grammar
// became the default; that reset was free only because there were no
// deployments.
func TestDefaultTemplateGolden(t *testing.T) {
	tmpl, err := New(Config{}, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := map[string]string{
		"openits.signal-control.fault-raised.v1":              "openits.us-tx.metro-atlanta.d07.signal-control.dev-1.fault-raised",
		"openits.signal-control.operational-status-report.v1": "openits.us-tx.metro-atlanta.d07.signal-control.dev-1.operational-status-report",
		"openits-collector.health.device-status-changed.v1":   "openits.us-tx.metro-atlanta.d07.health.dev-1.device-status-changed",
		"openits-collector.health.collector-started.v1":       "openits.us-tx.metro-atlanta.d07.health.dev-1.collector-started",
	}
	for ceType, want := range cases {
		got, err := tmpl.Render(ceType, "dev-1")
		if err != nil {
			t.Fatalf("Render(%q): %v", ceType, err)
		}
		if got != want {
			t.Errorf("Render(%q) = %q, want %q", ceType, got, want)
		}
	}
}

func TestCustomLayouts(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		ceType string
		want   string
	}{
		{
			name:   "fewer tokens: no tenancy",
			cfg:    Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "openits"}},
			ceType: "openits.signal-control.fault-raised.v1",
			want:   "openits.signal-control.fault-raised.v1",
		},
		{
			name: "more tokens: region + environment",
			cfg: Config{
				Template: "{prefix}.{geo}.{agency}.{site}.{env}.{service}.{event}.{version}",
				Vars:     map[string]string{"prefix": "traffic", "geo": "southeast", "env": "prod"},
			},
			ceType: "openits.signal-control.fault-raised.v1",
			want:   "traffic.southeast.metro-atlanta.cabinet-042.prod.signal-control.fault-raised.v1",
		},
		{
			name: "renamed tokens: operator's own vocabulary",
			cfg: Config{
				Template: "{district}.{corridor}.{service}.{event}.{version}",
				Vars:     map[string]string{"district": "d7", "corridor": "peachtree"},
			},
			ceType: "openits-collector.health.device-status-changed.v1",
			want:   "d7.peachtree.health.device-status-changed.v1",
		},
		{
			name:   "literal tokens",
			cfg:    Config{Template: "edge.v1.{agency}.{service}.{event}.{version}"},
			ceType: "openits.rsu.fault-raised.v1",
			want:   "edge.v1.metro-atlanta.rsu.fault-raised.v1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl, err := New(c.cfg, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := tmpl.Render(c.ceType, "dev-1")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != c.want {
				t.Errorf("Render = %q, want %q", got, c.want)
			}
		})
	}
}

// Each rejection here is a real footgun; a validator that never rejects is
// indistinguishable from no validator.
func TestNewRejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"unknown placeholder", Config{Template: "{prefix}.{nope}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "openits"}}},
		{"vars shadow reserved name", Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "openits", "service": "hijacked"}}},
		{"var value contains dot", Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "open.its"}}},
		{"var value contains wildcard", Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "open*"}}},
		{"var value contains >", Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "open>"}}},
		{"var value contains space", Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": "open its"}}},
		{"var value empty", Config{Template: "{prefix}.{service}.{event}.{version}", Vars: map[string]string{"prefix": ""}}},
		{"placeholder splits a token", Config{Template: "pre{service}post.{event}.{version}"}},
		{"empty placeholder", Config{Template: "{}.{service}.{event}.{version}"}},
		{"unbalanced brace", Config{Template: "{prefix.{service}.{event}.{version}"}},
		{"empty token", Config{Template: "{agency}..{service}.{event}.{version}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.cfg, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"}); err == nil {
				t.Fatalf("New(%+v) should have been rejected", c.cfg)
			}
		})
	}
}

// Agency and site parameters must be validated with the same rigor as var values,
// since they flow through the same substitution map and render path.
func TestNewRejectsInvalidAgencyAndSite(t *testing.T) {
	cases := []struct {
		name   string
		agency string
		site   string
	}{
		{"agency contains dot", "metro.atlanta", "cabinet-042"},
		{"agency contains wildcard", "metro*", "cabinet-042"},
		{"agency contains >", "metro>", "cabinet-042"},
		{"agency contains space", "metro atlanta", "cabinet-042"},
		{"agency empty", "", "cabinet-042"},
		{"site contains dot", "metro-atlanta", "cabinet.042"},
		{"site contains wildcard", "metro-atlanta", "cabinet*"},
		{"site contains >", "metro-atlanta", "cabinet>"},
		{"site contains space", "metro-atlanta", "cabinet 042"},
		{"site empty", "metro-atlanta", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(Config{}, Identity{Region: "us-tx", Agency: c.agency, AgencyUnit: "d07", Site: c.site}); err == nil {
				t.Fatalf("New(Config{}, %q, %q) should have been rejected", c.agency, c.site)
			}
		})
	}
}

func TestRenderRejectsMalformedCEType(t *testing.T) {
	tmpl, err := New(Config{}, Identity{Region: "us-tx", Agency: "metro", AgencyUnit: "d07", Site: "cab-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"openits.signal-control.fault-raised",      // 3 tokens
		"openits.signal-control.fault-raised.v1.x", // 5 tokens
		"fault-raised", // 1 token
		"",
	} {
		if _, err := tmpl.Render(bad, "dev-1"); err == nil {
			t.Errorf("Render(%q) should have been rejected", bad)
		}
	}
}

func TestBindingDerivation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "default truncates at {service}",
			cfg:  Config{},
			want: "openits.us-tx.metro-atlanta.d07.>",
		},
		{
			name: "deep tenancy keeps more static prefix",
			cfg: Config{
				Template: "{prefix}.{geo}.{agency}.{site}.{service}.{event}.{version}",
				Vars:     map[string]string{"prefix": "traffic", "geo": "southeast"},
			},
			want: "traffic.southeast.metro-atlanta.cabinet-042.>",
		},
		{
			name: "literal-only prefix",
			cfg:  Config{Template: "edge.{agency}.{service}.{event}.{version}"},
			want: "edge.metro-atlanta.>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl, err := New(c.cfg, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := tmpl.Binding(); got != c.want {
				t.Errorf("Binding() = %q, want %q", got, c.want)
			}
		})
	}
}

// A template leading with a per-event token has no static prefix, so its
// binding would degrade to ">" — a stream that swallows every subject on the
// server, including other components'. That must never be provisioned.
func TestNewRejectsTemplatesWithNoStaticPrefix(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"service-first", Config{Template: "{service}.{event}.{version}.{agency}.{site}"}},
		{"event-first", Config{Template: "{event}.{version}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.cfg, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"})
			if err == nil {
				t.Fatal("template with no static prefix must be rejected")
			}
			if !strings.Contains(err.Error(), "static prefix") {
				t.Errorf("error should explain the static-prefix problem, got: %v", err)
			}
		})
	}
}

func TestValidateCETypes(t *testing.T) {
	tmpl, err := New(Config{}, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"})
	if err != nil {
		t.Fatal(err)
	}
	ok := []string{
		"openits-collector.health.collector-started.v1",
		"openits.signal-control.fault-raised.v1",
	}
	if err := tmpl.ValidateCETypes(ok, nil); err != nil {
		t.Fatalf("ValidateCETypes(%v): %v", ok, err)
	}
	// A malformed ce-type must be caught at boot, not when it first publishes.
	if err := tmpl.ValidateCETypes([]string{"openits.signal-control.fault-raised"}, nil); err == nil {
		t.Fatal("malformed ce-type must be rejected")
	}
}

// decompose only checks tokens for emptiness, not legality, so a well-formed
// 4-token ce-type whose service segment contains a space renders a subject
// with an illegal token. The illegal-token loop in ValidateCETypes is the
// only thing that catches this — Render itself happily produces the subject.
func TestValidateCETypesRejectsIllegalRenderedToken(t *testing.T) {
	tmpl, err := New(Config{}, Identity{Region: "us-tx", Agency: "metro-atlanta", AgencyUnit: "d07", Site: "cabinet-042"})
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{"openits.sig nal.fault-raised.v1"}
	if _, err := tmpl.Render(bad[0], "dev-1"); err != nil {
		t.Fatalf("Render(%q) should succeed (decompose does not check legality): %v", bad[0], err)
	}
	err = tmpl.ValidateCETypes(bad, nil)
	if err == nil {
		t.Fatalf("ValidateCETypes(%v) should have rejected the illegal rendered token", bad)
	}
	if !strings.Contains(err.Error(), "illegal token") {
		t.Errorf("error should explain the illegal-token problem, got: %v", err)
	}
}

func TestWithinBinding(t *testing.T) {
	cases := []struct {
		name    string
		subj    string
		binding string
		want    bool
	}{
		{
			name:    "wildcard binding accepts a real subject",
			subj:    "openits.metro.cab-1.health.collector-started.v1",
			binding: "openits.metro.cab-1.>",
			want:    true,
		},
		{
			// cab-10 shares a textual prefix with cab-1, but is not within a
			// binding scoped to cab-1: withinBinding must compare at a token
			// boundary, not do a raw string-prefix match. If the trailing dot
			// ever got trimmed along with the ">", this would wrongly pass,
			// and boot validation would bless a subject the stream never
			// actually captures.
			name:    "near-miss: shared textual prefix is not a token match",
			subj:    "openits.metro.cab-10.health.x.v1",
			binding: "openits.metro.cab-1.>",
			want:    false,
		},
		{
			name:    "exact binding: equal subject matches",
			subj:    "openits.metro.cab-1",
			binding: "openits.metro.cab-1",
			want:    true,
		},
		{
			name:    "exact binding: different subject does not match",
			subj:    "openits.metro.cab-2",
			binding: "openits.metro.cab-1",
			want:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withinBinding(c.subj, c.binding); got != c.want {
				t.Errorf("withinBinding(%q, %q) = %v, want %v", c.subj, c.binding, got, c.want)
			}
		})
	}
}

func TestDefaultTemplateIsTheSevenTokenProfileGrammar(t *testing.T) {
	// The profile's grammar, and the shape its conformance harness asserts:
	// openits.{geo}.{agency}.{agency-unit}.{service}.{controller-id}.{event}
	// Note there is no {version} token — the ce-type carries the major
	// version, so repeating it in the subject would be a second source of
	// truth that can disagree with the first.
	tmpl, err := New(Config{}, Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := tmpl.Render("openits.signal-control.fault-raised.v1", "i35-exit-214")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "openits.us-tx.txdot.d07.signal-control.i35-exit-214.fault-raised"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if n := len(strings.Split(got, ".")); n != 7 {
		t.Errorf("subject has %d tokens, want 7: %q", n, got)
	}
}

func TestRenderDeviceLessEventUsesCollector(t *testing.T) {
	// Collector-level events (the boot event) have no device. {device_id} was
	// previously rejected outright BECAUSE of them; rendering the literal
	// "collector" is what lets the token graduate, so every event the
	// collector can emit has a legal subject.
	tmpl, _ := New(Config{}, Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	got, err := tmpl.Render("openits-collector.health.collector-started.v1", "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "openits.us-tx.txdot.d07.health.collector.collector-started"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestBindingTruncatesAtTheFirstPerEventToken(t *testing.T) {
	// The stream binding must be static. service is the first per-event token
	// in the default grammar, so the binding stops before it.
	tmpl, _ := New(Config{}, Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	if got, want := tmpl.Binding(), "openits.us-tx.txdot.d07.>"; got != want {
		t.Errorf("Binding = %q, want %q", got, want)
	}
}

func TestValidateCETypesIsExhaustiveOverDevices(t *testing.T) {
	// Every ce-type × every configured device must render a legal subject.
	// A device id carrying a dot would silently add a token and land the event
	// outside the binding; catching it here makes it a boot failure.
	tmpl, _ := New(Config{}, Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	ceTypes := []string{"openits.signal-control.fault-raised.v1"}

	if err := tmpl.ValidateCETypes(ceTypes, []string{"good-1", "good-2"}); err != nil {
		t.Errorf("legal device ids rejected: %v", err)
	}
	if err := tmpl.ValidateCETypes(ceTypes, []string{"good-1", "bad.id"}); err == nil {
		t.Error("a device id containing a dot must be rejected: it adds a subject token")
	}
	if err := tmpl.ValidateCETypes(ceTypes, []string{"wild*"}); err == nil {
		t.Error("a device id containing a wildcard must be rejected")
	}
}

func TestVarsMayNotRedefineTheNewReservedNames(t *testing.T) {
	for _, name := range []string{"region", "agency_unit", "device_id"} {
		_, err := New(Config{Vars: map[string]string{name: "x"}},
			Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
		if err == nil {
			t.Errorf("vars redefining %q was accepted", name)
		}
	}
}
