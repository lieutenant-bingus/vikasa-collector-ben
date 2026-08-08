# Health Subject Separation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish collector-owned health events on their own subject root and their own JetStream stream, so operational data about the collector is separable from traffic telemetry for retention, access control, and consumption.

**Architecture:** The ce-type already carries its namespace as its first token (`openits.*` for catalog events, `openits-collector.*` for health), and `subject.decompose` currently discards it. Reverse that discard: `{namespace}` becomes a per-event subject token and the leftmost token of the default grammar. One template then renders two subject roots, `Bindings()` returns one static filter per namespace the emitters can actually produce, and the publisher provisions a stream per binding.

**Tech Stack:** Go 1.26, NATS JetStream (`nats.go`), existing `internal/subject` template engine.

## Global Constraints

- Every task ends green on `make check` AND `go test ./... -race`.
- TDD: write the failing test, watch it fail for the right reason, then implement. A guard that has not been seen to fail is not known to be a guard.
- No AI/assistant attribution anywhere in commit messages (`AGENTS.md`).
- Conventional Commits. `main` is protected; work stays on a branch.
- **Do not push.** Commit locally only.
- Adapters, `sdk/`, and everything outside `internal/wire` must not import openits-models (ADR 0002, enforced by `scripts/lint-boundary.sh`).
- Subject tokens must match `^[a-z0-9][a-z0-9-]*$`. Note `openits-collector` satisfies this — the namespace is a legal single token.

## Why this is a breaking change worth making now

The stream binding is derived from the template, so changing the subject grammar means reprovisioning streams. There are zero deployments, which makes it free exactly once. The three drivers, in order of durability:

1. **Retention.** Health is high-churn operational state with short useful life; telemetry is archival. One stream forces one retention policy, wrong for one of them.
2. **Access control.** With a shared root you cannot grant `openits.>` to a data platform without also exposing collector internals.
3. **Conformance tooling.** A Tier 2 harness pointed at `openits.>` currently flags every health event, because health carries `openits-collector.*` ce-types that the profile's ce-type regex rejects. Separating the root removes the false negative.

## File Structure

| File | Responsibility after this change |
|---|---|
| `internal/subject/subject.go` | `{namespace}` per-event token; default grammar rooted on it; `Bindings(namespaces)` replacing `Binding()` |
| `internal/subject/subject_test.go` | Grammar goldens for both roots; binding derivation per namespace |
| `internal/config/config.go` | Drops `subject.stream`; stream names derive from bindings |
| `internal/publish/publish.go` | Provisions one stream per binding; `Connect` takes namespaces |
| `internal/app/app.go` | Collects namespaces from `CETypes()`, passes them to template validation and `Connect` |
| `internal/app/conformance_test.go` | Asserts health is ABSENT from `openits.>`, present on `openits-collector.>` |
| `docs/adr/0011-namespace-rooted-subject-spaces.md` | New record; amends 0009's grammar consequence |
| `collector.yaml`, `README.md` | Operator-facing documentation of the two roots |

---

### Task 1: `{namespace}` becomes a per-event subject token

**Files:**
- Modify: `internal/subject/subject.go`
- Test: `internal/subject/subject_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `decompose(ceType string) (namespace, service, event, version string, err error)` — note the added leading return value; `namespaceOf(ceType string) (string, error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestRenderRootsOnTheCETypeNamespace(t *testing.T) {
	// The ce-type's first token IS the subject root. Catalog and health
	// events therefore land on sibling roots without the template needing
	// to know either name.
	tmpl, err := New(Config{}, Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct{ ceType, deviceID, want string }{
		{"openits.signal-control.fault-raised.v1", "i35-exit-214",
			"openits.us-tx.txdot.d07.signal-control.i35-exit-214.fault-raised"},
		{"openits-collector.health.collector-started.v1", "",
			"openits-collector.us-tx.txdot.d07.health.collector.collector-started"},
		{"openits-collector.health.device-status-changed.v1", "asc-1",
			"openits-collector.us-tx.txdot.d07.health.asc-1.device-status-changed"},
	} {
		got, err := tmpl.Render(tc.ceType, tc.deviceID)
		if err != nil {
			t.Fatalf("Render(%q): %v", tc.ceType, err)
		}
		if got != tc.want {
			t.Errorf("Render(%q) = %q, want %q", tc.ceType, got, tc.want)
		}
		if n := len(strings.Split(got, ".")); n != 7 {
			t.Errorf("Render(%q) produced %d tokens, want 7: %q", tc.ceType, n, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/subject/ -run RootsOnTheCEType`
Expected: FAIL — the rendered subject still starts with `openits.` for both, because `{prefix}` is a constant and the namespace is discarded.

- [ ] **Step 3: Implement**

In `internal/subject/subject.go`, change the default template, register the token, and stop discarding the namespace:

```go
const DefaultTemplate = "{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}"
```

Add `"namespace": true` to both `perEventNames` and `reservedNames`.

Change `decompose` to return the namespace instead of throwing it away, and delete the comment claiming catalog and health share a root:

```go
// decompose splits a ce-type into its subject-relevant parts. The first token
// is the schema namespace — `openits` for catalog events, `openits-collector`
// for the collector-owned health schema (ADR 0007) — and it IS a subject
// token: it roots each family in its own space so they can carry different
// retention and different access control (ADR 0011).
func decompose(ceType string) (namespace, service, event, version string, err error) {
	parts := strings.Split(ceType, ".")
	if len(parts) != 4 {
		return "", "", "", "", fmt.Errorf("subject: ce-type %q must be <namespace>.<service>.<event>.<version>", ceType)
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", "", fmt.Errorf("subject: ce-type %q has an empty token", ceType)
		}
	}
	return parts[0], parts[1], parts[2], parts[3], nil
}

// namespaceOf returns just the ce-type's subject root.
func namespaceOf(ceType string) (string, error) {
	ns, _, _, _, err := decompose(ceType)
	return ns, err
}
```

In `Render`, update the call site and add the case:

```go
	namespace, service, event, version, err := decompose(ceType)
	if err != nil {
		return "", err
	}
```

```go
		case "namespace":
			parts[i] = namespace
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/subject/ -run RootsOnTheCEType`
Expected: PASS. Other tests in the package will still fail — Task 2 fixes the binding, and the pre-existing goldens are updated in Task 2 Step 5.

- [ ] **Step 5: Commit**

```bash
git add internal/subject/
git commit -m "feat(subject): root subjects on the ce-type namespace

The ce-type's first token names the schema family — openits for catalog
events, openits-collector for the collector-owned health schema — and
decompose deliberately discarded it so both shared one subject root.

That discard was made for the pre-template scheme, before health and
telemetry had reason to diverge. They now do: different retention,
different consumers, different access control. Rooting each family on its
own namespace is what makes those separable."
```

---

### Task 2: `Bindings()` returns one static filter per namespace

**Files:**
- Modify: `internal/subject/subject.go`
- Test: `internal/subject/subject_test.go`

**Interfaces:**
- Consumes: `namespaceOf` from Task 1.
- Produces: `func (t *Template) Bindings(ceTypes []string) ([]string, error)` — sorted, deduped, one per distinct namespace. Replaces `Binding() string`, which is deleted. `ValidateCETypes` keeps its existing signature `(ceTypes []string, deviceIDs []string) error`.

**Why the signature takes ce-types, not namespaces:** the caller already has the emitters' `CETypes()` and would otherwise have to extract namespaces itself, duplicating `decompose` outside this package. Deriving them here keeps ce-type parsing in one place.

- [ ] **Step 1: Write the failing test**

```go
func TestBindingsAreOnePerNamespace(t *testing.T) {
	tmpl, _ := New(Config{}, Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	got, err := tmpl.Bindings([]string{
		"openits.signal-control.fault-raised.v1",
		"openits.dms.mode-changed.v1", // same namespace: must not duplicate
		"openits-collector.health.collector-started.v1",
	})
	if err != nil {
		t.Fatalf("Bindings: %v", err)
	}
	want := []string{
		"openits-collector.us-tx.txdot.d07.>",
		"openits.us-tx.txdot.d07.>",
	}
	if len(got) != len(want) {
		t.Fatalf("Bindings() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Bindings()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBindingsRejectATemplateWithNoStaticPrefix(t *testing.T) {
	// A leftmost per-event token other than namespace leaves nothing static
	// to bind on, so the stream filter would be ">" and would capture every
	// subject on the server. That guard predates this change and must survive
	// it: namespace is now leftmost and per-event, so the check has to run
	// AFTER namespace substitution rather than before.
	tmpl, err := New(Config{Template: "{service}.{namespace}.{event}"},
		Identity{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tmpl.Bindings([]string{"openits.signal-control.fault-raised.v1"}); err == nil {
		t.Error("a template whose leftmost token varies per event must be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/subject/ -run Bindings`
Expected: FAIL — `tmpl.Bindings` undefined.

- [ ] **Step 3: Implement**

Delete `Binding()` and `deriveBinding`. Remove the `binding` field from `Template` and the `deriveBinding` call in `New` — binding derivation now happens on demand, because it depends on which namespaces the emitters produce and `New` does not know them.

```go
// Bindings returns the JetStream subject filters that capture everything this
// template can render, one per distinct ce-type namespace, sorted.
//
// One filter per namespace rather than one overall: the namespace is the
// leftmost token and varies per event, so a single filter would have to be
// ">" — which would capture every subject on the server, including other
// tenants sharing the broker.
func (t *Template) Bindings(ceTypes []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, ceType := range ceTypes {
		ns, err := namespaceOf(ceType)
		if err != nil {
			return nil, err
		}
		if seen[ns] {
			continue
		}
		seen[ns] = true
		b, err := t.bindingFor(ns)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	sort.Strings(out)
	return out, nil
}

// bindingFor substitutes one namespace and truncates at the first remaining
// per-event token.
func (t *Template) bindingFor(namespace string) (string, error) {
	var prefix []string
	for _, tok := range t.tokens {
		switch tok.perEventVar {
		case "":
			prefix = append(prefix, tok.literal)
		case "namespace":
			prefix = append(prefix, namespace)
		default:
			// First genuinely per-event token: the static prefix ends here.
			if len(prefix) == 0 {
				return "", fmt.Errorf("subject: template %q has no static prefix — its leftmost token varies per event, "+
					"so the stream binding would be \">\" and would capture every subject on the server. "+
					"Put constant tokens (the namespace, region, agency) leftmost", t.raw)
			}
			return strings.Join(prefix, ".") + ".>", nil
		}
	}
	if len(prefix) == 0 {
		return "", fmt.Errorf("subject: template %q rendered an empty binding", t.raw)
	}
	// No per-event tokens at all: every event renders one subject. Legal, if
	// odd; bind it exactly rather than with a wildcard.
	return strings.Join(prefix, "."), nil
}
```

Add `"sort"` to the imports.

Update `ValidateCETypes` to check membership against the binding for that ce-type's own namespace:

```go
			ns, err := namespaceOf(ceType)
			if err != nil {
				return err
			}
			binding, err := t.bindingFor(ns)
			if err != nil {
				return err
			}
			if !withinBinding(subj, binding) {
				return fmt.Errorf("subject: ce-type %q with device %q renders to %q, outside the stream binding %q", ceType, id, subj, binding)
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/subject/ -run Bindings`
Expected: PASS.

- [ ] **Step 5: Update the pre-existing subject tests**

`TestDefaultTemplateGolden` and `TestBindingDerivation` assert the single-root grammar and will fail. Update the golden to the namespace-rooted form:

```go
		"openits.signal-control.fault-raised.v1":              "openits.us-tx.metro-atlanta.d07.signal-control.dev-1.fault-raised",
		"openits.signal-control.operational-status-report.v1": "openits.us-tx.metro-atlanta.d07.signal-control.dev-1.operational-status-report",
		"openits-collector.health.device-status-changed.v1":   "openits-collector.us-tx.metro-atlanta.d07.health.dev-1.device-status-changed",
		"openits-collector.health.collector-started.v1":       "openits-collector.us-tx.metro-atlanta.d07.health.dev-1.collector-started",
```

Rewrite each `TestBindingDerivation` case to call `Bindings` with a representative ce-type and compare against a `[]string`. Any case whose template used `{prefix}` as the root keeps working — `prefix` remains an ordinary operator var — but its expected binding is now returned as a one-element slice.

- [ ] **Step 6: Run the package and commit**

Run: `go test ./internal/subject/ -v`
Expected: PASS (whole package).

```bash
git add internal/subject/
git commit -m "feat(subject): derive one stream binding per ce-type namespace

Binding() returned a single filter derived by truncating at the first
per-event token. With namespace leftmost and per-event, that single
filter would be \">\" — capturing every subject on the server, including
other tenants sharing the broker.

Bindings() substitutes each namespace the emitters can produce and
truncates after it, yielding one static filter per subject root. The
no-static-prefix guard survives, moved after namespace substitution so
it still catches a template whose leftmost token genuinely varies."
```

---

### Task 3: Publisher provisions a stream per binding

**Files:**
- Modify: `internal/publish/publish.go`
- Test: `internal/publish/publish_test.go`
- Modify: `internal/config/config.go` (drop `Subject.Stream`, `StreamName()`)
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `Template.Bindings(ceTypes)` from Task 2.
- Produces: `func Connect(ctx context.Context, url string, tmpl *subject.Template, ceTypes []string) (*Publisher, error)`. `Publish` keeps `(ctx, env, ceType, deviceID)`.
- Removed: `config.Config.Subject.Stream` and `config.StreamName()`.

**Why stream names are derived, not configured:** with two streams a single `stream:` value is meaningless, and a two-entry config block is a second source of truth that can disagree with the bindings it is supposed to match. Deriving the name from the binding makes them structurally incapable of disagreeing. Adding an override later is additive if an operator ever needs one.

- [ ] **Step 1: Write the failing test**

```go
func TestStreamNameForBinding(t *testing.T) {
	for binding, want := range map[string]string{
		"openits.us-ga.metro.d01.>":           "OPENITS-US-GA-METRO-D01",
		"openits-collector.us-ga.metro.d01.>": "OPENITS-COLLECTOR-US-GA-METRO-D01",
		"traffic.southeast.metro.>":           "TRAFFIC-SOUTHEAST-METRO",
	} {
		if got := StreamNameForBinding(binding); got != want {
			t.Errorf("StreamNameForBinding(%q) = %q, want %q", binding, got, want)
		}
	}
}

func TestConnectProvisionsAStreamPerNamespace(t *testing.T) {
	ns := startNATS(t)
	ctx := context.Background()
	tmpl, err := subject.New(subject.Config{},
		subject.Identity{Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Connect(ctx, ns.ClientURL(), tmpl, []string{
		"openits.signal-control.fault-raised.v1",
		"openits-collector.health.collector-started.v1",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)

	for name, wantSubject := range map[string]string{
		"OPENITS-US-GA-METRO-D01":           "openits.us-ga.metro.d01.>",
		"OPENITS-COLLECTOR-US-GA-METRO-D01": "openits-collector.us-ga.metro.d01.>",
	} {
		st, err := js.Stream(ctx, name)
		if err != nil {
			t.Fatalf("stream %s not provisioned: %v", name, err)
		}
		info, err := st.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(info.Config.Subjects) != 1 || info.Config.Subjects[0] != wantSubject {
			t.Errorf("stream %s subjects = %q, want [%q]", name, info.Config.Subjects, wantSubject)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/publish/ -run 'StreamName|PerNamespace'`
Expected: FAIL — `StreamNameForBinding` undefined and `Connect` still takes a stream name.

- [ ] **Step 3: Implement**

In `internal/publish/publish.go`:

```go
// StreamNameForBinding derives a stream name from its subject filter, so the
// two cannot disagree. Uppercased, dots to dashes, trailing wildcard dropped:
// "openits.us-ga.metro.d01.>" becomes "OPENITS-US-GA-METRO-D01".
func StreamNameForBinding(binding string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(binding, ">"), ".")
	return strings.ToUpper(strings.ReplaceAll(trimmed, ".", "-"))
}

// Connect dials NATS and ensures one stream per ce-type namespace. Health and
// catalog events live in separate streams so they can carry different
// retention and different subject permissions (ADR 0011).
func Connect(ctx context.Context, url string, tmpl *subject.Template, ceTypes []string) (*Publisher, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("publish: subject template is required")
	}
	bindings, err := tmpl.Bindings(ceTypes)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("publish: no ce-types, so no stream to provision")
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
	for _, b := range bindings {
		if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:       StreamNameForBinding(b),
			Subjects:   []string{b},
			Storage:    jetstream.FileStorage,
			Duplicates: dedupWindow,
		}); err != nil {
			nc.Close()
			return nil, fmt.Errorf("ensure stream for %s: %w", b, err)
		}
	}
	return &Publisher{nc: nc, js: js, tmpl: tmpl}, nil
}
```

Add `"strings"` to the imports.

In `internal/config/config.go`, delete the `Stream` field from `Subject` and delete `StreamName()` entirely. Remove the now-unused `"strings"` import if nothing else uses it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/publish/ -run 'StreamName|PerNamespace'`
Expected: PASS.

- [ ] **Step 5: Update the remaining publish and config tests**

In `internal/publish/publish_test.go`, every `Connect(ctx, url, tmpl, "SOME-STREAM")` becomes `Connect(ctx, url, tmpl, []string{...})` naming the ce-types that test publishes, and any `js.Stream(ctx, "EDGE-METRO-CAB1")` lookup becomes the derived name for that template's binding.

In `internal/config/config_test.go`, delete `TestLoadSubjectStreamOnly` and remove `stream:` from any YAML fixture and the `StreamName()` assertion from `TestLoadSubjectCustom`.

- [ ] **Step 6: Run both packages and commit**

Run: `go test ./internal/publish/ ./internal/config/`
Expected: PASS.

```bash
git add internal/publish/ internal/config/
git commit -m "feat(publish): provision one stream per ce-type namespace

Health and catalog events now land in separate streams, which is the
point of the split: they want different retention (operational churn vs
archival telemetry) and different subject permissions (a data platform
should not need collector internals to read signal events).

Stream names derive from their binding rather than from config. With two
streams a single stream: value is meaningless, and a two-entry block
would be a second source of truth free to disagree with the bindings it
is supposed to match. Deriving makes that disagreement impossible; an
override stays additive if anyone needs one."
```

---

### Task 4: App passes ce-types through to validation and provisioning

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/app_test.go` (existing e2e must keep passing)

**Interfaces:**
- Consumes: `publish.Connect(ctx, url, tmpl, ceTypes)`, `Template.Bindings`.
- Produces: no new exported API.

- [ ] **Step 1: Write the failing test**

```go
func TestRunProvisionsBothStreams(t *testing.T) {
	// The spine must provision a stream for every namespace its emitters can
	// produce. Missing one means those events publish into a subject space no
	// stream captures — they vanish with no error anywhere.
	ns := startTestNATS(t)
	runCollectorAgainst(t, ns, 400*time.Millisecond)

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	for _, name := range []string{
		"OPENITS-US-GA-METRO-D01",
		"OPENITS-COLLECTOR-US-GA-METRO-D01",
	} {
		if _, err := js.Stream(context.Background(), name); err != nil {
			t.Errorf("stream %s not provisioned: %v", name, err)
		}
	}
}
```

Extract `startTestNATS(t)` and `runCollectorAgainst(t, ns, window)` from the existing `runCollectorAndCollect` helper in `conformance_test.go` so both files share them; `runCollectorAndCollect` then calls them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run BothStreams`
Expected: FAIL — compile error, `Connect` signature changed.

- [ ] **Step 3: Implement**

In `internal/app/app.go`:

```go
	if err := tmpl.ValidateCETypes(ceTypes, cfg.DeviceIDs()); err != nil {
		return err
	}

	pub, err := publish.Connect(ctx, natsURL, tmpl, ceTypes)
	if err != nil {
		return err
	}
```

`ceTypes` is already collected from the emitter chain immediately above; no other change.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run BothStreams`
Expected: PASS.

- [ ] **Step 5: Run the whole suite and commit**

Run: `make check && go test ./... -race`
Expected: both exit 0.

```bash
git add internal/app/
git commit -m "feat(app): provision a stream for every emitter namespace

The ce-type list gathered for boot validation now also drives stream
provisioning, so the two cannot drift. A namespace with no stream would
publish into a subject space nothing captures — events would vanish with
no error at any layer, which is the failure mode boot validation exists
to prevent."
```

---

### Task 5: Conformance test asserts health is off the catalog space

**Files:**
- Modify: `internal/app/conformance_test.go`

**Interfaces:**
- Consumes: helpers from Task 4.
- Produces: none.

- [ ] **Step 1: Rewrite the divergence test as a separation test**

Replace `TestHealthEventsAreKnowinglyOutsideTheProfile` with:

```go
// TestHealthEventsAreOffTheCatalogSubjectSpace is the assertion that makes the
// separation real rather than incidental.
//
// Health carries openits-collector.* ce-types, which the profile's ce-type
// regex rejects by design (ADR 0007). While health shared the catalog subject
// root, a Tier 2 harness pointed at `openits.>` flagged every health event as
// non-conformant. Rooting health on its own namespace removes that false
// negative — but only for as long as nothing drifts back, which is what this
// test holds in place.
func TestHealthEventsAreOffTheCatalogSubjectSpace(t *testing.T) {
	msgs := runCollectorAndCollect(t, 900*time.Millisecond) // subscribes ">"

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
```

- [ ] **Step 2: Widen the collector subscription and scope the Tier 2 test**

In `runCollectorAndCollect`, change the subscription from `"openits.>"` to `">"` so health is observable at all. Then in `TestTier2ProfileConformance`, skip anything not on the catalog root, and replace the ce-type-prefix scoping comment:

```go
	for _, m := range msgs {
		// Tier 2 is assessed over the catalog subject space. Health lives on
		// its own root (ADR 0011) and is asserted separately.
		if !strings.HasPrefix(m.subject, "openits.") {
			continue
		}
```

Keep the existing `sawOpenITS` guard — with the subscription widened it now genuinely proves catalog events were observed rather than merely that something arrived.

- [ ] **Step 3: Run and verify it fails when health leaks back**

Run: `go test ./internal/app/ -run 'Tier2|OffTheCatalog'`
Expected: PASS.

Then prove the guard bites — temporarily force one root by editing `internal/subject/subject.go` `Render` to `parts[i] = "openits"` in the `namespace` case:

Run: `go test ./internal/app/ -run OffTheCatalog`
Expected: FAIL with `health ce-type ... published on ... which is inside the catalog space`.

Revert the edit and confirm green again.

- [ ] **Step 4: Commit**

```bash
git add internal/app/
git commit -m "test(app): assert health stays off the catalog subject space

The previous test pinned health's divergence as a known trade-off:
conformant subject, non-conformant ce-type, flagged by any Tier 2 harness
watching openits.>. The split removes that, and this holds it removed.

Also widens the e2e subscription from openits.> to >, without which the
collector would have stopped observing health entirely and every health
assertion would have passed by seeing nothing."
```

---

### Task 6: ADR 0011 and operator documentation

**Files:**
- Create: `docs/adr/0011-namespace-rooted-subject-spaces.md`
- Modify: `docs/adr/README.md`, `collector.yaml`, `README.md`
- Modify: `docs/specs/2026-07-21-openits-models-emitter-design.md` (Revisions section)

**Interfaces:** none.

- [ ] **Step 1: Write ADR 0011**

Follow the house format exactly — Status, Context, Decision, Consequences, Alternatives considered — and note that it amends ADR 0009's grammar consequence rather than superseding the ADR. Content to cover:

- **Context:** health and telemetry shared a subject root because the pre-template scheme did; they have since diverged in retention, consumers, and access control, and a Tier 2 harness on `openits.>` flags health as non-conformant.
- **Decision:** stated as the GENERAL rule — the ce-type namespace roots the
  subject space, separating collector-internal traffic from ITS-domain
  traffic — with health named as its first application rather than its
  justification. A later collector control channel then lands under an
  existing decision instead of needing a new one. Also: one stream per
  namespace; stream names derive from bindings; `subject.stream` is removed.
- **Consequences:** two streams to provision and monitor; subject permissions
  become per-root; a consumer wanting both subscribes to two roots or `>`; the
  default grammar and both bindings changed, so any pre-existing stream must be
  reprovisioned — free only because there are no deployments.
- **Scope limit on "one stream per namespace", recorded explicitly.** The rule
  holds for outbound event families and does NOT generalise to everything a
  namespace may eventually carry. A collector control channel would be
  inbound and carry write authority: it wants tighter subject permissions than
  health and probably a work-queue stream, or no stream at all if it is
  request/reply. Under the present rule it would share health's stream purely
  for sharing a namespace, which would be wrong. Splitting the binding more
  finely — per (namespace, service class) — is the likely evolution, and it is
  additive. Recorded so the next reader sees a rule with a known boundary
  rather than one they assume generalises.
- **Not decided here:** ADR 0004 remains pull-only, and the subject engine is
  publish-only — `Render` has no subscribe-pattern counterpart. An inbound
  channel needs both, and neither is required to keep this decision open.
- **Alternatives considered:** keep the shared root and scope the conformance claim (rejected: leaves retention and permissions unsolvable, and trains people to ignore harness output); rename health ce-types into `openits.collector-health.*` (rejected: passes the regex by claiming membership in an authority namespace whose registry has no such service — converts a visible failure into an invisible lie).

- [ ] **Step 2: Add the row to `docs/adr/README.md`**

```markdown
| [0011](0011-namespace-rooted-subject-spaces.md) | Namespace-rooted subject spaces, one stream per namespace (amends 0009) |
```

- [ ] **Step 3: Update `collector.yaml`**

Replace the subject block's documentation: the default template is now `{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}`; `{namespace}` is a per-event token taken from the ce-type; the collector provisions `OPENITS-<REGION>-<AGENCY>-<UNIT>` and `OPENITS-COLLECTOR-<REGION>-<AGENCY>-<UNIT>`; `stream:` no longer exists and stream names derive from the bindings. Add `namespace` to the reserved-names list.

- [ ] **Step 4: Update `README.md`**

The architecture bullet currently names the seven-token grammar rooted on `openits`. Change it to show both roots and say in one line why they are separate.

- [ ] **Step 5: Add a Revisions entry to the Plan 2 spec**

Record that §4's subject decision was amended by ADR 0011 after the conformance work exposed the health/catalog namespace conflict, so the spec does not read as current where it has been overtaken.

- [ ] **Step 6: Verify and commit**

Run: `make check && go test ./... -race`
Expected: both exit 0. `cmd/collector`'s `TestExampleConfigIsValid` covers the edited `collector.yaml`.

```bash
git add docs/ collector.yaml README.md
git commit -m "docs: ADR 0011 records namespace-rooted subject spaces

ADR records are immutable once accepted, so this amends 0009's grammar
consequence as a new record rather than editing it.

Documents the trade explicitly: two streams to provision and monitor, and
a consumer wanting both families now subscribes to two roots or to >. The
gain is that retention and subject permissions become expressible per
family, which one shared root made impossible."
```

---

## Self-Review

**Spec coverage.** Every element of the agreed design maps to a task: namespace token (1), per-namespace bindings (2), stream-per-namespace and config cleanup (3), spine wiring (4), conformance assertions (5), ADR and docs (6).

**Known gaps, deliberate:**
- **Retention is not yet differentiated.** Both streams get identical `FileStorage` + `Duplicates` config. Separating the roots is what makes differing retention *possible*; choosing the values is an operational decision with no current requirement, so it stays out (YAGNI). Adding per-stream limits later is additive and needs no subject change.
- **`subject.stream` is removed rather than migrated.** There are no deployments to migrate.

**Type consistency check.** `Bindings(ceTypes []string) ([]string, error)` is defined in Task 2 and consumed in Tasks 3 and 4 with that exact signature. `bindingFor(namespace string)` is unexported and used only within `internal/subject` (Task 2, both `Bindings` and `ValidateCETypes`). `StreamNameForBinding(binding string) string` is defined and consumed in Task 3. `decompose` gains a leading return value in Task 1; its only callers are `Render` and the new `namespaceOf`, both updated in that task. `Binding() string` and `config.StreamName()` are deleted in Tasks 2 and 3 respectively, and no later task references either.

**Ordering constraint.** Tasks 1 and 2 leave `internal/subject` tests red between them — Task 1's Step 4 says so explicitly. Every other task is green at its own commit. If a reviewer needs each commit independently green, squash 1 and 2.
