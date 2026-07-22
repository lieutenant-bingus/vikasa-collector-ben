# Gen2 Plan 1: ADRs, SDK Contract, and Core Spine — Implementation Plan


**Goal:** Greenfield rebuild step 1: rationale ADRs, the public adapter SDK (`sdk/model`, `sdk/adapter`, `sdk/transport/snmp`), the boundary lint, and a running core spine (config → runner → synth → health emitter → CloudEvents → local JetStream) with the `ntcip-asc` adapter on fixtures.

**Architecture:** Per `docs/specs/2026-07-12-greenfield-collector-architecture-design.md`. Adapters return collector-owned `sdk/model` types; the core diffs snapshots into domain events; wire emitters encode events to `(payload, ce-type)`; the publisher wraps them in CloudEvents on tenant-scoped subjects. Plan 1 publishes **health events only** (collector-owned schema) — the openits-models emitter is Plan 2, so this plan has **no openits-models dependency at all**.

**Tech Stack:** Go 1.26, gosnmp v1.43.2, nats.go v1.52.0, nats-server v2 (test-only, embedded), yaml.v3.

## Global Constraints

- Work happens on branch `gen2`, created from `main`. Gen-1 code is deleted in Task 0 (git history preserves it). `docs/` is kept.
- Module path stays `github.com/Vikasa2M/openits-collector`; Go `1.26`.
- **Boundary rule:** `sdk/...` and `internal/vendors/...` must never import `github.com/Vikasa2M/openits-models`. In Plan 1 *nothing* imports it (no `require` at all).
- No `replace` directives in go.mod, ever.
- Commit style: conventional commits, **no Co-Authored-By trailers**.
- All times UTC; anything hashed or goldened iterates in sorted order; tests use fixed timestamps (no clock seams beyond an injectable `now func() time.Time` where the runner needs it).
- Subjects: `openits.<agency>.<site>.<rest-of-ce-type>`; tenant tokens must match `^[a-z0-9][a-z0-9-]*$`.
- Run `make check` (test + lint-boundary + vet) before every commit claim.

---

### Task 0: Branch, wipe, scaffold

**Files:**
- Delete: `cmd/`, `internal/`, `sdk/`, `configs/`, `go.mod`, `go.sum`, `Makefile`, `Dockerfile.poller`, `bin/`
- Keep: `docs/`, `README.md` (rewritten later), `.gitignore` (rewritten now)
- Create: `go.mod`, `Makefile`, `.gitignore`, `scripts/lint-boundary.sh`

**Interfaces:**
- Produces: empty module `github.com/Vikasa2M/openits-collector` (go 1.26); `make check` target = `go vet ./... && go test ./... && scripts/lint-boundary.sh`.

- [ ] **Step 1: Branch and wipe**

```bash
git checkout -b gen2
git rm -r cmd internal sdk configs go.mod go.sum Makefile Dockerfile.poller
git rm -r bin 2>/dev/null || true
```

- [ ] **Step 2: Scaffold module, Makefile, lint script**

`go.mod`:
```
module github.com/Vikasa2M/openits-collector

go 1.26
```

`.gitignore`:
```
bin/
*.test
```

`Makefile`:
```make
.PHONY: build test vet lint-boundary check

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

lint-boundary:
	./scripts/lint-boundary.sh

check: vet test lint-boundary
```

`scripts/lint-boundary.sh` (mark executable):
```bash
#!/usr/bin/env bash
# Boundary rule: sdk/ and internal/vendors/ (and in Plan 1, everything)
# must not depend on openits-models. Only internal/wire/ may (Plan 2+).
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
for pkgroot in ./sdk/... ./internal/vendors/...; do
  if go list -deps "$pkgroot" 2>/dev/null | grep -q 'github.com/Vikasa2M/openits-models'; then
    echo "BOUNDARY VIOLATION: $pkgroot depends on openits-models" >&2
    fail=1
  fi
done
exit $fail
```

```bash
chmod +x scripts/lint-boundary.sh
```

- [ ] **Step 3: Verify empty module is green**

Run: `make check`
Expected: `go vet`/`go test` report no packages (exit 0); lint-boundary exits 0.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore!: wipe gen-1 code for greenfield rebuild (gen2)

Gen-1 remains in git history. Spec:
docs/specs/2026-07-12-greenfield-collector-architecture-design.md"
```

---

### Task 1: ADR set — the "why" documentation

**Files:**
- Create: `docs/adr/README.md`, `docs/adr/0001-greenfield-rebuild.md`, `docs/adr/0002-domain-model-and-wire-emitter-boundary.md`, `docs/adr/0003-stable-sdk-in-tree-adapters.md`, `docs/adr/0004-pull-only-state-and-event-readers.md`, `docs/adr/0005-one-catalog-version-per-instance.md`, `docs/adr/0006-tenant-scoped-subjects.md`, `docs/adr/0007-collector-owned-health-schema.md`, `docs/adr/0008-fixture-golden-testing-bar.md`

**Interfaces:**
- Produces: `docs/adr/` referenced by README and future contributor docs. No code.

- [ ] **Step 1: Write the ADRs**

`docs/adr/README.md`:
```markdown
# Architecture Decision Records

Why the collector is built the way it is. Format per record: Status,
Context, Decision, Consequences, Alternatives considered. Records are
immutable once accepted; reversals get a new ADR that supersedes the old.

| # | Decision |
|---|---|
| [0001](0001-greenfield-rebuild.md) | Rebuild the collector greenfield |
| [0002](0002-domain-model-and-wire-emitter-boundary.md) | Collector-owned domain model + versioned wire emitters |
| [0003](0003-stable-sdk-in-tree-adapters.md) | Stable SDK, in-tree adapters (Telegraf model) |
| [0004](0004-pull-only-state-and-event-readers.md) | Pull-only; StateReader vs EventReader split by semantics |
| [0005](0005-one-catalog-version-per-instance.md) | One catalog version per collector instance |
| [0006](0006-tenant-scoped-subjects.md) | Tenant-scoped NATS subjects; CE type = catalog ce-type |
| [0007](0007-collector-owned-health-schema.md) | Collector-owned health event schema |
| [0008](0008-fixture-golden-testing-bar.md) | Fixture-golden testing bar for adapters |

Companion spec: `../specs/2026-07-12-greenfield-collector-architecture-design.md`
```

`docs/adr/0001-greenfield-rebuild.md`:
```markdown
# ADR 0001: Rebuild the collector greenfield

**Status:** Accepted (2026-07-12)

## Context
The gen-1 collector grew four divergent ingest paths and three inconsistent
organizing axes (drivers by transport, translators by device-type, decoders
by vendor). A 2026-07 restructure (P0–P4a) hardened it and introduced a
vendor×device-kind adapter registry, but its "internal" `Reading` struct
embedded openits-models proto enums directly — so a YANG-driven regeneration
of openits-models broke the entire build. The wire model and the collector's
working model were the same types; upstream schema churn reached every
translator.

## Decision
Rebuild from scratch on branch `gen2` per the 2026-07-12 architecture spec.
Gen-1 code is deleted (git history preserves it) and mined for lessons:
golden determinism via fixed timestamps, sorted iteration for
content-addressed CE ids, per-device serialized SNMP I/O, panic-guarded
long-lived goroutines, and "a failed read must never synthesize a
state-change event."

## Consequences
Short gap with no shippable binary until the Plan 1 spine lands. In
exchange: no dead code shadowing new code, no gen-1 compatibility drag, and
the openits-models dependency re-enters only behind the wire-emitter
boundary (ADR 0002).

## Alternatives considered
Incremental refactor of gen-1 (rejected: the model coupling was load-bearing
everywhere); parallel build in-tree (rejected: two architectures, colliding
`sdk/` paths); new repository (rejected: loses history/doc continuity).
```

`docs/adr/0002-domain-model-and-wire-emitter-boundary.md`:
```markdown
# ADR 0002: Collector-owned domain model + versioned wire emitters

**Status:** Accepted (2026-07-12)

## Context
The collector must survive three model-change scenarios: (S1) codegen
rename/renumber churn in openits-models, (S2) different deployments pinned
to different model releases at once, (S3) wholesale replacement of
openits-models someday. With 10+ contributed vendor adapters planned, any
design where adapters touch wire types makes each scenario an
every-adapter problem.

## Decision
Adapters produce only collector-owned `sdk/model` types (facets + events).
Exactly one layer, `internal/wire/<version>`, imports openits-models and
maps domain events to `(proto payload, ce-type)`. The rule is mechanical:
CI fails if `sdk/` or `internal/vendors/` transitively import
openits-models. openits-models is consumed as tagged semver releases —
never a `replace` on a moving checkout.

## Consequences
S1 = edit one emitter package. S2 = compile two emitter packages, config
picks one. S3 = write a new emitter family. Cost: a permanent mapping layer
(every event exists in domain + mapping form) and a second schema to govern
— accepted because the mapping is dumb, golden-tested, and cheaper than
coordinating breaking changes across contributed adapters. The domain model
may be richer than any wire version; gaps become explicit emitter
map-or-drop decisions instead of collection blockers.

## Alternatives considered
Pin openits-models harder and shim on majors (rejected: fails S2/S3
structurally; makes proto types the contributor API). Declarative mapping
engine (rejected as foundation: mapping DSLs grow into bad programming
languages; kept as a possible future library inside adapter families).
```

`docs/adr/0003-stable-sdk-in-tree-adapters.md`:
```markdown
# ADR 0003: Stable SDK, in-tree adapters (Telegraf model)

**Status:** Accepted (2026-07-12)

## Context
The core is open source and adapters will be contributed by people we don't
control. Go's runtime `plugin` package requires exact toolchain matches and
is effectively unusable for community distribution; subprocess plugins add
operational weight on cabinet edge hardware.

## Decision
Adapters compile in-tree, registered against a small semver-disciplined
public surface: `sdk/model`, `sdk/adapter`, optional `sdk/transport/*`
helpers. Contribution = PR adding `internal/vendors/<vendor>/<kind>/` plus
fixtures (ADR 0008). Registry key: `<vendor>-<device_kind>`; `ntcip` is
itself a vendor — the generic standards-only implementation and compat
target.

## Consequences
Interface changes to `sdk/` are breaking changes and are treated that way.
An out-of-tree story later is a subprocess-bridge *adapter* — additive, no
rearchitecture. Single Go module for now; splitting `sdk/` into its own
module is deferred until out-of-tree adapters actually exist.

## Alternatives considered
Go runtime plugins (rejected: toolchain fragility). hashicorp/go-plugin
subprocesses as the primary model (rejected for v1: deployment weight at
the edge; kept open as a future bridge).
```

`docs/adr/0004-pull-only-state-and-event-readers.md`:
```markdown
# ADR 0004: Pull-only; StateReader vs EventReader split by semantics

**Status:** Accepted (2026-07-12)

## Context
Every device in the cabinet is polled — nothing pushes. But pull transport
≠ snapshot semantics: an ASC status poll returns *state* (diff it against
the previous poll), while an ATSPM high-res log fetch returns *discrete
events* (nothing to diff).

## Decision
Two adapter read interfaces, split by what the data means, not how it
travels: `StateReader.Read → *model.Snapshot` (core diffs consecutive
snapshots via synth) and `EventReader.Fetch → []model.Event` (forwarded
directly to emitters). No push machinery anywhere. A `Commander` capability
is reserved but dormant — v1 is collect-only by decision; commands bolt on
later without breaking any adapter.

## Consequences
The core has no concept of transport at all. Synth logic is written once
against facets. EventReader checkpointing (don't re-emit the same log
window) is deferred to the first log-shaped adapter.

## Alternatives considered
One interface with event-vs-state discrimination inside payloads (rejected:
pushes semantics into every consumer); push/callback sinks (rejected: no
push sources exist; YAGNI).
```

`docs/adr/0005-one-catalog-version-per-instance.md`:
```markdown
# ADR 0005: One catalog version per collector instance

**Status:** Accepted (2026-07-12)

## Context
openits-models versions events *per ce-type* (`openits.<service>.<event>.v1`)
and publishes catalog snapshots per release. Deployments migrate at
different speeds (S2 in ADR 0002).

## Decision
Each collector instance selects exactly one catalog version at boot
(`model_version` in config), instantiating one wire emitter. Version
coexistence happens across the fleet — different cabinets pinned to
different releases — never inside one instance.

## Consequences
No per-event fan-out, no double publish volume, no version routing inside
the collector. A cabinet migrates by config change + restart. If a true
dual-emit migration window is ever required, it's a new ADR (publisher
fan-out), not a rearchitecture.

## Alternatives considered
Dual-emit per instance (rejected: 2x volume and subject ambiguity for a
window nobody has needed yet); per-stream version routing (rejected:
complexity without a driving consumer).
```

`docs/adr/0006-tenant-scoped-subjects.md`:
```markdown
# ADR 0006: Tenant-scoped NATS subjects; CE type = catalog ce-type

**Status:** Accepted (2026-07-12)

## Context
The generated AsyncAPI in openits-models defines channel addresses as bare
ce-types (`openits.signal-control.fault-raised.v1`) with no tenancy. Events
from many cabinets eventually aggregate upstream; consumers need to route
by agency/site/service without parsing payloads.

## Decision
Two distinct concepts. CE `type` = the catalog ce-type verbatim (schema
identity, matches AsyncAPI exactly). NATS subject = the ce-type with tenant
spliced after the first token: `openits.<agency>.<site>.<service>.<event>.v<n>`.
Device identity lives in CE `source` (`//<agency>/<site>/<device-id>`), not
the subject. CE `id` is content-addressed (deterministic hash) so JetStream
dedup survives restarts. Tenant tokens are validated (`^[a-z0-9][a-z0-9-]*$`)
so they can never corrupt subject grammar.

## Consequences
One stream binding per cabinet (`openits.<agency>.<site>.>`), prefix-based
upstream aggregation, wildcard subscription by service or event across
sites. AsyncAPI documents the address with parameters. Subject-per-device
cardinality explosion is avoided.

## Alternatives considered
Subject = ce-type verbatim (rejected: routing/aggregation too weak once
events leave the cabinet); version-first hierarchy `openits.v1.<agency>...`
(rejected: diverges most from catalog strings for little gain).
```

`docs/adr/0007-collector-owned-health-schema.md`:
```markdown
# ADR 0007: Collector-owned health event schema

**Status:** Accepted (2026-07-12)

## Context
Device reachability, poll failures, and collector self-health must be
reportable even when — especially when — the wire model is in flux. Gen-1's
poll heartbeat lost its proto home for a period when upstream regeneration
dropped `OperationalStatus`.

## Decision
Health events use a small collector-owned versioned schema with its own
ce-type namespace (`openits-collector.health.*.v1`), JSON-encoded,
documented in the collector's AsyncAPI. They ride the same tenant-scoped
subject scheme but never pass through the openits-models emitter.

## Consequences
Health can never be hostage to upstream schema churn, and Plan 1's spine
runs end-to-end with zero openits-models dependency. Downstream consumers
accept a second (tiny) schema source. If openits-models later models
equivalent events, mapping them is an optional emitter addition, not a
migration.

## Alternatives considered
Catalog-first with own-schema fallback (rejected by decision: one owner for
all health semantics beats a split); catalog-only (rejected: blocks new
health signals on upstream YANG work).
```

`docs/adr/0008-fixture-golden-testing-bar.md`:
```markdown
# ADR 0008: Fixture-golden testing bar for adapters

**Status:** Accepted (2026-07-12)

## Context
A zoo of contributed adapters is only maintainable if adapters are
reviewable and regression-testable without vendor hardware on the
reviewer's desk.

## Decision
Every adapter PR ships fixtures: recorded raw transport responses (SNMP
values by OID, API JSON bodies) plus the expected `model.Snapshot`/events.
`sdk/transport` packages ship test fakes that replay fixtures. **No
fixtures, no merge.** Core guards: table-driven synth differ tests with
fixed timestamps; emitter goldens (domain event → exact bytes + ce-type);
emitter catalog-conformance (every producible ce-type must exist in the
pinned models release's asyncapi.yaml); byte-literal subject goldens; and
the CI boundary lint (ADR 0002).

## Consequences
Adapter review = read the mapping + eyeball fixtures; hardware-free CI;
drift between collector and models catalog is a CI failure, not a
production surprise. Cost: recording fixtures is real work on first
integration — accepted as the price of admission.

## Alternatives considered
Mock-heavy unit tests (rejected: they test the mock); live-device
integration environments as the bar (rejected: unavailable to most
contributors and to CI).
```

- [ ] **Step 2: Verify and commit**

Run: `ls docs/adr/` — expect the 9 files above.

```bash
git add docs/adr
git commit -m "docs: add ADRs 0001-0008 recording gen2 architecture rationale"
```

---

### Task 2: `sdk/model` — snapshot, facets, enums

**Files:**
- Create: `sdk/model/model.go`, `sdk/model/enums.go`, `sdk/model/signal.go`
- Test: `sdk/model/model_test.go`

**Interfaces:**
- Produces (later tasks depend on these exact names):
  - `type Kind string`; `const KindSignalStatus Kind = "signal-status"`
  - `type Facet interface{ FacetKind() Kind }`
  - `type FacetError struct { Kind Kind; Err string }`
  - `type Snapshot struct { DeviceID string; SampledAt time.Time; Facets []Facet; Errors []FacetError }` with `func (s *Snapshot) Facet(k Kind) (Facet, bool)` and `func (s *Snapshot) FacetFailed(k Kind) bool`
  - `type ControllerMode uint8` with `ModeUnknown, ModeNormal, ModeFlash, ModeStandby, ModeOff` and `String() string`
  - `type SignalStatus struct { Mode ControllerMode; InConflictFlash bool; ActivePlanID uint32; PreemptionActive bool; PreemptionSource string }` implementing `Facet`

- [ ] **Step 1: Write the failing test**

`sdk/model/model_test.go`:
```go
package model

import (
	"testing"
	"time"
)

func TestSnapshotFacetLookup(t *testing.T) {
	s := &Snapshot{
		DeviceID:  "asc-1",
		SampledAt: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
		Facets:    []Facet{SignalStatus{Mode: ModeNormal, ActivePlanID: 3}},
		Errors:    []FacetError{{Kind: Kind("detector-samples"), Err: "timeout"}},
	}

	f, ok := s.Facet(KindSignalStatus)
	if !ok {
		t.Fatal("expected signal-status facet present")
	}
	if got := f.(SignalStatus).ActivePlanID; got != 3 {
		t.Fatalf("ActivePlanID = %d, want 3", got)
	}
	if _, ok := s.Facet(Kind("dms-status")); ok {
		t.Fatal("unexpected facet found")
	}
	if !s.FacetFailed(Kind("detector-samples")) {
		t.Fatal("expected detector-samples marked failed")
	}
	if s.FacetFailed(KindSignalStatus) {
		t.Fatal("signal-status must not be marked failed")
	}
}

func TestControllerModeString(t *testing.T) {
	cases := map[ControllerMode]string{
		ModeUnknown: "unknown", ModeNormal: "normal", ModeFlash: "flash",
		ModeStandby: "standby", ModeOff: "off", ControllerMode(99): "unknown",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", mode, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/`
Expected: FAIL (undefined types).

- [ ] **Step 3: Implement**

`sdk/model/model.go`:
```go
// Package model is the collector-owned domain model: the only currency
// adapters produce and the core consumes. It never imports wire schemas
// (openits-models); mapping to wire types is internal/wire's job (ADR 0002).
package model

import "time"

// Kind identifies a facet family. Facets are per-device-KIND, never
// per-vendor (governance rail in the architecture spec §4).
type Kind string

// Facet is one typed slice of device state within a Snapshot.
type Facet interface{ FacetKind() Kind }

// FacetError records a facet the adapter tried and failed to read this
// poll. Synth suspends diffing for failed facets: absence of evidence is
// never a state change.
type FacetError struct {
	Kind Kind
	Err  string
}

// Snapshot is the state of one device at a single poll.
type Snapshot struct {
	DeviceID  string
	SampledAt time.Time
	Facets    []Facet
	Errors    []FacetError
}

// Facet returns the facet of kind k, if present.
func (s *Snapshot) Facet(k Kind) (Facet, bool) {
	for _, f := range s.Facets {
		if f.FacetKind() == k {
			return f, true
		}
	}
	return nil, false
}

// FacetFailed reports whether the adapter recorded a read failure for k.
func (s *Snapshot) FacetFailed(k Kind) bool {
	for _, e := range s.Errors {
		if e.Kind == k {
			return true
		}
	}
	return false
}
```

`sdk/model/enums.go`:
```go
package model

// ControllerMode is the collector-owned controller mode enum. Wire enums
// live in wire versions; this one never changes because upstream renames.
type ControllerMode uint8

const (
	ModeUnknown ControllerMode = iota
	ModeNormal
	ModeFlash
	ModeStandby
	ModeOff
)

func (m ControllerMode) String() string {
	switch m {
	case ModeNormal:
		return "normal"
	case ModeFlash:
		return "flash"
	case ModeStandby:
		return "standby"
	case ModeOff:
		return "off"
	default:
		return "unknown"
	}
}
```

`sdk/model/signal.go`:
```go
package model

// KindSignalStatus is the signal-controller operational-state facet.
const KindSignalStatus Kind = "signal-status"

// SignalStatus is the operational state of a signal controller at one poll.
type SignalStatus struct {
	Mode             ControllerMode
	InConflictFlash  bool
	ActivePlanID     uint32
	PreemptionActive bool
	PreemptionSource string
}

func (SignalStatus) FacetKind() Kind { return KindSignalStatus }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): domain model core — Snapshot, Facet, SignalStatus, enums"
```

---

### Task 3: `sdk/model` — domain events, health events, command seam

**Files:**
- Create: `sdk/model/events.go`, `sdk/model/health.go`, `sdk/model/command.go`
- Test: `sdk/model/events_test.go`

**Interfaces:**
- Produces:
  - `type Event interface { EventKind() string; EventDeviceID() string; EventOccurredAt() time.Time }`
  - `type Base struct { DeviceID string; OccurredAt time.Time }` (embeddable, implements the accessors)
  - Domain events: `OperationalStatusReport{Base; Mode ControllerMode; InConflictFlash bool; ActivePlanID uint32}` (kind `"operational-status-report"`), `ModeChanged{Base; From, To ControllerMode}` (`"mode-changed"`), `PlanChanged{Base; FromPlanID, ToPlanID uint32}` (`"plan-changed"`), `PreemptionActivated{Base; Source string}` (`"preemption-activated"`), `PreemptionCleared{Base}` (`"preemption-cleared"`)
  - Health events: `DeviceStatusChanged{Base; Reachable bool; Reason string; ConsecutiveFailures int}` (`"device-status-changed"`), `CollectorStarted{Base; Version string}` (`"collector-started"`, DeviceID = "" meaning the collector itself)
  - Command seam (dormant): `type Command interface{ CommandKind() string }`, `SetPlan{PlanID uint32}` (`"set-plan"`)

- [ ] **Step 1: Write the failing test**

`sdk/model/events_test.go`:
```go
package model

import (
	"testing"
	"time"
)

func TestEventKindsAndAccessors(t *testing.T) {
	at := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	b := Base{DeviceID: "asc-1", OccurredAt: at}

	cases := []struct {
		ev   Event
		kind string
	}{
		{OperationalStatusReport{Base: b, Mode: ModeNormal, ActivePlanID: 3}, "operational-status-report"},
		{ModeChanged{Base: b, From: ModeNormal, To: ModeFlash}, "mode-changed"},
		{PlanChanged{Base: b, FromPlanID: 3, ToPlanID: 7}, "plan-changed"},
		{PreemptionActivated{Base: b, Source: "railroad"}, "preemption-activated"},
		{PreemptionCleared{Base: b}, "preemption-cleared"},
		{DeviceStatusChanged{Base: b, Reachable: false, Reason: "timeout", ConsecutiveFailures: 1}, "device-status-changed"},
		{CollectorStarted{Base: Base{OccurredAt: at}, Version: "dev"}, "collector-started"},
	}
	for _, c := range cases {
		if got := c.ev.EventKind(); got != c.kind {
			t.Errorf("EventKind() = %q, want %q", got, c.kind)
		}
		if got := c.ev.EventOccurredAt(); !got.Equal(at) {
			t.Errorf("%s: OccurredAt = %v, want %v", c.kind, got, at)
		}
	}
	if (SetPlan{PlanID: 4}).CommandKind() != "set-plan" {
		t.Error("SetPlan.CommandKind() != set-plan")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/model/` — Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

`sdk/model/events.go`:
```go
package model

import "time"

// Event is a discrete domain occurrence — produced by synth (from
// consecutive Snapshots) or returned directly by EventReader adapters.
// Emitters (internal/wire) are the only consumers that turn Events into
// wire payloads.
type Event interface {
	EventKind() string
	EventDeviceID() string
	EventOccurredAt() time.Time
}

// Base carries the fields every event has; embed it.
type Base struct {
	DeviceID   string
	OccurredAt time.Time
}

func (b Base) EventDeviceID() string     { return b.DeviceID }
func (b Base) EventOccurredAt() time.Time { return b.OccurredAt }

// OperationalStatusReport is the periodic current-state report for a
// signal controller (emitted every poll, not just on change).
type OperationalStatusReport struct {
	Base
	Mode            ControllerMode
	InConflictFlash bool
	ActivePlanID    uint32
}

func (OperationalStatusReport) EventKind() string { return "operational-status-report" }

// ModeChanged fires when the controller mode transitions.
type ModeChanged struct {
	Base
	From, To ControllerMode
}

func (ModeChanged) EventKind() string { return "mode-changed" }

// PlanChanged fires when the active timing plan transitions.
type PlanChanged struct {
	Base
	FromPlanID, ToPlanID uint32
}

func (PlanChanged) EventKind() string { return "plan-changed" }

// PreemptionActivated fires when preemption becomes active.
type PreemptionActivated struct {
	Base
	Source string
}

func (PreemptionActivated) EventKind() string { return "preemption-activated" }

// PreemptionCleared fires when preemption ends.
type PreemptionCleared struct{ Base }

func (PreemptionCleared) EventKind() string { return "preemption-cleared" }
```

`sdk/model/health.go`:
```go
package model

// Health events use a collector-owned wire schema (ADR 0007) and so are
// publishable even with no openits-models emitter configured.

// DeviceStatusChanged fires on reachability transitions (up→down, down→up).
type DeviceStatusChanged struct {
	Base
	Reachable           bool
	Reason              string
	ConsecutiveFailures int
}

func (DeviceStatusChanged) EventKind() string { return "device-status-changed" }

// CollectorStarted fires once at boot. DeviceID is empty: the subject of
// the event is the collector itself.
type CollectorStarted struct {
	Base
	Version string
}

func (CollectorStarted) EventKind() string { return "collector-started" }
```

`sdk/model/command.go`:
```go
package model

// Command is the reserved write-back seam (ADR 0004). v1 is collect-only:
// these types exist so the Commander adapter capability compiles, but
// nothing dispatches them yet.
type Command interface{ CommandKind() string }

// SetPlan requests activation of a timing plan.
type SetPlan struct{ PlanID uint32 }

func (SetPlan) CommandKind() string { return "set-plan" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/model/` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sdk/model
git commit -m "feat(sdk): domain events, health events, dormant command seam"
```

---

### Task 4: `sdk/adapter` — interfaces and registry

**Files:**
- Create: `sdk/adapter/adapter.go`, `sdk/adapter/registry.go`
- Test: `sdk/adapter/registry_test.go`

**Interfaces:**
- Consumes: `model.Snapshot`, `model.Event`, `model.Command` (Tasks 2–3).
- Produces:
  - `type Capability uint8`; `CapState`, `CapEvents`, `CapCommand`; `func (c Capability) Has(Capability) bool`
  - `type Descriptor struct { Vendor, DeviceKind string; Caps Capability }` with `Key() string` = `"<vendor>-<device_kind>"`
  - `type Adapter interface { Descriptor() Descriptor; Close() error }`
  - `type StateReader interface { Adapter; Read(ctx) (*model.Snapshot, error) }`
  - `type EventReader interface { Adapter; Fetch(ctx) ([]model.Event, error) }`
  - `type Commander interface { Adapter; Execute(ctx, model.Command) error }`
  - `type Factory func(deviceID string, conn map[string]any) (Adapter, error)`
  - `type Registry` with `Register(Descriptor, Factory)` (panics on duplicate), `Build(vendor, deviceKind, deviceID string, conn map[string]any) (Adapter, error)`, `Known(vendor, deviceKind string) bool`

- [ ] **Step 1: Write the failing test**

`sdk/adapter/registry_test.go`:
```go
package adapter

import (
	"context"
	"testing"

	"github.com/Vikasa2M/openits-collector/sdk/model"
)

type fakeAdapter struct{ d Descriptor }

func (f *fakeAdapter) Descriptor() Descriptor { return f.d }
func (f *fakeAdapter) Close() error           { return nil }
func (f *fakeAdapter) Read(context.Context) (*model.Snapshot, error) {
	return &model.Snapshot{DeviceID: "x"}, nil
}

func TestRegistryBuildAndKnown(t *testing.T) {
	r := NewRegistry()
	d := Descriptor{Vendor: "ntcip", DeviceKind: "asc", Caps: CapState}
	r.Register(d, func(deviceID string, conn map[string]any) (Adapter, error) {
		return &fakeAdapter{d: d}, nil
	})

	if !r.Known("ntcip", "asc") {
		t.Fatal("ntcip-asc should be known")
	}
	if r.Known("acme", "asc") {
		t.Fatal("acme-asc should be unknown")
	}

	a, err := r.Build("ntcip", "asc", "dev-1", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := a.(StateReader); !ok {
		t.Fatal("built adapter should be a StateReader")
	}
	if !a.Descriptor().Caps.Has(CapState) || a.Descriptor().Caps.Has(CapCommand) {
		t.Fatal("capability bits wrong")
	}

	if _, err := r.Build("acme", "asc", "dev-2", nil); err == nil {
		t.Fatal("expected error for unknown adapter")
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	r := NewRegistry()
	d := Descriptor{Vendor: "ntcip", DeviceKind: "asc", Caps: CapState}
	f := func(string, map[string]any) (Adapter, error) { return nil, nil }
	r.Register(d, f)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register(d, f)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/adapter/` — Expected: FAIL.

- [ ] **Step 3: Implement**

`sdk/adapter/adapter.go`:
```go
// Package adapter defines the vendor×device-kind integration surface.
// Adapters own transport entirely; their only obligation is to return
// sdk/model types (ADR 0002, 0003). Everything is pull (ADR 0004).
package adapter

import (
	"context"

	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// Capability is a bitset of what an adapter can do.
type Capability uint8

const (
	// CapState: implements StateReader — poll returns a Snapshot the core
	// diffs into events.
	CapState Capability = 1 << iota
	// CapEvents: implements EventReader — poll returns discrete events
	// (log fetchers); no diffing.
	CapEvents
	// CapCommand: implements Commander. Reserved seam; nothing dispatches
	// commands in v1 (ADR 0004).
	CapCommand
)

// Has reports whether c includes capability q.
func (c Capability) Has(q Capability) bool { return c&q != 0 }

// Descriptor identifies an adapter and its capabilities.
type Descriptor struct {
	Vendor     string // e.g. "ntcip", "econolite", "qfree"
	DeviceKind string // e.g. "asc", "rsu", "dms"
	Caps       Capability
}

// Key is the registry key: "<vendor>-<device_kind>".
func (d Descriptor) Key() string { return d.Vendor + "-" + d.DeviceKind }

// Adapter is the common surface every vendor×device-kind unit implements.
type Adapter interface {
	Descriptor() Descriptor
	Close() error
}

// StateReader polls the device and returns a normalized state snapshot.
type StateReader interface {
	Adapter
	Read(ctx context.Context) (*model.Snapshot, error)
}

// EventReader polls a source that yields discrete events (e.g. controller
// hi-res logs). Still pull; split from StateReader by semantics.
type EventReader interface {
	Adapter
	Fetch(ctx context.Context) ([]model.Event, error)
}

// Commander writes commands to the device. Dormant in v1.
type Commander interface {
	Adapter
	Execute(ctx context.Context, cmd model.Command) error
}

// Factory builds an Adapter for one configured device. conn is the
// device's `connection` config block — opaque to the core, parsed here.
type Factory func(deviceID string, conn map[string]any) (Adapter, error)
```

`sdk/adapter/registry.go`:
```go
package adapter

import "fmt"

// Registry maps "<vendor>-<device_kind>" keys to adapter factories.
// Config validation uses Known as the trust boundary: a device whose
// vendor/kind has no registered adapter fails boot.
type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register panics on duplicate keys — duplicates are programmer error.
func (r *Registry) Register(d Descriptor, f Factory) {
	k := d.Key()
	if _, exists := r.factories[k]; exists {
		panic("adapter: duplicate registration for " + k)
	}
	r.factories[k] = f
}

// Known reports whether an adapter is registered for vendor+deviceKind.
func (r *Registry) Known(vendor, deviceKind string) bool {
	_, ok := r.factories[Descriptor{Vendor: vendor, DeviceKind: deviceKind}.Key()]
	return ok
}

// Build constructs the adapter for one configured device.
func (r *Registry) Build(vendor, deviceKind, deviceID string, conn map[string]any) (Adapter, error) {
	k := Descriptor{Vendor: vendor, DeviceKind: deviceKind}.Key()
	f, ok := r.factories[k]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for %q", k)
	}
	return f(deviceID, conn)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sdk/adapter/` — Expected: PASS.
Also run: `make check` — Expected: all green (boundary lint now has real `sdk/` packages to scan).

- [ ] **Step 5: Commit**

```bash
git add sdk/adapter
git commit -m "feat(sdk): adapter interfaces, capabilities, registry"
```

---

### Task 5: `sdk/transport/snmp` — minimal client + test fake

**Files:**
- Create: `sdk/transport/snmp/client.go`, `sdk/transport/snmp/snmptest/static.go`
- Test: `sdk/transport/snmp/snmptest/static_test.go`

**Interfaces:**
- Produces:
  - `type Client interface { Get(ctx context.Context, oids []string) (map[string]int64, error); Close() error }` — missing OIDs are omitted from the map (graceful degradation), matching gen-1 semantics.
  - `func Dial(cfg DialConfig) (Client, error)` with `type DialConfig struct { Address, Community string; Timeout time.Duration; Retries int }` — gosnmp-backed, **all I/O serialized with a mutex** (one UDP socket must never see concurrent use; gen-1 audit C3).
  - `snmptest.Static{Values map[string]int64; Err error}` implementing `Client` for fixtures.

- [ ] **Step 1: Write the failing test**

`sdk/transport/snmp/snmptest/static_test.go`:
```go
package snmptest

import (
	"context"
	"errors"
	"testing"
)

func TestStaticReturnsKnownOIDsOnly(t *testing.T) {
	s := &Static{Values: map[string]int64{".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3}}
	got, err := s.Get(context.Background(), []string{
		".1.3.6.1.4.1.1206.4.2.1.3.2.0",
		".1.3.6.1.4.1.1206.4.2.1.2.7.0", // not in fixture — must be absent, not zero
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v, ok := got[".1.3.6.1.4.1.1206.4.2.1.3.2.0"]; !ok || v != 3 {
		t.Fatalf("known OID = %v,%v; want 3,true", v, ok)
	}
	if _, ok := got[".1.3.6.1.4.1.1206.4.2.1.2.7.0"]; ok {
		t.Fatal("unknown OID must be omitted")
	}
}

func TestStaticErr(t *testing.T) {
	s := &Static{Err: errors.New("boom")}
	if _, err := s.Get(context.Background(), []string{".1"}); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sdk/transport/...` — Expected: FAIL.

- [ ] **Step 3: Implement**

`sdk/transport/snmp/client.go`:
```go
// Package snmp is an OPTIONAL transport helper adapters may use. The core
// never imports it — transport is entirely the adapter's business.
package snmp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Client fetches integer-valued OIDs. OIDs the agent does not answer are
// omitted from the result map — callers treat absence as a facet-level
// read failure, never as a zero value.
type Client interface {
	Get(ctx context.Context, oids []string) (map[string]int64, error)
	Close() error
}

// DialConfig configures a UDP SNMP v2c session.
type DialConfig struct {
	Address   string // "host:port"
	Community string
	Timeout   time.Duration // default 2s
	Retries   int           // default 1
}

// Dial opens a gosnmp session. All I/O on the returned Client is
// mutex-serialized: one gosnmp connection must never see concurrent use.
func Dial(cfg DialConfig) (Client, error) {
	host, port, err := splitHostPort(cfg.Address)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	g := &gosnmp.GoSNMP{
		Target:    host,
		Port:      port,
		Community: cfg.Community,
		Version:   gosnmp.Version2c,
		Timeout:   cfg.Timeout,
		Retries:   cfg.Retries,
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("snmp connect %s: %w", cfg.Address, err)
	}
	return &client{g: g}, nil
}

type client struct {
	mu sync.Mutex
	g  *gosnmp.GoSNMP
}

func (c *client) Get(ctx context.Context, oids []string) (map[string]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(oids))
	// gosnmp caps PDUs per request; chunk to stay portable across agents.
	const chunk = 16
	for i := 0; i < len(oids); i += chunk {
		end := min(i+chunk, len(oids))
		pkt, err := c.g.Get(oids[i:end])
		if err != nil {
			return nil, fmt.Errorf("snmp get: %w", err)
		}
		for _, pdu := range pkt.Variables {
			if v, ok := toInt64(pdu); ok {
				out[pdu.Name] = v
			}
		}
	}
	return out, nil
}

func (c *client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.g.Conn.Close()
}

func toInt64(pdu gosnmp.SnmpPDU) (int64, bool) {
	switch pdu.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Counter64, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Uinteger32:
		return gosnmp.ToBigInt(pdu.Value).Int64(), true
	default:
		return 0, false // NoSuchObject / NoSuchInstance / non-numeric: omit
	}
}

func splitHostPort(addr string) (string, uint16, error) {
	var host string
	var port int
	if _, err := fmt.Sscanf(addr, "%s", &host); err != nil || addr == "" {
		return "", 0, fmt.Errorf("snmp: empty address")
	}
	n, err := fmt.Sscanf(addr, "%[^:]:%d", &host, &port)
	if err != nil || n != 2 || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("snmp: address %q must be host:port", addr)
	}
	return host, uint16(port), nil
}
```

`sdk/transport/snmp/snmptest/static.go`:
```go
// Package snmptest provides the fixture-replay fake used by adapter golden
// tests (ADR 0008): recorded OID→value maps stand in for a live agent.
package snmptest

import "context"

// Static replays a fixed OID→value map. OIDs absent from Values are
// omitted from Get results, mirroring a live agent's NoSuchObject.
type Static struct {
	Values map[string]int64
	Err    error
}

func (s *Static) Get(_ context.Context, oids []string) (map[string]int64, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	out := make(map[string]int64)
	for _, oid := range oids {
		if v, ok := s.Values[oid]; ok {
			out[oid] = v
		}
	}
	return out, nil
}

func (s *Static) Close() error { return nil }
```

- [ ] **Step 4: Add gosnmp dependency, run tests**

```bash
go get github.com/gosnmp/gosnmp@v1.43.2
go mod tidy
```

Run: `go test ./sdk/transport/...` — Expected: PASS. Run `go vet ./...` — clean.

- [ ] **Step 5: Commit**

```bash
git add sdk/transport go.mod go.sum
git commit -m "feat(sdk): minimal serialized SNMP client + snmptest fixture fake"
```

---

### Task 6: `internal/vendors/ntcip` — the ntcip-asc adapter

**Files:**
- Create: `internal/vendors/ntcip/asc.go`, `internal/vendors/ntcip/register.go`
- Test: `internal/vendors/ntcip/asc_test.go`

**Interfaces:**
- Consumes: `model.*` (Tasks 2–3), `adapter.*` (Task 4), `snmp.Client`/`snmp.Dial`/`snmptest.Static` (Task 5).
- Produces:
  - `func NewASC(deviceID string, client snmp.Client) adapter.StateReader` (exported for tests/fixtures)
  - `func RegisterTo(r *adapter.Registry)` registering `ntcip-asc` with a factory that parses `conn["snmp"]` = `map[string]any{"address": string, "community": string}` and dials a real client.
- NTCIP 1202 OIDs (verbatim from the standard; same set gen-1 polled):
  - operation status `.1.3.6.1.4.1.1206.4.2.1.2.7.0` (values: 2=normal, 3=standby, 4=flash — others map to `ModeUnknown`)
  - flash status `.1.3.6.1.4.1.1206.4.2.1.2.5.0` (2 = conflict flash)
  - pattern status `.1.3.6.1.4.1.1206.4.2.1.3.2.0` (active plan id)
  - preempt status `.1.3.6.1.4.1.1206.4.2.1.6.5.0` (>0 = active; value = preempt number, stringified as `"preempt-<n>"`)

- [ ] **Step 1: Write the failing golden test**

`internal/vendors/ntcip/asc_test.go`:
```go
package ntcip

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Vikasa2M/openits-collector/sdk/model"
	"github.com/Vikasa2M/openits-collector/sdk/transport/snmp/snmptest"
)

// Fixture: healthy controller, plan 3, no preemption, no conflict flash.
var healthyFixture = map[string]int64{
	".1.3.6.1.4.1.1206.4.2.1.2.7.0": 2, // operation: normal
	".1.3.6.1.4.1.1206.4.2.1.2.5.0": 0, // flash: none
	".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3, // pattern: plan 3
	".1.3.6.1.4.1.1206.4.2.1.6.5.0": 0, // preempt: none
}

func TestASCReadGolden(t *testing.T) {
	a := NewASC("asc-1", &snmptest.Static{Values: healthyFixture})
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.DeviceID != "asc-1" || snap.SampledAt.IsZero() {
		t.Fatalf("bad header: %+v", snap)
	}
	f, ok := snap.Facet(model.KindSignalStatus)
	if !ok {
		t.Fatal("missing signal-status facet")
	}
	want := model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}
	if got := f.(model.SignalStatus); !reflect.DeepEqual(got, want) {
		t.Fatalf("SignalStatus = %+v, want %+v", got, want)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("unexpected facet errors: %+v", snap.Errors)
	}
}

func TestASCReadPreemptionAndFlash(t *testing.T) {
	fx := map[string]int64{
		".1.3.6.1.4.1.1206.4.2.1.2.7.0": 4, // flash mode
		".1.3.6.1.4.1.1206.4.2.1.2.5.0": 2, // conflict flash
		".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3,
		".1.3.6.1.4.1.1206.4.2.1.6.5.0": 2, // preempt 2 active
	}
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := snap.Facet(model.KindSignalStatus)
	want := model.SignalStatus{
		Mode: model.ModeFlash, InConflictFlash: true, ActivePlanID: 3,
		PreemptionActive: true, PreemptionSource: "preempt-2",
	}
	if !reflect.DeepEqual(got.(model.SignalStatus), want) {
		t.Fatalf("SignalStatus = %+v, want %+v", got, want)
	}
}

func TestASCReadPartialFailureIsFacetError(t *testing.T) {
	// Agent answered nothing for the mandatory operation-status OID:
	// the facet must be reported failed, NOT defaulted (absence ≠ state).
	fx := map[string]int64{".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3}
	snap, err := NewASC("asc-1", &snmptest.Static{Values: fx}).Read(context.Background())
	if err != nil {
		t.Fatalf("partial data must not be a hard error: %v", err)
	}
	if _, ok := snap.Facet(model.KindSignalStatus); ok {
		t.Fatal("incomplete facet must not be present")
	}
	if !snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("expected signal-status FacetError")
	}
}

func TestASCReadTransportErrorIsHardError(t *testing.T) {
	_, err := NewASC("asc-1", &snmptest.Static{Err: errors.New("timeout")}).Read(context.Background())
	if err == nil {
		t.Fatal("transport failure must be a hard Read error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vendors/...` — Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/vendors/ntcip/asc.go`:
```go
// Package ntcip implements the generic standards-only vendor: pure NTCIP
// with no vendor quirks. It is the compatibility target other ASC vendors
// start from (ADR 0003).
package ntcip

import (
	"context"
	"fmt"
	"time"

	"github.com/Vikasa2M/openits-collector/sdk/adapter"
	"github.com/Vikasa2M/openits-collector/sdk/model"
	"github.com/Vikasa2M/openits-collector/sdk/transport/snmp"
)

// NTCIP 1202 OIDs polled per cycle.
const (
	oidOperationStatus = ".1.3.6.1.4.1.1206.4.2.1.2.7.0"
	oidFlashStatus     = ".1.3.6.1.4.1.1206.4.2.1.2.5.0"
	oidPatternStatus   = ".1.3.6.1.4.1.1206.4.2.1.3.2.0"
	oidPreemptStatus   = ".1.3.6.1.4.1.1206.4.2.1.6.5.0"
)

var ascDescriptor = adapter.Descriptor{
	Vendor: "ntcip", DeviceKind: "asc", Caps: adapter.CapState,
}

type asc struct {
	deviceID string
	client   snmp.Client
	now      func() time.Time
}

// NewASC wraps an SNMP client as the ntcip-asc StateReader. Exported so
// fixture tests (and vendor adapters embedding the NTCIP base) can inject
// a client.
func NewASC(deviceID string, client snmp.Client) adapter.StateReader {
	return &asc{deviceID: deviceID, client: client, now: time.Now}
}

func (a *asc) Descriptor() adapter.Descriptor { return ascDescriptor }
func (a *asc) Close() error                   { return a.client.Close() }

func (a *asc) Read(ctx context.Context) (*model.Snapshot, error) {
	vals, err := a.client.Get(ctx, []string{
		oidOperationStatus, oidFlashStatus, oidPatternStatus, oidPreemptStatus,
	})
	if err != nil {
		return nil, fmt.Errorf("ntcip-asc %s: %w", a.deviceID, err)
	}
	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}

	op, ok := vals[oidOperationStatus]
	if !ok {
		// Mandatory OID unanswered: report the facet failed rather than
		// fabricating state (absence of evidence is never a state change).
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "operation-status OID unanswered",
		})
		return snap, nil
	}

	st := model.SignalStatus{
		Mode:            modeFromOperation(op),
		InConflictFlash: vals[oidFlashStatus] == 2,
		ActivePlanID:    uint32(vals[oidPatternStatus]),
	}
	if p := vals[oidPreemptStatus]; p > 0 {
		st.PreemptionActive = true
		st.PreemptionSource = fmt.Sprintf("preempt-%d", p)
	}
	snap.Facets = append(snap.Facets, st)
	return snap, nil
}

func modeFromOperation(v int64) model.ControllerMode {
	switch v {
	case 2:
		return model.ModeNormal
	case 3:
		return model.ModeStandby
	case 4:
		return model.ModeFlash
	default:
		return model.ModeUnknown
	}
}
```

`internal/vendors/ntcip/register.go`:
```go
package ntcip

import (
	"fmt"
	"time"

	"github.com/Vikasa2M/openits-collector/sdk/adapter"
	"github.com/Vikasa2M/openits-collector/sdk/transport/snmp"
)

// RegisterTo registers ntcip-asc. The connection block is
//
//	connection:
//	  snmp: { address: "host:161", community: "public" }
func RegisterTo(r *adapter.Registry) {
	r.Register(ascDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseSNMPBlock(conn)
		if err != nil {
			return nil, fmt.Errorf("ntcip-asc %s: %w", deviceID, err)
		}
		c, err := snmp.Dial(cfg)
		if err != nil {
			return nil, err
		}
		return NewASC(deviceID, c), nil
	})
}

func parseSNMPBlock(conn map[string]any) (snmp.DialConfig, error) {
	raw, ok := conn["snmp"].(map[string]any)
	if !ok {
		return snmp.DialConfig{}, fmt.Errorf("connection.snmp block required")
	}
	addr, _ := raw["address"].(string)
	if addr == "" {
		return snmp.DialConfig{}, fmt.Errorf("connection.snmp.address required")
	}
	community, _ := raw["community"].(string)
	if community == "" {
		community = "public"
	}
	return snmp.DialConfig{Address: addr, Community: community, Timeout: 2 * time.Second, Retries: 1}, nil
}
```

- [ ] **Step 4: Run tests + boundary lint**

Run: `go test ./internal/vendors/...` — Expected: PASS.
Run: `make check` — Expected: green (proves `internal/vendors` passes the boundary lint).

- [ ] **Step 5: Commit**

```bash
git add internal/vendors
git commit -m "feat(vendors): ntcip-asc StateReader with fixture goldens"
```

---

### Task 7: `internal/synth` — diff engine + SignalStatus differ

**Files:**
- Create: `internal/synth/synth.go`, `internal/synth/signal.go`
- Test: `internal/synth/signal_test.go`

**Interfaces:**
- Consumes: `model.*`.
- Produces:
  - `type Differ interface { Kind() model.Kind; Diff(prev model.Facet, curr model.Facet, base model.Base) []model.Event }` — `prev == nil` on first observation.
  - `type Engine` with `func NewEngine(differs ...Differ) *Engine` and `func (e *Engine) Apply(snap *model.Snapshot) []model.Event`. Engine keeps last-known facets per device; **a facet listed in `snap.Errors` keeps its previous value and produces no events** (suspension); a facet absent without error is treated the same as failed (unknown ≠ cleared). Engine is mutex-guarded.
  - `func NewSignalDiffer() Differ` — every poll emits `OperationalStatusReport`; when `prev != nil` also emits `ModeChanged`/`PlanChanged`/`PreemptionActivated`/`PreemptionCleared` on transitions. `base.OccurredAt` = `snap.SampledAt`.

- [ ] **Step 1: Write the failing test**

`internal/synth/signal_test.go`:
```go
package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/sdk/model"
)

var t0 = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func snapAt(at time.Time, st model.SignalStatus) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at, Facets: []model.Facet{st}}
}

func kinds(evs []model.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventKind()
	}
	return out
}

func TestFirstPollEmitsOnlyStatusReport(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	evs := e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 1 || got[0] != "operational-status-report" {
		t.Fatalf("first poll events = %v, want [operational-status-report]", got)
	}
	rep := evs[0].(model.OperationalStatusReport)
	if rep.ActivePlanID != 3 || rep.Mode != model.ModeNormal || !rep.OccurredAt.Equal(t0) {
		t.Fatalf("bad report: %+v", rep)
	}
}

func TestTransitionsEmitChangeEvents(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))

	t1 := t0.Add(time.Second)
	evs := e.Apply(snapAt(t1, model.SignalStatus{
		Mode: model.ModeFlash, ActivePlanID: 7,
		PreemptionActive: true, PreemptionSource: "preempt-2",
	}))
	got := map[string]bool{}
	for _, k := range kinds(evs) {
		got[k] = true
	}
	for _, want := range []string{"operational-status-report", "mode-changed", "plan-changed", "preemption-activated"} {
		if !got[want] {
			t.Fatalf("missing %q in %v", want, kinds(evs))
		}
	}

	t2 := t1.Add(time.Second)
	evs = e.Apply(snapAt(t2, model.SignalStatus{Mode: model.ModeFlash, ActivePlanID: 7}))
	found := false
	for _, ev := range evs {
		if ev.EventKind() == "preemption-cleared" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected preemption-cleared in %v", kinds(evs))
	}
}

func TestFailedFacetSuspendsDiffing(t *testing.T) {
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))

	// Poll 2: facet failed. No events at all for this facet.
	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindSignalStatus, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed facet must emit nothing, got %v", kinds(evs))
	}

	// Poll 3: recovered with same state. Must NOT re-emit transitions
	// against a zero value — prev survived the failed poll.
	evs := e.Apply(snapAt(t0.Add(2*time.Second), model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 1 || got[0] != "operational-status-report" {
		t.Fatalf("post-recovery events = %v, want [operational-status-report]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/synth/` — Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/synth/synth.go`:
```go
// Package synth turns consecutive state Snapshots into domain events.
// One registered Differ per facet kind; the engine never grows vendor or
// wire knowledge.
package synth

import (
	"sync"

	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// Differ diffs one facet kind. prev is nil on first observation.
type Differ interface {
	Kind() model.Kind
	Diff(prev, curr model.Facet, base model.Base) []model.Event
}

// Engine applies snapshots and remembers last-known facets per device.
type Engine struct {
	mu      sync.Mutex
	differs map[model.Kind]Differ
	// prev[deviceID][kind] = last successfully-read facet
	prev map[string]map[model.Kind]model.Facet
}

func NewEngine(differs ...Differ) *Engine {
	e := &Engine{
		differs: make(map[model.Kind]Differ),
		prev:    make(map[string]map[model.Kind]model.Facet),
	}
	for _, d := range differs {
		e.differs[d.Kind()] = d
	}
	return e
}

// Apply diffs snap against last-known state and returns domain events.
// Iron rule: a facet that failed (snap.Errors) or is simply absent emits
// nothing and keeps its previous value — absence of evidence is never a
// state change.
func (e *Engine) Apply(snap *model.Snapshot) []model.Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	dev := e.prev[snap.DeviceID]
	if dev == nil {
		dev = make(map[model.Kind]model.Facet)
		e.prev[snap.DeviceID] = dev
	}

	base := model.Base{DeviceID: snap.DeviceID, OccurredAt: snap.SampledAt}
	var events []model.Event
	for _, f := range snap.Facets {
		d, ok := e.differs[f.FacetKind()]
		if !ok {
			continue // facet kind with no differ registered: carried, not diffed
		}
		events = append(events, d.Diff(dev[f.FacetKind()], f, base)...)
		dev[f.FacetKind()] = f
	}
	return events
}
```

`internal/synth/signal.go`:
```go
package synth

import "github.com/Vikasa2M/openits-collector/sdk/model"

// NewSignalDiffer diffs the signal-status facet: a status report every
// poll, plus transition events when a previous value exists.
func NewSignalDiffer() Differ { return signalDiffer{} }

type signalDiffer struct{}

func (signalDiffer) Kind() model.Kind { return model.KindSignalStatus }

func (signalDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.SignalStatus)
	events := []model.Event{model.OperationalStatusReport{
		Base: base, Mode: c.Mode, InConflictFlash: c.InConflictFlash, ActivePlanID: c.ActivePlanID,
	}}
	if prev == nil {
		return events
	}
	p := prev.(model.SignalStatus)
	if p.Mode != c.Mode {
		events = append(events, model.ModeChanged{Base: base, From: p.Mode, To: c.Mode})
	}
	if p.ActivePlanID != c.ActivePlanID {
		events = append(events, model.PlanChanged{Base: base, FromPlanID: p.ActivePlanID, ToPlanID: c.ActivePlanID})
	}
	if !p.PreemptionActive && c.PreemptionActive {
		events = append(events, model.PreemptionActivated{Base: base, Source: c.PreemptionSource})
	}
	if p.PreemptionActive && !c.PreemptionActive {
		events = append(events, model.PreemptionCleared{Base: base})
	}
	return events
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/synth/` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/synth
git commit -m "feat(core): synth diff engine with per-facet differs; signal-status differ"
```

---

### Task 8: `internal/cloudevents` — envelope, content-addressed id, tenant-spliced subjects

**Files:**
- Create: `internal/cloudevents/envelope.go`, `internal/cloudevents/subject.go`
- Test: `internal/cloudevents/envelope_test.go`

**Interfaces:**
- Consumes: nothing project-internal.
- Produces:
  - `type Tenant struct { Agency, Site string }` with `func (t Tenant) Validate() error` (both tokens `^[a-z0-9][a-z0-9-]*$`)
  - `type Envelope struct { SpecVersion, ID, Source, Type string; Time time.Time; DataContentType string; Data json.RawMessage }` with JSON tags `specversion,id,source,type,time,datacontenttype,data`
  - `func New(ceType, source string, occurredAt time.Time, contentType string, data []byte) Envelope` — `ID` = full hex SHA-256 of `type\x00source\x00RFC3339Nano(occurredAt UTC)\x00data`
  - `func SubjectFor(t Tenant, ceType string) (string, error)` — splices tenant after the first dot-token: `openits.signal-control.fault-raised.v1` → `openits.<agency>.<site>.signal-control.fault-raised.v1`; `openits-collector.health.device-status-changed.v1` → `openits.<agency>.<site>.health.device-status-changed.v1`
  - `func SourceFor(t Tenant, deviceID string) string` — `//<agency>/<site>/<deviceID>`; empty deviceID (collector self) → `//<agency>/<site>`

- [ ] **Step 1: Write the failing test**

`internal/cloudevents/envelope_test.go`:
```go
package cloudevents

import (
	"testing"
	"time"
)

var tenant = Tenant{Agency: "metro-atlanta", Site: "cabinet-042"}

// Byte-literal subject goldens: the tenant-splice rule (ADR 0006).
func TestSubjectForGolden(t *testing.T) {
	cases := map[string]string{
		"openits.signal-control.fault-raised.v1":             "openits.metro-atlanta.cabinet-042.signal-control.fault-raised.v1",
		"openits.signal-control.operational-status-report.v1": "openits.metro-atlanta.cabinet-042.signal-control.operational-status-report.v1",
		"openits-collector.health.device-status-changed.v1":  "openits.metro-atlanta.cabinet-042.health.device-status-changed.v1",
		"openits-collector.health.collector-started.v1":      "openits.metro-atlanta.cabinet-042.health.collector-started.v1",
	}
	for ceType, want := range cases {
		got, err := SubjectFor(tenant, ceType)
		if err != nil {
			t.Fatalf("SubjectFor(%q): %v", ceType, err)
		}
		if got != want {
			t.Errorf("SubjectFor(%q) = %q, want %q", ceType, got, want)
		}
	}
}

func TestTenantValidate(t *testing.T) {
	for _, bad := range []Tenant{
		{Agency: "Metro", Site: "cab"}, {Agency: "metro", Site: "cab.1"},
		{Agency: "", Site: "cab"}, {Agency: "metro", Site: "cab*"},
	} {
		if bad.Validate() == nil {
			t.Errorf("Validate(%+v) should fail", bad)
		}
	}
	if tenant.Validate() != nil {
		t.Errorf("Validate(%+v) should pass", tenant)
	}
}

func TestContentAddressedID(t *testing.T) {
	at := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	a := New("openits-collector.health.collector-started.v1", "//metro/cab", at, "application/json", []byte(`{"v":1}`))
	b := New("openits-collector.health.collector-started.v1", "//metro/cab", at, "application/json", []byte(`{"v":1}`))
	c := New("openits-collector.health.collector-started.v1", "//metro/cab", at, "application/json", []byte(`{"v":2}`))
	if a.ID != b.ID {
		t.Error("identical inputs must produce identical IDs (JetStream dedup)")
	}
	if a.ID == c.ID {
		t.Error("different payloads must produce different IDs")
	}
	if len(a.ID) != 64 {
		t.Errorf("ID should be 64 hex chars, got %d", len(a.ID))
	}
	if a.SpecVersion != "1.0" {
		t.Errorf("specversion = %q", a.SpecVersion)
	}
}

func TestSourceFor(t *testing.T) {
	if got := SourceFor(tenant, "asc-1"); got != "//metro-atlanta/cabinet-042/asc-1" {
		t.Errorf("SourceFor = %q", got)
	}
	if got := SourceFor(tenant, ""); got != "//metro-atlanta/cabinet-042" {
		t.Errorf("SourceFor(collector) = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cloudevents/` — Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/cloudevents/envelope.go`:
```go
// Package cloudevents builds the CE envelopes the collector publishes.
// CE type = catalog ce-type verbatim; subject = tenant-spliced (ADR 0006);
// id = content-addressed so JetStream dedup survives restarts.
package cloudevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Envelope is a structured-mode CloudEvent (JSON).
type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

// New builds an envelope with a deterministic content-addressed ID.
func New(ceType, source string, occurredAt time.Time, contentType string, data []byte) Envelope {
	at := occurredAt.UTC()
	h := sha256.New()
	for _, part := range [][]byte{
		[]byte(ceType), []byte(source), []byte(at.Format(time.RFC3339Nano)), data,
	} {
		h.Write(part)
		h.Write([]byte{0})
	}
	return Envelope{
		SpecVersion:     "1.0",
		ID:              hex.EncodeToString(h.Sum(nil)),
		Source:          source,
		Type:            ceType,
		Time:            at,
		DataContentType: contentType,
		Data:            data,
	}
}
```

`internal/cloudevents/subject.go`:
```go
package cloudevents

import (
	"fmt"
	"regexp"
	"strings"
)

// Tenant identifies the cabinet: stamped into subjects and CE source.
type Tenant struct {
	Agency string
	Site   string
}

var tokenRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate rejects tenant tokens that could corrupt subject grammar
// (dots, wildcards, uppercase, empty).
func (t Tenant) Validate() error {
	if !tokenRe.MatchString(t.Agency) {
		return fmt.Errorf("invalid agency %q (need %s)", t.Agency, tokenRe)
	}
	if !tokenRe.MatchString(t.Site) {
		return fmt.Errorf("invalid site %q (need %s)", t.Site, tokenRe)
	}
	return nil
}

// SubjectFor splices the tenant after the ce-type's first dot-token:
//
//	openits.signal-control.fault-raised.v1
//	  → openits.<agency>.<site>.signal-control.fault-raised.v1
//	openits-collector.health.device-status-changed.v1
//	  → openits.<agency>.<site>.health.device-status-changed.v1
func SubjectFor(t Tenant, ceType string) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	_, rest, found := strings.Cut(ceType, ".")
	if !found || rest == "" {
		return "", fmt.Errorf("malformed ce-type %q", ceType)
	}
	return "openits." + t.Agency + "." + t.Site + "." + rest, nil
}

// SourceFor is the CE source URI-ref: //<agency>/<site>[/<device-id>].
// Empty deviceID means the collector itself.
func SourceFor(t Tenant, deviceID string) string {
	s := "//" + t.Agency + "/" + t.Site
	if deviceID != "" {
		s += "/" + deviceID
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cloudevents/` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloudevents
git commit -m "feat(core): CE envelope with content-addressed id and tenant-spliced subjects"
```

---

### Task 9: `internal/wire` — Emitter interface + health emitter

**Files:**
- Create: `internal/wire/emitter.go`, `internal/wire/health/health.go`
- Test: `internal/wire/health/health_test.go`

**Interfaces:**
- Consumes: `model.Event` and concrete health event types (Task 3).
- Produces:
  - `type Encoded struct { CEType string; ContentType string; Data []byte }`
  - `type Emitter interface { Encode(ev model.Event) (enc *Encoded, ok bool, err error) }` — `ok=false, err=nil` means "this emitter doesn't map this event" (callers try the next emitter or count a drop).
  - `func NewHealthEmitter() Emitter` mapping: `model.DeviceStatusChanged` → ce-type `openits-collector.health.device-status-changed.v1`; `model.CollectorStarted` → `openits-collector.health.collector-started.v1`. JSON bodies (keys sorted by Go's json marshaling of structs — field order fixed below).

- [ ] **Step 1: Write the failing golden test**

`internal/wire/health/health_test.go`:
```go
package health

import (
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/sdk/model"
)

var at = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func TestDeviceStatusChangedGolden(t *testing.T) {
	e := NewHealthEmitter()
	enc, ok, err := e.Encode(model.DeviceStatusChanged{
		Base: model.Base{DeviceID: "asc-1", OccurredAt: at},
		Reachable: false, Reason: "read timeout", ConsecutiveFailures: 3,
	})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.health.device-status-changed.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	if enc.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", enc.ContentType)
	}
	want := `{"device_id":"asc-1","occurred_at":"2026-07-12T10:00:00Z","reachable":false,"reason":"read timeout","consecutive_failures":3}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

func TestCollectorStartedGolden(t *testing.T) {
	enc, ok, err := NewHealthEmitter().Encode(model.CollectorStarted{
		Base: model.Base{OccurredAt: at}, Version: "dev",
	})
	if err != nil || !ok {
		t.Fatalf("Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits-collector.health.collector-started.v1" {
		t.Fatalf("CEType = %q", enc.CEType)
	}
	want := `{"occurred_at":"2026-07-12T10:00:00Z","version":"dev"}`
	if string(enc.Data) != want {
		t.Fatalf("Data = %s\nwant  %s", enc.Data, want)
	}
}

func TestUnmappedEventIsNotOK(t *testing.T) {
	_, ok, err := NewHealthEmitter().Encode(model.PlanChanged{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatal("health emitter must not claim domain events")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/wire/...` — Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/wire/emitter.go`:
```go
// Package wire is the ONLY layer allowed to know wire schemas. Emitters
// turn domain events into (payload, ce-type). One subpackage per pinned
// openits-models release (Plan 2+); package health is the collector-owned
// schema (ADR 0007).
package wire

import "github.com/Vikasa2M/openits-collector/sdk/model"

// Encoded is one wire-ready payload.
type Encoded struct {
	CEType      string
	ContentType string
	Data        []byte
}

// Emitter maps domain events to wire payloads. ok=false (with nil error)
// means "not mine": callers try the next emitter in their chain, and an
// event no emitter claims is dropped LOUDLY (metric + log), never silently.
type Emitter interface {
	Encode(ev model.Event) (enc *Encoded, ok bool, err error)
}
```

`internal/wire/health/health.go`:
```go
// Package health encodes health events in the collector-owned schema
// (ADR 0007): ce-types openits-collector.health.*.v1, JSON bodies.
package health

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/wire"
	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// NewHealthEmitter returns the emitter for collector-owned health events.
func NewHealthEmitter() wire.Emitter { return emitter{} }

type emitter struct{}

type deviceStatusBody struct {
	DeviceID            string    `json:"device_id"`
	OccurredAt          time.Time `json:"occurred_at"`
	Reachable           bool      `json:"reachable"`
	Reason              string    `json:"reason"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

type collectorStartedBody struct {
	OccurredAt time.Time `json:"occurred_at"`
	Version    string    `json:"version"`
}

func (emitter) Encode(ev model.Event) (*wire.Encoded, bool, error) {
	switch e := ev.(type) {
	case model.DeviceStatusChanged:
		return encodeJSON("openits-collector.health.device-status-changed.v1", deviceStatusBody{
			DeviceID: e.DeviceID, OccurredAt: e.OccurredAt.UTC(),
			Reachable: e.Reachable, Reason: e.Reason, ConsecutiveFailures: e.ConsecutiveFailures,
		})
	case model.CollectorStarted:
		return encodeJSON("openits-collector.health.collector-started.v1", collectorStartedBody{
			OccurredAt: e.OccurredAt.UTC(), Version: e.Version,
		})
	default:
		return nil, false, nil
	}
}

func encodeJSON(ceType string, body any) (*wire.Encoded, bool, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("health encode %s: %w", ceType, err)
	}
	return &wire.Encoded{CEType: ceType, ContentType: "application/json", Data: data}, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/wire/...` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/wire
git commit -m "feat(core): wire Emitter interface + collector-owned health emitter"
```

---

### Task 10: `internal/config` — load and fail-fast validation

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `adapter.Registry.Known` (Task 4), `cloudevents.Tenant.Validate` (Task 8).
- Produces:
  - `type Device struct { ID, Vendor, DeviceKind string; PollInterval time.Duration; Connection map[string]any }` (yaml: `id, vendor, device_kind, poll_interval, connection`; PollInterval default 5s, must be > 0 if set)
  - `type Config struct { Agency, Site, ModelVersion string; Devices []Device }` (yaml: `agency, site, model_version, devices`)
  - `func Load(path string, reg *adapter.Registry) (*Config, error)` — parses YAML then validates: tenant tokens valid; `model_version` non-empty; ≥1 device; device IDs non-empty and unique; every vendor/device_kind pair `reg.Known`.

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/sdk/adapter"
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

const validYAML = `
agency: metro-atlanta
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
agency: metro
site: cab-1
model_version: openits/v1
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
agency: metro
site: cab-1
model_version: openits/v1
devices: [{ id: d1, vendor: acme, device_kind: asc, connection: {} }]`,
		"bad agency token": `
agency: Metro.Atlanta
site: cab-1
model_version: openits/v1
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"missing model_version": `
agency: metro
site: cab-1
devices: [{ id: d1, vendor: ntcip, device_kind: asc, connection: {} }]`,
		"duplicate device id": `
agency: metro
site: cab-1
model_version: openits/v1
devices:
  - { id: d1, vendor: ntcip, device_kind: asc, connection: {} }
  - { id: d1, vendor: ntcip, device_kind: asc, connection: {} }`,
		"no devices": `
agency: metro
site: cab-1
model_version: openits/v1
devices: []`,
	}
	for name, yaml := range cases {
		if _, err := Load(write(t, yaml), reg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/` — Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/config/config.go`:
```go
// Package config loads the collector YAML and enforces the boot trust
// boundary: bad tenant tokens, unknown adapters, or malformed devices
// refuse to start (spec §6).
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Vikasa2M/openits-collector/internal/cloudevents"
	"github.com/Vikasa2M/openits-collector/sdk/adapter"
)

// Device is one polled device.
type Device struct {
	ID           string         `yaml:"id"`
	Vendor       string         `yaml:"vendor"`
	DeviceKind   string         `yaml:"device_kind"`
	PollInterval time.Duration  `yaml:"poll_interval"`
	Connection   map[string]any `yaml:"connection"`
}

// Config is the collector instance configuration.
type Config struct {
	Agency       string   `yaml:"agency"`
	Site         string   `yaml:"site"`
	ModelVersion string   `yaml:"model_version"`
	Devices      []Device `yaml:"devices"`
}

// Tenant returns the validated-shape tenant identity.
func (c *Config) Tenant() cloudevents.Tenant {
	return cloudevents.Tenant{Agency: c.Agency, Site: c.Site}
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
```

- [ ] **Step 4: Add yaml dependency, run tests**

```bash
go get gopkg.in/yaml.v3@v3.0.1
go mod tidy
```

Run: `go test ./internal/config/` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config go.mod go.sum
git commit -m "feat(core): config load with fail-fast boot validation"
```

---

### Task 11: `internal/runner` — per-device poll loop with health transitions

**Files:**
- Create: `internal/runner/runner.go`
- Test: `internal/runner/runner_test.go`

**Interfaces:**
- Consumes: `adapter.StateReader`, `synth.Engine`, `model.*`.
- Produces:
  - `type Runner struct` with `func New(dev adapter.StateReader, deviceID string, interval, timeout time.Duration, engine *synth.Engine, out func([]model.Event)) *Runner` and `func (r *Runner) Run(ctx context.Context)` (blocks until ctx cancel).
  - Behavior: initial jitter in `[0, interval)`; each poll bounded by `timeout` (default `interval` if timeout==0); each poll wrapped in panic-recover (a panicking adapter counts as a failed poll, never kills the loop); on read error → consecutive-failure count increments, and on the *first* failure emits `DeviceStatusChanged{Reachable:false}`; on success after ≥1 failure emits `DeviceStatusChanged{Reachable:true}`; successful snapshots go through `engine.Apply` and events (health + domain) flow to `out`.
  - Test hook: `r.SetNow(func() time.Time)` and `r.SetJitter(func(time.Duration) time.Duration)` exported setters for determinism.

- [ ] **Step 1: Write the failing test**

`internal/runner/runner_test.go`:
```go
package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/synth"
	"github.com/Vikasa2M/openits-collector/sdk/adapter"
	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// scriptedAdapter returns queued outcomes then repeats the last one.
type scriptedAdapter struct {
	mu      sync.Mutex
	script  []func() (*model.Snapshot, error)
	callN   int
}

func (s *scriptedAdapter) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "test", DeviceKind: "asc", Caps: adapter.CapState}
}
func (s *scriptedAdapter) Close() error { return nil }
func (s *scriptedAdapter) Read(context.Context) (*model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(s.callN, len(s.script)-1)
	s.callN++
	return s.script[i]()
}

func okSnap() (*model.Snapshot, error) {
	return &model.Snapshot{
		DeviceID: "asc-1", SampledAt: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}},
	}, nil
}
func failRead() (*model.Snapshot, error) { return nil, errors.New("timeout") }
func panicRead() (*model.Snapshot, error) { panic("adapter bug") }

func collect(t *testing.T, script []func() (*model.Snapshot, error), needAtLeast int) []model.Event {
	t.Helper()
	var mu sync.Mutex
	var events []model.Event
	done := make(chan struct{})
	out := func(evs []model.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evs...)
		if len(events) >= needAtLeast {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}
	r := New(&scriptedAdapter{script: script}, "asc-1", 5*time.Millisecond, 0,
		synth.NewEngine(synth.NewSignalDiffer()), out)
	r.SetJitter(func(time.Duration) time.Duration { return 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go r.Run(ctx)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("timed out; got %d events", len(events))
	}
	cancel()
	mu.Lock()
	defer mu.Unlock()
	return events
}

func TestHealthySequenceEmitsReports(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){okSnap}, 2)
	for _, ev := range events[:2] {
		if ev.EventKind() != "operational-status-report" {
			t.Fatalf("unexpected event %q", ev.EventKind())
		}
	}
}

func TestFailureThenRecoveryEmitsHealthTransitions(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){failRead, failRead, okSnap}, 3)
	// Expect: unreachable (poll 1), NO event for poll 2 (still down),
	// then reachable + status report (poll 3).
	var health []model.DeviceStatusChanged
	for _, ev := range events {
		if h, ok := ev.(model.DeviceStatusChanged); ok {
			health = append(health, h)
		}
	}
	if len(health) != 2 {
		t.Fatalf("health transitions = %d, want 2 (down, up): %+v", len(health), health)
	}
	if health[0].Reachable || health[0].ConsecutiveFailures != 1 {
		t.Fatalf("first transition should be unreachable/1: %+v", health[0])
	}
	if !health[1].Reachable {
		t.Fatalf("second transition should be reachable: %+v", health[1])
	}
}

func TestPanickingAdapterIsAFailedPollNotACrash(t *testing.T) {
	events := collect(t, []func() (*model.Snapshot, error){panicRead, okSnap}, 2)
	if len(events) < 2 {
		t.Fatal("runner must survive adapter panic and keep polling")
	}
	if h, ok := events[0].(model.DeviceStatusChanged); !ok || h.Reachable {
		t.Fatalf("first event should be unreachable health transition, got %+v", events[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runner/` — Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/runner/runner.go`:
```go
// Package runner drives one device: jittered poll loop, per-poll timeout,
// panic isolation, reachability health transitions. One sick device can
// never stall the cabinet (spec §7).
package runner

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/synth"
	"github.com/Vikasa2M/openits-collector/sdk/adapter"
	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// Runner polls one device on its own goroutine.
type Runner struct {
	dev      adapter.StateReader
	deviceID string
	interval time.Duration
	timeout  time.Duration
	engine   *synth.Engine
	out      func([]model.Event)

	now    func() time.Time
	jitter func(time.Duration) time.Duration

	consecutiveFailures int
}

// New builds a Runner. timeout==0 defaults to interval.
func New(dev adapter.StateReader, deviceID string, interval, timeout time.Duration,
	engine *synth.Engine, out func([]model.Event)) *Runner {
	if timeout <= 0 {
		timeout = interval
	}
	return &Runner{
		dev: dev, deviceID: deviceID, interval: interval, timeout: timeout,
		engine: engine, out: out,
		now:    time.Now,
		jitter: func(d time.Duration) time.Duration { return time.Duration(rand.Int63n(int64(d))) },
	}
}

// SetNow overrides the clock (tests).
func (r *Runner) SetNow(now func() time.Time) { r.now = now }

// SetJitter overrides start-jitter (tests).
func (r *Runner) SetJitter(j func(time.Duration) time.Duration) { r.jitter = j }

// Run blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	select {
	case <-time.After(r.jitter(r.interval)):
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.pollOnce(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) pollOnce(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	snap, err := r.readGuarded(pctx)
	switch {
	case err != nil:
		r.consecutiveFailures++
		if r.consecutiveFailures == 1 {
			r.out([]model.Event{model.DeviceStatusChanged{
				Base:      model.Base{DeviceID: r.deviceID, OccurredAt: r.now().UTC()},
				Reachable: false, Reason: err.Error(), ConsecutiveFailures: 1,
			}})
		}
	default:
		if r.consecutiveFailures > 0 {
			r.out([]model.Event{model.DeviceStatusChanged{
				Base:      model.Base{DeviceID: r.deviceID, OccurredAt: r.now().UTC()},
				Reachable: true, ConsecutiveFailures: 0,
			}})
			r.consecutiveFailures = 0
		}
		if evs := r.engine.Apply(snap); len(evs) > 0 {
			r.out(evs)
		}
	}
}

// readGuarded turns an adapter panic into a failed poll: adapters are
// third-party code and must never take the loop down.
func (r *Runner) readGuarded(ctx context.Context) (snap *model.Snapshot, err error) {
	defer func() {
		if p := recover(); p != nil {
			snap, err = nil, fmt.Errorf("adapter panic: %v", p)
		}
	}()
	return r.dev.Read(ctx)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runner/ -count=5` — Expected: PASS consistently (loop tests; run repeated to shake out flake).

- [ ] **Step 5: Commit**

```bash
git add internal/runner
git commit -m "feat(core): per-device runner with panic isolation and health transitions"
```

---

### Task 12: `internal/publish` — JetStream publisher

**Files:**
- Create: `internal/publish/publish.go`
- Test: `internal/publish/publish_test.go`

**Interfaces:**
- Consumes: `cloudevents.Envelope/Tenant/SubjectFor` (Task 8).
- Produces:
  - `func Connect(ctx context.Context, url string, t cloudevents.Tenant) (*Publisher, error)` — connects, creates-or-updates the stream: name `OPENITS-<AGENCY>-<SITE>` (tenant uppercased, `-` kept), subjects `[openits.<agency>.<site>.>]`, file storage, dedup window 2m.
  - `func (p *Publisher) Publish(ctx context.Context, env cloudevents.Envelope, ceType string) error` — subject via `SubjectFor`, JSON-marshals env, sets `Nats-Msg-Id` = `env.ID` (JetStream dedup), 3 attempts with 250ms backoff.
  - `func (p *Publisher) Close()`.
- New test-only dependency: `github.com/nats-io/nats-server/v2` (embedded server).

- [ ] **Step 1: Write the failing test**

`internal/publish/publish_test.go`:
```go
package publish

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/openits-collector/internal/cloudevents"
)

func startNATS(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats-server not ready")
	}
	return ns
}

func TestPublishRoundTripAndDedup(t *testing.T) {
	ns := startNATS(t)
	tenant := cloudevents.Tenant{Agency: "metro", Site: "cab-1"}
	ctx := context.Background()

	p, err := Connect(ctx, ns.ClientURL(), tenant)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer p.Close()

	at := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	ceType := "openits-collector.health.collector-started.v1"
	env := cloudevents.New(ceType, "//metro/cab-1", at, "application/json", []byte(`{"version":"dev"}`))

	// Publish the identical envelope twice: dedup must keep exactly one.
	if err := p.Publish(ctx, env, ceType); err != nil {
		t.Fatalf("Publish 1: %v", err)
	}
	if err := p.Publish(ctx, env, ceType); err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	stream, err := js.Stream(ctx, "OPENITS-METRO-CAB-1")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stream msgs = %d, want 1 (dedup)", info.State.Msgs)
	}

	// Read the message back and verify subject + envelope round-trip.
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "t"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(2 * time.Second))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	wantSubject := "openits.metro.cab-1.health.collector-started.v1"
	if msg.Subject() != wantSubject {
		t.Fatalf("subject = %q, want %q", msg.Subject(), wantSubject)
	}
	var got cloudevents.Envelope
	if err := json.Unmarshal(msg.Data(), &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if got.ID != env.ID || got.Type != ceType {
		t.Fatalf("envelope round-trip mismatch: %+v", got)
	}
}
```

- [ ] **Step 2: Add deps, run test to verify it fails**

```bash
go get github.com/nats-io/nats.go@v1.52.0
go get github.com/nats-io/nats-server/v2@latest
go mod tidy
```

Run: `go test ./internal/publish/` — Expected: FAIL (Connect undefined).

- [ ] **Step 3: Implement**

`internal/publish/publish.go`:
```go
// Package publish writes CloudEvents to the cabinet-local JetStream.
// Local JetStream IS the durability story (WAN-down is invisible here);
// publish is must-succeed with bounded retry, never unbounded buffering.
package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/openits-collector/internal/cloudevents"
)

const (
	publishAttempts = 3
	publishBackoff  = 250 * time.Millisecond
	dedupWindow     = 2 * time.Minute
)

// Publisher owns the NATS connection and the cabinet stream.
type Publisher struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	tenant cloudevents.Tenant
}

// Connect dials NATS and ensures the cabinet stream exists:
// name OPENITS-<AGENCY>-<SITE>, capturing openits.<agency>.<site>.>.
func Connect(ctx context.Context, url string, t cloudevents.Tenant) (*Publisher, error) {
	if err := t.Validate(); err != nil {
		return nil, err
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
		Name:       StreamName(t),
		Subjects:   []string{"openits." + t.Agency + "." + t.Site + ".>"},
		Storage:    jetstream.FileStorage,
		Duplicates: dedupWindow,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure stream: %w", err)
	}
	return &Publisher{nc: nc, js: js, tenant: t}, nil
}

// StreamName is OPENITS-<AGENCY>-<SITE> (subject-safe tokens uppercased).
func StreamName(t cloudevents.Tenant) string {
	return "OPENITS-" + strings.ToUpper(t.Agency) + "-" + strings.ToUpper(t.Site)
}

// Publish writes one envelope with Nats-Msg-Id = envelope ID for dedup.
func (p *Publisher) Publish(ctx context.Context, env cloudevents.Envelope, ceType string) error {
	subject, err := cloudevents.SubjectFor(p.tenant, ceType)
	if err != nil {
		return err
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	msg := &nats.Msg{Subject: subject, Data: data}
	msg.Header = nats.Header{}
	msg.Header.Set("Nats-Msg-Id", env.ID)

	var lastErr error
	for attempt := 0; attempt < publishAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(publishBackoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if _, lastErr = p.js.PublishMsg(ctx, msg); lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("publish %s after %d attempts: %w", subject, publishAttempts, lastErr)
}

// Close drains the connection.
func (p *Publisher) Close() { p.nc.Close() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/publish/` — Expected: PASS.
Run: `make check` — Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/publish go.mod go.sum
git commit -m "feat(core): JetStream publisher with stream provisioning and CE-id dedup"
```

---

### Task 13: `internal/app` + `cmd/collector` — wiring and end-to-end

**Files:**
- Create: `internal/app/app.go`, `cmd/collector/main.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `func Run(ctx context.Context, cfg *config.Config, reg *adapter.Registry, natsURL, version string) error` — builds adapters via registry, synth engine (`NewSignalDiffer()`), emitter chain (`[]wire.Emitter{health.NewHealthEmitter()}` — Plan 2 prepends the openitsv1 emitter), publisher; publishes `CollectorStarted`; starts one runner per device; event sink encodes → envelopes → publishes; **events no emitter claims are logged (`log/slog`) and counted, never silent**; blocks until ctx cancel; closes adapters and publisher on the way out.
  - `cmd/collector/main.go` — flags `-config` (path, required) and `-nats` (url, default `nats://127.0.0.1:4222`); registers `ntcip.RegisterTo`; runs `app.Run` under signal-cancelled context.

- [ ] **Step 1: Write the failing end-to-end test**

`internal/app/app_test.go`:
```go
package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/openits-collector/internal/cloudevents"
	"github.com/Vikasa2M/openits-collector/internal/config"
	"github.com/Vikasa2M/openits-collector/sdk/adapter"
	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// flakyASC fails its first read then succeeds — exercising both health
// transitions and the domain path in one e2e run.
type flakyASC struct {
	mu    sync.Mutex
	calls int
}

func (f *flakyASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}
func (f *flakyASC) Close() error { return nil }
func (f *flakyASC) Read(context.Context) (*model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return nil, errors.New("simulated timeout")
	}
	return &model.Snapshot{
		DeviceID: "asc-1", SampledAt: time.Now().UTC(),
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal, ActivePlanID: 3}},
	}, nil
}

func TestEndToEndHealthEventsReachJetStream(t *testing.T) {
	// Embedded NATS.
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	// Registry with the fixture vendor.
	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(deviceID string, conn map[string]any) (adapter.Adapter, error) { return &flakyASC{}, nil })

	// Config file.
	cfgYAML := `
agency: metro
site: cab-1
model_version: openits/v1
devices:
  - { id: asc-1, vendor: fixture, device_kind: asc, poll_interval: 20ms, connection: {} }
`
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe BEFORE starting the app (core NATS sub sees JetStream traffic).
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	seen := make(chan string, 64)
	sub, err := nc.Subscribe("openits.metro.cab-1.>", func(m *nats.Msg) {
		var env cloudevents.Envelope
		if json.Unmarshal(m.Data, &env) == nil {
			seen <- m.Subject + "|" + env.Type
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = Run(ctx, cfg, reg, ns.ClientURL(), "test") }()

	want := map[string]bool{
		"openits-collector.health.collector-started.v1":      false,
		"openits-collector.health.device-status-changed.v1": false,
	}
	deadline := time.After(8 * time.Second)
	for {
		allSeen := true
		for _, ok := range want {
			allSeen = allSeen && ok
		}
		if allSeen {
			break
		}
		select {
		case s := <-seen:
			parts := strings.SplitN(s, "|", 2)
			if !strings.HasPrefix(parts[0], "openits.metro.cab-1.") {
				t.Fatalf("event on unexpected subject %q", parts[0])
			}
			if _, tracked := want[parts[1]]; tracked {
				want[parts[1]] = true
			}
		case <-deadline:
			t.Fatalf("timed out; seen so far: %+v", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/` — Expected: FAIL (Run undefined).

- [ ] **Step 3: Implement**

`internal/app/app.go`:
```go
// Package app wires config → adapters → runners → synth → emitters →
// publisher: the collector spine.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Vikasa2M/openits-collector/internal/cloudevents"
	"github.com/Vikasa2M/openits-collector/internal/config"
	"github.com/Vikasa2M/openits-collector/internal/publish"
	"github.com/Vikasa2M/openits-collector/internal/runner"
	"github.com/Vikasa2M/openits-collector/internal/synth"
	"github.com/Vikasa2M/openits-collector/internal/wire"
	"github.com/Vikasa2M/openits-collector/internal/wire/health"
	"github.com/Vikasa2M/openits-collector/sdk/adapter"
	"github.com/Vikasa2M/openits-collector/sdk/model"
)

// Run starts the collector and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config, reg *adapter.Registry, natsURL, version string) error {
	tenant := cfg.Tenant()

	pub, err := publish.Connect(ctx, natsURL, tenant)
	if err != nil {
		return err
	}
	defer pub.Close()

	// Emitter chain: first claim wins. Plan 2 prepends the openits-models
	// emitter selected by cfg.ModelVersion; today only health is wired, so
	// domain events fall through to the loud-drop path below.
	emitters := []wire.Emitter{health.NewHealthEmitter()}

	sink := func(events []model.Event) {
		for _, ev := range events {
			encodeAndPublish(ctx, pub, tenant, emitters, ev)
		}
	}

	// Boot event.
	sink([]model.Event{model.CollectorStarted{
		Base: model.Base{OccurredAt: time.Now().UTC()}, Version: version,
	}})

	// Build adapters, then runners.
	engine := synth.NewEngine(synth.NewSignalDiffer())
	var wg sync.WaitGroup
	var adapters []adapter.Adapter
	for _, d := range cfg.Devices {
		a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
		if err != nil {
			return fmt.Errorf("device %s: %w", d.ID, err)
		}
		adapters = append(adapters, a)
		sr, ok := a.(adapter.StateReader)
		if !ok {
			return fmt.Errorf("device %s: adapter %s lacks CapState", d.ID, a.Descriptor().Key())
		}
		r := runner.New(sr, d.ID, d.PollInterval, 0, engine, sink)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(ctx)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	for _, a := range adapters {
		if err := a.Close(); err != nil {
			slog.Warn("adapter close", "adapter", a.Descriptor().Key(), "err", err)
		}
	}
	return nil
}

func encodeAndPublish(ctx context.Context, pub *publish.Publisher, tenant cloudevents.Tenant,
	emitters []wire.Emitter, ev model.Event) {
	for _, em := range emitters {
		enc, ok, err := em.Encode(ev)
		if err != nil {
			slog.Error("emit failed", "event", ev.EventKind(), "err", err)
			return
		}
		if !ok {
			continue
		}
		env := cloudevents.New(enc.CEType, cloudevents.SourceFor(tenant, ev.EventDeviceID()),
			ev.EventOccurredAt(), enc.ContentType, enc.Data)
		if err := pub.Publish(ctx, env, enc.CEType); err != nil {
			slog.Error("publish failed", "type", enc.CEType, "err", err)
		}
		return
	}
	// Loud drop: no emitter claims this event (spec §7). With only the
	// health emitter wired (Plan 1), every domain event lands here.
	slog.Warn("event dropped: no emitter for domain event",
		"event", ev.EventKind(), "device", ev.EventDeviceID())
}
```

`cmd/collector/main.go`:
```go
// Command collector is the OpenITS cabinet edge collector: polls local
// devices via registered vendor adapters and publishes CloudEvents to the
// cabinet-local NATS JetStream.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Vikasa2M/openits-collector/internal/app"
	"github.com/Vikasa2M/openits-collector/internal/config"
	"github.com/Vikasa2M/openits-collector/internal/vendors/ntcip"
	"github.com/Vikasa2M/openits-collector/sdk/adapter"
)

var version = "dev" // set via -ldflags "-X main.version=..."

func main() {
	cfgPath := flag.String("config", "", "path to collector.yaml (required)")
	natsURL := flag.String("nats", "nats://127.0.0.1:4222", "local NATS URL")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "-config is required")
		os.Exit(2)
	}

	reg := adapter.NewRegistry()
	ntcip.RegisterTo(reg)

	cfg, err := config.Load(*cfgPath, reg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, reg, *natsURL, version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run e2e + full check**

Run: `go test ./internal/app/ -count=2` — Expected: PASS.
Run: `make check` — Expected: all packages green, boundary lint green.
Run: `go build ./... && go run ./cmd/collector -version` — Expected: prints `dev`.

- [ ] **Step 5: Commit**

```bash
git add internal/app cmd
git commit -m "feat: collector spine — app wiring, cmd/collector, end-to-end test"
```

---

### Task 14: README rewrite + plan wrap-up

**Files:**
- Modify: `README.md` (full rewrite)

**Interfaces:** none (docs).

- [ ] **Step 1: Rewrite README.md**

```markdown
# openits-collector

Open-source edge collector for ITS cabinets: polls local devices (signal
controllers, RSUs, …) through vendor adapters, normalizes readings into a
collector-owned domain model, and publishes CloudEvents to the cabinet's
local NATS JetStream using versioned openits-models payloads.

## Architecture in one diagram

```
[device] ─transport─▶ ADAPTER ─sdk/model─▶ CORE ─wire emitter─▶ CloudEvent ─▶ local JetStream
```

- **Adapters** (`internal/vendors/<vendor>/<kind>/`) own transport
  entirely and return only `sdk/model` types.
- **The core** diffs snapshots into domain events, tracks device health,
  and publishes on tenant-scoped subjects
  (`openits.<agency>.<site>.<service>.<event>.v1`).
- **Wire emitters** (`internal/wire/`) are the only code that knows
  openits-models. One package per pinned models release.

Why it's built this way: see `docs/adr/`. Full design:
`docs/specs/2026-07-12-greenfield-collector-architecture-design.md`.

## Status

Gen-2 rebuild in progress. Working today: `ntcip-asc` adapter (fixtures +
live SNMP), signal-status synth, collector-owned health events end-to-end
to JetStream. Not yet wired: openits-models emitter (Plan 2), additional
facets and vendors (Plan 3+).

## Run

```bash
make check                             # vet + tests + boundary lint
go run ./cmd/collector -config collector.yaml
```

## Contributing an adapter

Implement `sdk/adapter.StateReader` (or `EventReader`) returning
`sdk/model` types, register a `Descriptor{Vendor, DeviceKind}`, and ship
recorded fixtures with golden tests — **no fixtures, no merge** (ADR 0008).
Adapters must not import openits-models (CI-enforced, ADR 0002).
```

- [ ] **Step 2: Final verification**

Run: `make check` — Expected: green.
Run: `git log --oneline main..gen2` — Expected: the commits from Tasks 0–14.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README for gen2 architecture"
```

---

## Follow-on plans (not in this plan)

- **Plan 2 — `wire/openitsv1`:** PREREQUISITE: tag a release in openits-models (user action; repo currently has no tags) and push it fetchable, then `go get github.com/Vikasa2M/openits-models@<tag>`. Emitter mapping domain events → proto payloads + catalog ce-types; catalog-conformance test against that release's `asyncapi.yaml`; `model_version` selects the emitter in `app.Run`'s chain.
- **Plan 3 — remaining facets:** `FaultSet`, `DetectorSamples`, `RSUBroadcastCounters` + differs + `ntcip-rsu` adapter.
- **Plan 4+ — real vendors:** econolite, qfree (adapter + fixtures each); EventReader path with the first log-shaped source; observability (Prometheus metrics, OTel) as its own slice.
