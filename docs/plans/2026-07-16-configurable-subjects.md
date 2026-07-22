# Configurable Subject Templates Implementation Plan


**Goal:** Let operators define their own NATS subject grammar in config, while the CloudEvent envelope stays canonical and today's subjects remain the byte-identical default.

**Architecture:** A new `internal/subject` package owns template parsing, rendering, and stream-binding derivation. Template variables split into instance-constants (operator-defined `vars`, plus `agency`/`site`) and per-event values decomposed from the ce-type (`service`, `event`, `version`). The binding is derived by substituting constants and truncating at the first per-event token — which is why a template leading with a per-event token is rejected rather than silently provisioning a `>` stream. `wire.Emitter` gains `CETypes()` so boot validation can render *every* producible ce-type instead of sampling.

**Tech Stack:** Go 1.26, yaml.v3, nats.go + jetstream, nats-server (test-only, embedded). No new dependencies.

**Spec:** `docs/specs/2026-07-16-configurable-subjects-design.md`

## Global Constraints

- Branch from `main`. Commit style: conventional commits, **no Co-Authored-By trailers**.
- Repo-local git identity is already set (`jp2195`); do not change it.
- Module path is `github.com/Vikasa2M/vikasa-collector`.
- **Back-compat is the headline guard.** The byte-literal golden subject strings must not change. With no `subject:` block the default template reproduces them exactly. If a golden *value* needs editing, the change is wrong — stop and re-read the spec.
- CE `type` (catalog ce-type verbatim) and CE `source` (`//<agency>/<site>/<device-id>`) are **unchanged**. This plan touches routing only.
- `agency`/`site` keep `^[a-z0-9][a-z0-9-]*$` (they appear in CE `source`, a URI). Operator-defined `vars` get the looser NATS-token rule.
- All times UTC; hashed/goldened iteration is sorted; tests use fixed timestamps.
- Run `make check` before every commit claim. It includes the boundary lint and its selftest.
- **Every new guard must be shown to fail.** For each validation added, write a test proving it *rejects* bad input — not merely that it accepts good input. A check that has never failed is indistinguishable from one that cannot fail (this session's boundary-lint bug).

## Deviation from the spec

**`{device_id}` is dropped from v1.** The spec (§4) makes it available but rejects any template using it "unless every emittable event has a device". `model.CollectorStarted` is always emittable and is deliberately device-less (its CE source is the cabinet, `//<agency>/<site>`). So that rule rejects `{device_id}` unconditionally — it would be documented config that can never be enabled. Supporting it properly requires emitters to declare which ce-types are device-scoped, which nothing has asked for. Re-adding later is additive. Task 7 updates the spec's §4 to record this.

## File Structure

**New:**
- `internal/subject/subject.go` — `Config`, `Template`, `New`, `Render`, `Binding`, `ValidateCETypes`, ce-type decomposition. One responsibility: turning a template + ce-type into a subject, and refusing bad templates.
- `internal/subject/subject_test.go` — parse/render/reject/binding tables, plus the legacy-bytes golden.

**Modified:**
- `internal/wire/emitter.go` — add `CETypes() []string` to `Emitter`.
- `internal/wire/health/health.go` — implement `CETypes()`.
- `internal/wire/health/conformance_test.go` — drive from `CETypes()`, delete the hand-maintained `samples` list.
- `internal/config/config.go` — `Subject` block + validation.
- `internal/config/config_test.go` — subject config cases.
- `internal/publish/publish.go` — `Connect` takes `*subject.Template` + stream name; `Publish` renders via the template.
- `internal/publish/publish_test.go` — construct a template.
- `internal/app/app.go` — build the template, validate against emitter ce-types, pass to publish.
- `internal/cloudevents/subject.go` — delete `SubjectFor` (moves to `internal/subject`); keep `Tenant` and `SourceFor`.
- `internal/cloudevents/envelope_test.go` — remove `TestSubjectForGolden` (its exact strings move to `internal/subject`, unchanged).
- `collector.yaml`, `asyncapi.yaml`, `README.md`, `docs/adr/*` — docs.

---

### Task 1: `internal/subject` — parse, render, binding derivation

**Files:**
- Create: `internal/subject/subject.go`
- Test: `internal/subject/subject_test.go`

**Interfaces:**
- Consumes: nothing project-internal.
- Produces:
  - `const DefaultTemplate = "{prefix}.{agency}.{site}.{service}.{event}.{version}"`
  - `const DefaultPrefix = "openits"`
  - `type Config struct { Template string; Vars map[string]string }`
  - `type Template struct{ ... }` (opaque)
  - `func New(cfg Config, agency, site string) (*Template, error)` — `cfg.Template == ""` uses `DefaultTemplate`
  - `func (t *Template) Render(ceType string) (string, error)`
  - `func (t *Template) Binding() string`
  - (`ValidateCETypes` is Task 2 and is the only member added later.)

- [ ] **Step 1: Write the failing test**

`internal/subject/subject_test.go`:
```go
package subject

import "testing"

// The default template must reproduce the pre-template subjects exactly.
// These strings are the back-compat contract, moved verbatim from
// cloudevents.TestSubjectForGolden. If you are editing these values, stop:
// the change is wrong.
func TestDefaultTemplateReproducesLegacyBytes(t *testing.T) {
	tmpl, err := New(Config{}, "metro-atlanta", "cabinet-042")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := map[string]string{
		"openits.signal-control.fault-raised.v1":              "openits.metro-atlanta.cabinet-042.signal-control.fault-raised.v1",
		"openits.signal-control.operational-status-report.v1": "openits.metro-atlanta.cabinet-042.signal-control.operational-status-report.v1",
		"openits-collector.health.device-status-changed.v1":   "openits.metro-atlanta.cabinet-042.health.device-status-changed.v1",
		"openits-collector.health.collector-started.v1":       "openits.metro-atlanta.cabinet-042.health.collector-started.v1",
	}
	for ceType, want := range cases {
		got, err := tmpl.Render(ceType)
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
				Template: "{prefix}.{region}.{agency}.{site}.{env}.{service}.{event}.{version}",
				Vars:     map[string]string{"prefix": "traffic", "region": "southeast", "env": "prod"},
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
			tmpl, err := New(c.cfg, "metro-atlanta", "cabinet-042")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := tmpl.Render(c.ceType)
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
		{"device_id is not supported in v1", Config{Template: "{agency}.{device_id}.{service}.{event}.{version}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.cfg, "metro-atlanta", "cabinet-042"); err == nil {
				t.Fatalf("New(%+v) should have been rejected", c.cfg)
			}
		})
	}
}

func TestRenderRejectsMalformedCEType(t *testing.T) {
	tmpl, err := New(Config{}, "metro", "cab-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"openits.signal-control.fault-raised",      // 3 tokens
		"openits.signal-control.fault-raised.v1.x", // 5 tokens
		"fault-raised",                             // 1 token
		"",
	} {
		if _, err := tmpl.Render(bad); err == nil {
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
			want: "openits.metro-atlanta.cabinet-042.>",
		},
		{
			name: "deep tenancy keeps more static prefix",
			cfg: Config{
				Template: "{prefix}.{region}.{agency}.{site}.{service}.{event}.{version}",
				Vars:     map[string]string{"prefix": "traffic", "region": "southeast"},
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
			tmpl, err := New(c.cfg, "metro-atlanta", "cabinet-042")
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
			_, err := New(c.cfg, "metro-atlanta", "cabinet-042")
			if err == nil {
				t.Fatal("template with no static prefix must be rejected")
			}
			if !strings.Contains(err.Error(), "static prefix") {
				t.Errorf("error should explain the static-prefix problem, got: %v", err)
			}
		})
	}
}
```

This test file imports `"strings"` and `"testing"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/subject/`
Expected: FAIL — build error, `New` undefined.

- [ ] **Step 3: Implement**

`internal/subject/subject.go`:
```go
// Package subject renders NATS subjects from an operator-supplied template.
//
// Subject grammar belongs to the operator (ADR 0009): agencies fit the
// collector into namespaces they already own. The CloudEvent envelope does
// NOT — `type` stays catalog-verbatim (schema identity) and `source` stays
// canonical (fleet identity), so events remain interpretable regardless of
// local routing choices.
package subject

import (
	"fmt"
	"strings"
)

// DefaultTemplate reproduces the pre-template subject scheme exactly. Config
// that omits a subject block gets this, so existing deployments are unaffected.
const DefaultTemplate = "{prefix}.{agency}.{site}.{service}.{event}.{version}"

// DefaultPrefix is the {prefix} value assumed when config omits it.
const DefaultPrefix = "openits"

// Per-event placeholders vary per published event; instance-constants are
// fixed for the process lifetime. The split determines where the stream
// binding can be truncated (see Binding).
var perEventNames = map[string]bool{
	"service": true,
	"event":   true,
	"version": true,
}

// Reserved names may not be redefined in vars. An operator who redefined
// "service" would get subjects that disagree with their own ce-types, and it
// would surface as unroutable events rather than a config error.
var reservedNames = map[string]bool{
	"service": true, "event": true, "version": true,
	"agency": true, "site": true,
	// device_id is reserved but unsupported in v1: collector-level events
	// (collector-started) have no device, so any template using it could never
	// render a legal subject for them. Reserved so a future version can add it
	// without colliding with an operator's var of the same name.
	"device_id": true,
}

// Config is the operator's subject configuration.
type Config struct {
	Template string            // "" means DefaultTemplate
	Vars     map[string]string // operator-defined instance-constants
}

// token is one dot-separated element: either a literal or a single placeholder.
type token struct {
	literal     string // rendered value for literals and resolved constants
	perEventVar string // non-empty if this token is a per-event placeholder
}

// Template is a validated, ready-to-render subject template.
type Template struct {
	tokens  []token
	binding string
	raw     string
}

// New validates cfg and returns a Template. Every failure here is a boot
// failure by design: config is the trust boundary, and a subject typo must
// not surface at 3am as an unroutable event.
func New(cfg Config, agency, site string) (*Template, error) {
	raw := cfg.Template
	if raw == "" {
		raw = DefaultTemplate
	}

	// Resolve instance-constants: operator vars plus agency/site.
	vars := make(map[string]string, len(cfg.Vars)+3)
	for k, v := range cfg.Vars {
		if reservedNames[k] {
			return nil, fmt.Errorf("subject: vars may not redefine reserved name %q", k)
		}
		if !isLegalToken(v) {
			return nil, fmt.Errorf("subject: vars[%q] = %q is not a legal NATS token (no %q, %q, %q or whitespace, and must be non-empty)", k, v, ".", "*", ">")
		}
		vars[k] = v
	}
	// {prefix} is conventional rather than built-in; default it so the default
	// template works with no vars at all.
	if _, ok := vars["prefix"]; !ok {
		vars["prefix"] = DefaultPrefix
	}
	vars["agency"] = agency
	vars["site"] = site

	toks, err := parseTokens(raw, vars)
	if err != nil {
		return nil, err
	}

	t := &Template{tokens: toks, raw: raw}
	b, err := deriveBinding(toks)
	if err != nil {
		return nil, err
	}
	t.binding = b
	return t, nil
}

// parseTokens splits the template on "." and resolves each token. Each token is
// either a literal or exactly one whole-token placeholder — a placeholder may
// not share a token with other text, because the binding must be truncatable at
// a token boundary.
func parseTokens(raw string, vars map[string]string) ([]token, error) {
	if raw == "" {
		return nil, fmt.Errorf("subject: template is empty")
	}
	parts := strings.Split(raw, ".")
	out := make([]token, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("subject: template %q has an empty token", raw)
		}
		opens := strings.Count(p, "{")
		closes := strings.Count(p, "}")
		if opens != closes {
			return nil, fmt.Errorf("subject: template %q has unbalanced braces in token %q", raw, p)
		}
		if opens == 0 {
			if !isLegalToken(p) {
				return nil, fmt.Errorf("subject: template %q has illegal literal token %q", raw, p)
			}
			out = append(out, token{literal: p})
			continue
		}
		// A placeholder sharing a token with other text leaves the binding no
		// boundary to truncate on (see deriveBinding).
		if opens > 1 || !strings.HasPrefix(p, "{") || !strings.HasSuffix(p, "}") {
			return nil, fmt.Errorf("subject: template %q token %q must be a literal or a single whole-token placeholder like {service}", raw, p)
		}
		name := p[1 : len(p)-1]
		if name == "" {
			return nil, fmt.Errorf("subject: template %q has an empty placeholder", raw)
		}
		if name == "device_id" {
			return nil, fmt.Errorf("subject: {device_id} is not supported: collector-level events have no device, so such a template could never render a legal subject for them")
		}
		if perEventNames[name] {
			out = append(out, token{perEventVar: name})
			continue
		}
		v, ok := vars[name]
		if !ok {
			return nil, fmt.Errorf("subject: template %q references {%s}, which is neither built-in (service, event, version) nor defined in vars", raw, name)
		}
		out = append(out, token{literal: v})
	}
	return out, nil
}

// Render produces the subject for one ce-type.
func (t *Template) Render(ceType string) (string, error) {
	service, event, version, err := decompose(ceType)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(t.tokens))
	for i, tok := range t.tokens {
		switch tok.perEventVar {
		case "":
			parts[i] = tok.literal
		case "service":
			parts[i] = service
		case "event":
			parts[i] = event
		case "version":
			parts[i] = version
		}
	}
	return strings.Join(parts, "."), nil
}

// decompose splits a ce-type into its subject-relevant parts. The first token
// is the schema namespace (openits vs openits-collector) and is deliberately
// NOT a subject token: catalog and health events share a subject root, which is
// what the pre-template scheme did.
func decompose(ceType string) (service, event, version string, err error) {
	parts := strings.Split(ceType, ".")
	if len(parts) != 4 {
		return "", "", "", fmt.Errorf("subject: ce-type %q must be <namespace>.<service>.<event>.<version>", ceType)
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", fmt.Errorf("subject: ce-type %q has an empty token", ceType)
		}
	}
	return parts[1], parts[2], parts[3], nil
}

// isLegalToken reports whether s can appear as a single NATS subject token.
func isLegalToken(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, ".*> \t\n\r")
}
```

Note the import block above lists only `fmt` and `strings` — do not add `sort` here; nothing in this package sorts.

And the binding derivation, which `New` calls above:

```go
// Binding is the JetStream stream subject filter that captures everything this
// template can render.
func (t *Template) Binding() string { return t.binding }

// deriveBinding substitutes instance-constants and truncates at the first
// per-event token, because a JetStream stream needs a static subject filter.
func deriveBinding(toks []token) (string, error) {
	var prefix []string
	for _, tok := range toks {
		if tok.perEventVar != "" {
			break
		}
		prefix = append(prefix, tok.literal)
	}
	if len(prefix) == 0 {
		return "", fmt.Errorf("subject: template has no static prefix — its leftmost token varies per event, " +
			"so the stream binding would be \">\" and would capture every subject on the server. " +
			"Put constant tokens (agency, site, a prefix) leftmost")
	}
	if len(prefix) == len(toks) {
		// No per-event tokens at all: every event renders the same subject.
		// Legal, if odd; bind it exactly rather than with a wildcard.
		return strings.Join(prefix, "."), nil
	}
	return strings.Join(prefix, ".") + ".>", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/subject/ -v`
Expected: PASS — `TestDefaultTemplateReproducesLegacyBytes`, `TestCustomLayouts`, `TestNewRejects`, `TestRenderRejectsMalformedCEType`, `TestBindingDerivation`, `TestNewRejectsTemplatesWithNoStaticPrefix`.

- [ ] **Step 5: Commit**

```bash
git add internal/subject
git commit -m "feat(subject): operator-defined subject template rendering

Template variables split into instance-constants (operator vars plus
agency/site) and per-event values decomposed from the ce-type. Each
dot-token is a literal or a single whole-token placeholder, which is what
lets the stream binding truncate on a boundary.

The default template reproduces the previous subjects byte-for-byte; that
golden is the back-compat contract."
```

---

### Task 2: `internal/subject` — exhaustive ce-type validation

**Files:**
- Modify: `internal/subject/subject.go` (add `ValidateCETypes`, `withinBinding`)
- Test: `internal/subject/subject_test.go` (append)

**Interfaces:**
- Consumes: `Template`, `Render`, `Binding` (Task 1).
- Produces:
  - `func (t *Template) ValidateCETypes(ceTypes []string) error`

- [ ] **Step 1: Write the failing test**

Append to `internal/subject/subject_test.go`:
```go
func TestValidateCETypes(t *testing.T) {
	tmpl, err := New(Config{}, "metro-atlanta", "cabinet-042")
	if err != nil {
		t.Fatal(err)
	}
	ok := []string{
		"openits-collector.health.collector-started.v1",
		"openits.signal-control.fault-raised.v1",
	}
	if err := tmpl.ValidateCETypes(ok); err != nil {
		t.Fatalf("ValidateCETypes(%v): %v", ok, err)
	}
	// A malformed ce-type must be caught at boot, not when it first publishes.
	if err := tmpl.ValidateCETypes([]string{"openits.signal-control.fault-raised"}); err == nil {
		t.Fatal("malformed ce-type must be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/subject/ -run ValidateCETypes`
Expected: FAIL — build error, `ValidateCETypes` undefined.

- [ ] **Step 3: Implement**

Add to `internal/subject/subject.go`:

```go
// ValidateCETypes renders every ce-type the collector can emit and checks each
// produces a legal subject inside the binding. Exhaustive rather than sampled:
// this is what turns a subject typo into a boot failure instead of a 3am
// unroutable event.
func (t *Template) ValidateCETypes(ceTypes []string) error {
	for _, ceType := range ceTypes {
		subj, err := t.Render(ceType)
		if err != nil {
			return fmt.Errorf("subject: template %q cannot render ce-type %q: %w", t.raw, ceType, err)
		}
		for _, tok := range strings.Split(subj, ".") {
			if !isLegalToken(tok) {
				return fmt.Errorf("subject: ce-type %q renders to %q, which has an illegal token %q", ceType, subj, tok)
			}
		}
		if !withinBinding(subj, t.binding) {
			return fmt.Errorf("subject: ce-type %q renders to %q, outside the stream binding %q", ceType, subj, t.binding)
		}
	}
	return nil
}

// withinBinding reports whether subj would be captured by the binding filter.
func withinBinding(subj, binding string) bool {
	if strings.HasSuffix(binding, ".>") {
		return strings.HasPrefix(subj, strings.TrimSuffix(binding, ">"))
	}
	return subj == binding
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/subject/ -v`
Expected: PASS, all tests.

Also run: `go vet ./internal/subject/` and `gofmt -l internal/subject` — both clean.

- [ ] **Step 5: Commit**

```bash
git add internal/subject
git commit -m "feat(subject): derive stream binding; reject templates with no static prefix

A JetStream stream needs a static subject filter, so the binding is the
template with constants substituted, truncated at the first per-event
token. A template leading with a per-event token has no static prefix and
would bind '>', swallowing every subject on the server — rejected at boot
with an error that explains why rather than silently provisioning it.

ValidateCETypes renders every emittable ce-type so a bad template fails at
startup instead of on first publish."
```

---

### Task 3: `wire.Emitter.CETypes()`

**Files:**
- Modify: `internal/wire/emitter.go`, `internal/wire/health/health.go`
- Test: `internal/wire/health/conformance_test.go` (replace the `samples` list)

**Interfaces:**
- Consumes: `model.Event` (existing).
- Produces: `Emitter.CETypes() []string` — every ce-type this emitter can produce, sorted.

- [ ] **Step 1: Write the failing test**

Replace the `samples` var and both AsyncAPI tests in `internal/wire/health/conformance_test.go` with:
```go
// Every health event the emitter can produce, one sample each. Paired with
// CETypes() below: the test asserts the two agree, so adding an event without
// updating both fails rather than silently escaping documentation.
var samples = []model.Event{
	model.DeviceStatusChanged{
		Base:      model.Base{DeviceID: "asc-1", OccurredAt: time.Now().UTC()},
		Reachable: false, Reason: "read timeout", ConsecutiveFailures: 2,
	},
	model.CollectorStarted{
		Base: model.Base{OccurredAt: time.Now().UTC()}, Version: "dev",
	},
}

// CETypes is the emitter's own declaration of what it can produce; the samples
// are what it actually produces. Drift between them means one of the two is
// lying, and boot validation trusts CETypes.
func TestCETypesMatchesWhatTheEmitterActuallyEmits(t *testing.T) {
	em := NewHealthEmitter()
	declared := em.CETypes()

	var actual []string
	for _, ev := range samples {
		enc, ok, err := em.Encode(ev)
		if err != nil || !ok {
			t.Fatalf("%s: emitter did not claim its own event (ok=%v err=%v)", ev.EventKind(), ok, err)
		}
		actual = append(actual, enc.CEType)
	}
	sort.Strings(actual)

	if !equal(declared, actual) {
		t.Errorf("CETypes() disagrees with emitted types\n  declared: %v\n  emitted:  %v", declared, actual)
	}
	if !sort.StringsAreSorted(declared) {
		t.Errorf("CETypes() must be sorted, got %v", declared)
	}
}

func TestAsyncAPICoversEveryEmittedType(t *testing.T) {
	doc := loadAsyncAPI(t)
	em := NewHealthEmitter()

	for _, ceType := range em.CETypes() {
		if _, documented := doc.Channels[ceType]; !documented {
			t.Errorf("ce-type %q is emittable but has no channel in asyncapi.yaml", ceType)
		}
		if _, documented := doc.Components.Messages[ceType]; !documented {
			t.Errorf("ce-type %q has no message definition in asyncapi.yaml", ceType)
		}
	}

	// Payload shape must match the bytes actually emitted.
	for _, ev := range samples {
		enc, ok, err := em.Encode(ev)
		if err != nil || !ok {
			t.Fatalf("%s: Encode ok=%v err=%v", ev.EventKind(), ok, err)
		}
		msg := doc.Components.Messages[enc.CEType]
		if msg.ContentType != enc.ContentType {
			t.Errorf("%s: asyncapi says contentType %q, emitter sends %q", enc.CEType, msg.ContentType, enc.ContentType)
		}
		var body map[string]any
		if err := json.Unmarshal(enc.Data, &body); err != nil {
			t.Fatalf("%s: emitted payload is not JSON: %v", enc.CEType, err)
		}
		emitted := keysOf(body)
		if !equal(emitted, keysOf(msg.Payload.Properties)) {
			t.Errorf("%s: payload drift\n  emitted:    %v\n  documented: %v", enc.CEType, emitted, keysOf(msg.Payload.Properties))
		}
		if !equal(emitted, sorted(msg.Payload.Required)) {
			t.Errorf("%s: every field is always emitted, so all must be required\n  emitted:  %v\n  required: %v", enc.CEType, emitted, sorted(msg.Payload.Required))
		}
	}
}

// A channel nobody can emit is a lie to consumers.
func TestAsyncAPIDocumentsNothingUnemittable(t *testing.T) {
	doc := loadAsyncAPI(t)
	emittable := map[string]bool{}
	for _, ceType := range NewHealthEmitter().CETypes() {
		emittable[ceType] = true
	}
	for ceType := range doc.Channels {
		if !emittable[ceType] {
			t.Errorf("asyncapi.yaml documents %q but no health event produces it", ceType)
		}
	}
}
```

Ensure the test file imports `sort`, `encoding/json`, `time`, `testing`, `os`, `path/filepath`, `gopkg.in/yaml.v3`, and the `model` package.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/wire/...`
Expected: FAIL — `em.CETypes` undefined.

- [ ] **Step 3: Implement**

In `internal/wire/emitter.go`, add to the `Emitter` interface:
```go
type Emitter interface {
	Encode(ev model.Event) (enc *Encoded, ok bool, err error)

	// CETypes returns every ce-type this emitter can produce, sorted.
	// Boot validation renders each through the subject template, so an
	// emitter that under-reports here defeats that check.
	CETypes() []string
}
```

In `internal/wire/health/health.go`, add:
```go
// ceTypeDeviceStatusChanged and ceTypeCollectorStarted are the collector-owned
// health ce-types (ADR 0007).
const (
	ceTypeDeviceStatusChanged = "openits-collector.health.device-status-changed.v1"
	ceTypeCollectorStarted    = "openits-collector.health.collector-started.v1"
)

func (emitter) CETypes() []string {
	// Sorted: boot validation and goldens both iterate this.
	return []string{ceTypeCollectorStarted, ceTypeDeviceStatusChanged}
}
```

Replace the two ce-type string literals in `Encode` with `ceTypeDeviceStatusChanged` / `ceTypeCollectorStarted`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/wire/... -v`
Expected: PASS. Confirm `TestCETypesMatchesWhatTheEmitterActuallyEmits` passes — it is the guard that keeps `CETypes()` honest.

- [ ] **Step 5: Commit**

```bash
git add internal/wire
git commit -m "feat(wire): emitters declare the ce-types they can produce

Boot validation needs to render every producible ce-type through the
subject template; sampling would let a typo reach production on a rarely
emitted event. A test asserts CETypes() agrees with what Encode actually
returns, so the declaration cannot drift from reality.

Also lets the AsyncAPI conformance test drive from the real set instead of
a hand-maintained list."
```

---

### Task 4: `internal/config` — the subject block

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (append)

**Interfaces:**
- Consumes: `subject.Config`, `subject.New` (Tasks 1–2).
- Produces:
  - `type Subject struct { Template string; Stream string; Vars map[string]string }` (yaml: `template`, `stream`, `vars`)
  - `Config.Subject Subject` (yaml: `subject`)
  - `func (c *Config) SubjectConfig() subject.Config`
  - `func (c *Config) StreamName() string` — `Subject.Stream`, else `OPENITS-<AGENCY>-<SITE>` uppercased

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:
```go
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
	if got := cfg.StreamName(); got != "OPENITS-METRO-ATLANTA-CABINET-042" {
		t.Errorf("StreamName() = %q, want OPENITS-METRO-ATLANTA-CABINET-042", got)
	}
}

func TestLoadSubjectCustom(t *testing.T) {
	yaml := `
agency: metro
site: cab-1
model_version: openits/v1
subject:
  template: "{prefix}.{region}.{agency}.{service}.{event}.{version}"
  stream: EDGE-METRO-CAB1
  vars:
    prefix: traffic
    region: southeast
devices:
  - { id: asc-1, vendor: ntcip, device_kind: asc, connection: {} }
`
	cfg, err := Load(write(t, yaml), regWith("ntcip", "asc"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Subject.Vars["region"] != "southeast" {
		t.Errorf("vars not preserved: %+v", cfg.Subject.Vars)
	}
	if got := cfg.StreamName(); got != "EDGE-METRO-CAB1" {
		t.Errorf("StreamName() = %q, want EDGE-METRO-CAB1", got)
	}
	sc := cfg.SubjectConfig()
	if sc.Template != "{prefix}.{region}.{agency}.{service}.{event}.{version}" {
		t.Errorf("SubjectConfig().Template = %q", sc.Template)
	}
}

// A bad template must fail at boot, not at first publish.
func TestLoadRejectsBadSubjectTemplate(t *testing.T) {
	cases := map[string]string{
		"unknown placeholder": `
agency: metro
site: cab-1
model_version: openits/v1
subject: { template: "{prefix}.{nope}.{service}.{event}.{version}" }
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"no static prefix": `
agency: metro
site: cab-1
model_version: openits/v1
subject: { template: "{service}.{event}.{version}" }
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"illegal var token": `
agency: metro
site: cab-1
model_version: openits/v1
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `cfg.Subject`, `StreamName`, `SubjectConfig` undefined.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add the import `"github.com/Vikasa2M/vikasa-collector/internal/subject"` and `"strings"`, then:

```go
// Subject is the operator's subject grammar. Every field is optional; the
// zero value reproduces the pre-template scheme exactly (ADR 0009).
type Subject struct {
	Template string            `yaml:"template"`
	Stream   string            `yaml:"stream"`
	Vars     map[string]string `yaml:"vars"`
}
```

Add to `Config`:
```go
	Subject Subject `yaml:"subject"`
```

Add methods:
```go
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
```

In `validate`, after the `ModelVersion` check, add:
```go
	// Build the template now so a bad grammar is a boot failure rather than a
	// 3am unroutable event. The result is rebuilt in app.Run (which also has
	// the emitter ce-types to validate against); this is the early, cheap half.
	if _, err := subject.New(c.SubjectConfig(), c.Agency, c.Site); err != nil {
		return err
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, including the three rejection cases.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): operator subject template, vars and stream name

Every field is optional and the zero value reproduces the previous scheme,
so existing configs are byte-identical and need no migration. The template
is built during validation so a bad grammar fails at boot."
```

---

### Task 5: `internal/publish` — publish through the template

**Files:**
- Modify: `internal/publish/publish.go`, `internal/publish/publish_test.go`

**Interfaces:**
- Consumes: `subject.Template` (Tasks 1–2), `cloudevents.Envelope`.
- Produces:
  - `func Connect(ctx context.Context, url string, tmpl *subject.Template, streamName string) (*Publisher, error)`
  - `func (p *Publisher) Publish(ctx context.Context, env cloudevents.Envelope, ceType string) error` (unchanged signature)
  - `StreamName(cloudevents.Tenant) string` is **deleted** — the name now comes from config.

- [ ] **Step 1: Update the test**

In `internal/publish/publish_test.go`, replace the `Connect` call and stream lookup in `TestPublishRoundTripAndDedup`:
```go
	tmpl, err := subject.New(subject.Config{}, "metro", "cab-1")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Connect(ctx, ns.ClientURL(), tmpl, "OPENITS-METRO-CAB-1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()
```
Add the import `"github.com/Vikasa2M/vikasa-collector/internal/subject"`. The `wantSubject` assertion stays exactly `"openits.metro.cab-1.health.collector-started.v1"` — the default template must still produce it.

Append a custom-template test:
```go
func TestPublishUsesCustomTemplate(t *testing.T) {
	ns := startNATS(t)
	ctx := context.Background()

	tmpl, err := subject.New(subject.Config{
		Template: "{prefix}.{region}.{agency}.{service}.{event}.{version}",
		Vars:     map[string]string{"prefix": "traffic", "region": "southeast"},
	}, "metro", "cab-1")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Connect(ctx, ns.ClientURL(), tmpl, "EDGE-METRO-CAB1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	at := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	ceType := "openits-collector.health.collector-started.v1"
	env := cloudevents.New(ceType, "//metro/cab-1", at, "application/json", []byte(`{"version":"dev"}`))
	if err := p.Publish(ctx, env, ceType); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	stream, err := js.Stream(ctx, "EDGE-METRO-CAB1")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "t"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(2 * time.Second))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := "traffic.southeast.metro.health.collector-started.v1"
	if msg.Subject() != want {
		t.Fatalf("subject = %q, want %q", msg.Subject(), want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/publish/`
Expected: FAIL — `Connect` signature mismatch.

- [ ] **Step 3: Implement**

In `internal/publish/publish.go`: drop the `strings` and `cloudevents.Tenant` plumbing for names, add `"github.com/Vikasa2M/vikasa-collector/internal/subject"`, and replace `Publisher`, `Connect`, `StreamName`, and the subject lookup in `Publish`:

```go
// Publisher owns the NATS connection and the cabinet stream.
type Publisher struct {
	nc   *nats.Conn
	js   jetstream.JetStream
	tmpl *subject.Template
}

// Connect dials NATS and ensures the cabinet stream exists. The stream's
// subject filter is derived from the template, so an operator's grammar and
// the stream that captures it can never disagree.
func Connect(ctx context.Context, url string, tmpl *subject.Template, streamName string) (*Publisher, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("publish: subject template is required")
	}
	if streamName == "" {
		return nil, fmt.Errorf("publish: stream name is required")
	}
	nc, err := nats.Connect(url, nats.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       streamName,
		Subjects:   []string{tmpl.Binding()},
		Storage:    jetstream.FileStorage,
		Duplicates: dedupWindow,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure stream: %w", err)
	}
	return &Publisher{nc: nc, js: js, tmpl: tmpl}, nil
}
```

In `Publish`, replace the first line:
```go
	subj, err := p.tmpl.Render(ceType)
	if err != nil {
		return err
	}
```
and use `subj` in place of `subject` throughout (`nats.Msg{Subject: subj, ...}` and the error message). Delete the `StreamName` function.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/publish/ -v`
Expected: PASS — both the default-template round-trip (still asserting the exact legacy subject) and the custom-template test.

- [ ] **Step 5: Commit**

```bash
git add internal/publish
git commit -m "feat(publish): render subjects from the template; bind the derived filter

The stream's subject filter now comes from the template's own binding, so
an operator's grammar and the stream that captures it cannot disagree.
Stream name comes from config."
```

---

### Task 6: `internal/app` — wire it up, validate exhaustively

**Files:**
- Modify: `internal/app/app.go`, `internal/cloudevents/subject.go`, `internal/cloudevents/envelope_test.go`
- Test: `internal/app/app_test.go` (append)

**Interfaces:**
- Consumes: everything above.
- Produces: `app.Run` unchanged in signature; builds the template, validates it against the union of emitter ce-types, then connects.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/app_test.go`:
```go
// A bad subject grammar must stop the collector at boot, before any device is
// polled or any stream provisioned. (This exercises the New/parse rejection;
// ValidateCETypes' own failure path is covered by the unit tests in Task 2,
// since Run hardcodes its emitter chain and no ce-type can be injected here.)
func TestRunRejectsBadSubjectTemplate(t *testing.T) {
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
		func(deviceID string, conn map[string]any) (adapter.Adapter, error) { return &flakyASC{}, nil })

	cfg := &config.Config{
		Agency: "metro", Site: "cab-1", ModelVersion: "openits/v1",
		Subject: config.Subject{Template: "{prefix}.{agency}.{nope}.{service}.{event}.{version}"},
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 20 * time.Millisecond}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, cfg, reg, ns.ClientURL(), "test"); err == nil {
		t.Fatal("Run must refuse to start on a template it cannot render")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestRunRejectsBadSubjectTemplate`
Expected: FAIL — `Run` does not yet build a template, so it proceeds past the bad grammar and blocks until the context deadline instead of returning an error.

Also confirm no other caller depends on the function Task 6 deletes:

Run: `grep -rn 'SubjectFor' --include='*.go' .`
Expected: only `internal/cloudevents/subject.go` and `internal/cloudevents/envelope_test.go` — both handled in Step 3. Any other hit means an extra call site to update.

- [ ] **Step 3: Implement**

In `internal/app/app.go`, replace the top of `Run` (through `publish.Connect`):
```go
func Run(ctx context.Context, cfg *config.Config, reg *adapter.Registry, natsURL, version string) error {
	tenant := cfg.Tenant()

	// Emitter chain: first claim wins. Plan 2 prepends the openits-models
	// emitter selected by cfg.ModelVersion; today only health is wired, so
	// domain events fall through to the loud-drop path below.
	emitters := []wire.Emitter{health.NewHealthEmitter()}

	tmpl, err := subject.New(cfg.SubjectConfig(), cfg.Agency, cfg.Site)
	if err != nil {
		return err
	}
	// Exhaustive, not sampled: every ce-type any emitter can produce must
	// render to a legal subject inside the binding. A grammar mistake fails
	// here, at boot, rather than the first time a rare event fires.
	var ceTypes []string
	for _, em := range emitters {
		ceTypes = append(ceTypes, em.CETypes()...)
	}
	sort.Strings(ceTypes)
	if err := tmpl.ValidateCETypes(ceTypes); err != nil {
		return err
	}

	pub, err := publish.Connect(ctx, natsURL, tmpl, cfg.StreamName())
	if err != nil {
		return err
	}
	defer pub.Close()
```
Add `"sort"` to the imports and `"github.com/Vikasa2M/vikasa-collector/internal/subject"`.

In `internal/cloudevents/subject.go`, delete `SubjectFor` entirely (it moved to `internal/subject`) and drop the now-unused `strings` import. Keep `Tenant`, `Validate`, `tokenRe`, and `SourceFor`.

In `internal/cloudevents/envelope_test.go`, delete `TestSubjectForGolden` — its exact expected strings now live in `internal/subject.TestDefaultTemplateReproducesLegacyBytes`, unchanged. Keep `TestTenantValidate`, `TestContentAddressedID`, `TestSourceFor`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/app/ -count=2`
Expected: PASS — the existing e2e still publishes health events on `openits.metro.cab-1.>`, and the new test proves boot refuses a broken template.

Run: `make check` — Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/app internal/cloudevents
git commit -m "feat(app): build the subject template at boot and validate it exhaustively

Every ce-type any emitter can produce is rendered through the template
before connecting, so a grammar mistake refuses to start instead of
surfacing when a rare event first fires.

cloudevents.SubjectFor is deleted; subject rendering now lives in
internal/subject. Its byte-literal goldens moved there unchanged and
remain the back-compat contract."
```

---

### Task 7: ADR 0009 and documentation

**Files:**
- Create: `docs/adr/0009-configurable-subject-templates.md`
- Modify: `docs/adr/README.md`, `docs/adr/0006-tenant-scoped-subjects.md`, `collector.yaml`, `asyncapi.yaml`, `README.md`, `docs/specs/2026-07-16-configurable-subjects-design.md`

**Interfaces:** none (docs).

- [ ] **Step 1: Write ADR 0009**

`docs/adr/0009-configurable-subject-templates.md`:
```markdown
# ADR 0009: Operator-configurable subject templates

**Status:** Accepted (2026-07-16)
**Supersedes:** the subject-grammar half of [ADR 0006](0006-tenant-scoped-subjects.md)

## Context
ADR 0006 fixed the subject grammar at
`openits.<agency>.<site>.<service>.<event>.v<n>`. Its reasoning about routing
and cardinality holds, but it decided the grammar on every deployment's behalf.
Agencies need to fit the collector into namespaces they already own: different
token names, fewer tokens, more tokens. All three required a code change, and
`openits` as a root was imposed on everyone.

## Decision
Subject grammar is the operator's, via a config template of literal `{name}`
placeholders (no logic, nothing executable). Variables are instance-constants
(operator-defined `vars`, plus `agency`/`site`) or per-event values decomposed
from the ce-type (`service`, `event`, `version`). Omitting the config
reproduces ADR 0006's scheme byte-for-byte.

**ADR 0006's identity decisions are retained unchanged:** CE `type` is the
catalog ce-type verbatim, and CE `source` is `//<agency>/<site>/<device-id>`.
Identity and routing are different concerns — operators own routing; the
envelope stays canonical so a fleet remains interpretable regardless of local
subject choices. This is also what keeps Plan 2's catalog-conformance test
meaningful.

The JetStream stream binding is derived from the template (constants
substituted, truncated at the first per-event token). A template whose leftmost
token varies per event has no static prefix, so its binding would be `>` — a
stream capturing every subject on the server. That is rejected at boot, not
provisioned.

## Consequences
Agencies self-serve their namespace. Boot validation renders every emittable
ce-type, so grammar mistakes fail at startup rather than when a rare event
fires; this required `wire.Emitter` to declare `CETypes()`. Layouts that read
well for fleet-wide consumers (service-first, flat) cannot bind a per-cabinet
stream and are therefore rejected — an honest consequence of the collector
being an edge component. Changing a running cabinet's grammar generally implies
provisioning a new stream.

`{device_id}` is reserved but unsupported: collector-level events
(`collector-started`) have no device, so any template using it could never
render a legal subject for them. Supporting it would require emitters to
declare which ce-types are device-scoped; nothing has asked for that.

## Alternatives considered
Configurable prefix only (rejected: covers "not openits" but not the actual ask
— fewer, more, and renamed tokens). Go `text/template` (rejected: templates
become programs; validation gets much harder and nobody asked for logic in a
subject). Templating `type`/`source` as well (rejected: forfeits catalog
conformance and a canonical fleet identity).
```

- [ ] **Step 2: Update the ADR index and 0006**

In `docs/adr/README.md`, add to the table after the 0008 row:
```markdown
| [0009](0009-configurable-subject-templates.md) | Operator-configurable subject templates (supersedes 0006's grammar) |
```

In `docs/adr/0006-tenant-scoped-subjects.md`, change the status line to:
```markdown
**Status:** Partially superseded by [ADR 0009](0009-configurable-subject-templates.md) (2026-07-16)

The subject *grammar* below is now the operator's, configured per deployment;
ADR 0009's default reproduces it exactly. The identity decisions — CE `type` =
catalog ce-type verbatim, CE `source` = `//<agency>/<site>/<device-id>`,
content-addressed CE `id` — are **retained unchanged**.
```
(Records are immutable; this adds a forward pointer rather than editing the decision.)

- [ ] **Step 3: Document the config**

In `collector.yaml`, insert before `devices:`:
```yaml
# Subject grammar is yours (ADR 0009). Omit this block entirely and you get
# the default below, which is what the collector published before templates
# existed:
#
#   subject:
#     template: "{prefix}.{agency}.{site}.{service}.{event}.{version}"
#     vars: { prefix: openits }
#
# Placeholders are either instance-constants — anything under `vars`, plus
# `agency` and `site` — or per-event values decomposed from the ce-type:
# `service`, `event`, `version` (which carries its literal "v", e.g. "v1").
# Reserved names (agency, site, service, event, version, device_id) cannot be
# redefined in vars.
#
# Rule of thumb: COARSEST, MOST STABLE TOKENS LEFTMOST. NATS makes prefix
# matching cheap — the JetStream stream binding, subject permissions, and
# upstream aggregation all key off the left of the subject.
#
#   Tenant-first (default) — clean per-cabinet stream binding; per-site NATS
#     auth falls out naturally. Cross-fleet subscription by event type needs
#     mid-pattern wildcards that hardcode tenancy depth.
#   Deep tenancy ({prefix}.{region}.{agency}.{site}...) — region-level
#     aggregation and auth; every subscriber pattern encodes the depth.
#   Service-first / flat ({prefix}.{service}...) — REJECTED AT BOOT. A cabinet
#     publishes across every service, so there is no static prefix to bind a
#     stream on and the binding would capture every subject on the server.
#
# Two warnings: adding a token later silently breaks subscribers whose
# wildcards encoded the old depth, so choose depth up front; and changing the
# grammar of a running cabinet generally means provisioning a new stream,
# because the binding is derived from the template.
#
# subject:
#   template: "{prefix}.{region}.{agency}.{site}.{service}.{event}.{version}"
#   stream: EDGE-METRO-ATLANTA-CABINET-042   # defaults to OPENITS-<AGENCY>-<SITE>
#   vars:
#     prefix: openits
#     region: southeast
```

- [ ] **Step 4: Note the AsyncAPI addresses are the default rendering**

In `asyncapi.yaml`, extend the header comment after the tenant-parameterized paragraph:
```yaml
# The addresses below show the DEFAULT subject rendering. Subject grammar is
# operator-configurable (ADR 0009), so a deployment with a custom template
# publishes these same ce-types on subjects of its own shape. The ce-type —
# the channel key, and the CloudEvent `type` — is what stays constant, and is
# the schema identity consumers should match on.
```

In `README.md`, change the core bullet to:
```markdown
- **The core** diffs snapshots into domain events, tracks device health,
  and publishes on operator-configurable subjects (ADR 0009) — by default
  `openits.<agency>.<site>.<service>.<event>.v1`.
```

- [ ] **Step 5: Record the `{device_id}` deviation in the spec**

In `docs/specs/2026-07-16-configurable-subjects-design.md`, replace the `### {device_id}` subsection body with:
```markdown
**Not supported in v1.** The rule below ("rejected unless every emittable event
has a device") turns out to reject it unconditionally: `model.CollectorStarted`
is always emittable and is deliberately device-less, since its CE source is the
cabinet rather than a device. Shipping a knob that can never be switched on is
worse than omitting it. The name stays reserved so a later version can add it —
which would require emitters to declare which ce-types are device-scoped —
without colliding with an operator's var. Cardinality (devices × events) remains
the reason ADR 0006 kept device out of the subject.
```

- [ ] **Step 6: Verify and commit**

Run: `make check` — Expected: green.
Run: `go test ./cmd/...` — Expected: PASS (the example config test still loads `collector.yaml`).

```bash
git add docs collector.yaml asyncapi.yaml README.md
git commit -m "docs: ADR 0009 — operator-configurable subject templates

Supersedes ADR 0006's grammar while retaining its identity decisions.
Documents the layout trade-offs in collector.yaml, including why
service-first and flat layouts are rejected at boot, and records that
{device_id} is reserved but unsupported."
```

---

## Final verification

```bash
make check                       # vet, tests, boundary lint + selftest
go test ./... -race              # concurrency
go build ./... && go run ./cmd/collector -version
```

Then confirm the back-compat contract explicitly:

```bash
go test ./internal/subject/ -run TestDefaultTemplateReproducesLegacyBytes -v
```

If that test ever needs its expected strings edited, the change is wrong.

## Follow-on (not in this plan)

- **Plan 3 — facets:** `FaultSet` + `DetectorSamples` on `ntcip-asc`. Decided during design review: port all 8 of gen-1's fault bitmap bits carrying its "never validated against real hardware" hedge; the detector differ suppresses volume on first poll and treats a counter decrease as a controller reset. Do **not** port gen-1's `snmp-unreachable` synthetic fault — because `diffFaults` was a pure set-difference, a failed poll spuriously cleared every real fault and re-raised it on recovery, which is exactly what the "absence of evidence is never a state change" rule forbids.
- **RSU:** `RSUBroadcastCounters` + `ntcip-rsu` (separate spec — the catalog has no broadcast-counter event, only a `rate_hz` sample, so that facet defines a shape rather than matching one).
- **Plan 2 — `wire/openitsv1`:** still blocked on openits-models settling its module path (`Vikasa2M/openits-models` vs the declared `openits/openits-models`) and cutting a tag.
