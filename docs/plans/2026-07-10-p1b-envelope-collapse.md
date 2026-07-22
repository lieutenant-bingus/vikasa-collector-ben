# P1b — Collapse Envelope Constructors into `emit.New` Implementation Plan


**Goal:** Replace the ~75 per-event `NewXEnvelope` constructors and 8 per-service wrapper functions in `internal/cloudevents/envelope.go` with one generic `New(ceType, …)` constructor, and route the publisher + heartbeat through it — eliminating the copy-paste surface and the hand-typed-`eventName` drift class, with byte-identical output.

**Architecture:** `New` decomposes the ce-type via the existing `parseCEType` (service token + event name), looks the internal `serviceSpec` up in a token→spec map, and delegates to the existing `newEnvelope`. A pre-analysis already proved every current constructor's hand-typed `eventName` equals the `parseCEType`-derived one (0/67 mismatches), so `New(TypeX, …)` is equivalent to `NewXEnvelope(…)` by construction. A subject/ce-id golden (Task 1) and a catalog self-consistency test (Task 2) guard the change; the compiler guards the deletions. This is P1b of the vendor-adapter design (`docs/specs/2026-07-10-vendor-adapter-architecture-design.md`).

**Tech Stack:** Go 1.26, `github.com/nats-io/nats.go` + `.../jetstream` (fake JSPublisher), stdlib `testing`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- **Behavior-preserving:** the ce-type, ce-source URN, NATS subject, ce-dataschema URL, and content-addressed ce-id for every event MUST be byte-identical before and after. The P1a + Task-1 goldens are the guard; if any golden changes, STOP.
- `parseCEType` (`internal/cloudevents/registry.go:139`) decomposes `openits.<service>.<event>.v<N>` → `{Service, EventName}`. It is the single source of the event-name; do not re-derive it elsewhere.
- The 8 per-service wrappers (`NewSignalControlEventEnvelope`, `NewDMSEventEnvelope`, `NewESSEventEnvelope`, `NewRSUEventEnvelope`, `NewRampMeteringEventEnvelope`, `NewGatewayEventEnvelope`, `NewTrafficSensorEventEnvelope`, `NewPerceptionEventEnvelope`) have **no callers outside `envelope.go`** (verified) — safe to delete once `New` replaces them.
- Keep `newEnvelope`'s existing behavior verbatim (panic on invalid controllerID, `OccurredAt`→`Time` binding, `AssignContentID(nil)`). `New` is a thin front door to it.
- Commit after each task; `gofmt -s -w .` before committing.

---

### Task 1: Subject/ce-id golden for the publisher path

Pins the externally-critical, deterministic publisher output (NATS subject + ce-id header) for a representative event set, so Tasks 3–4 can prove the collapse changed nothing end-to-end.

**Files:**
- Test: `internal/events/publisher_test.go` (create)

**Interfaces:**
- Consumes: `events.NewPublisher(client inats.NATSClient, cfg events.Config) (*Publisher, error)`; `events.Config{Region, Agency, AgencyUnit, Registry *registry.Registry, JetStream events.JSPublisher, Logger}`; `events.JSPublisher` = `PublishMsg(ctx, *nats.Msg, ...jetstream.PublishOpt) (*jetstream.PubAck, error)`; `cloudevents.WithOccurredAt(t)`.
- `registry.Registry` is a struct with exported fields: `Agencies map[string]registry.Agency`; `registry.Agency{Region, UnitLabel string, Units []string}`.

- [ ] **Step 1: Write the golden test.** Create `internal/events/publisher_test.go`:

```go
package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/openits-collector/internal/cloudevents"
	"github.com/Vikasa2M/openits-collector/internal/registry"
	openitspb "github.com/Vikasa2M/openits-models/pkg/proto/openits/v1"
)

// captureJS records the subject + ce-id of every published message.
type captureJS struct {
	mu   sync.Mutex
	subj []string
	ceid []string
}

func (c *captureJS) PublishMsg(_ context.Context, msg *nats.Msg, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subj = append(c.subj, msg.Subject)
	c.ceid = append(c.ceid, msg.Header.Get("ce-id"))
	return &jetstream.PubAck{Stream: "TEST", Sequence: 1}, nil
}

func testPublisher(t *testing.T, js JSPublisher) *Publisher {
	t.Helper()
	reg := &registry.Registry{
		Agencies: map[string]registry.Agency{
			"txdot": {Region: "us-tx", UnitLabel: "district", Units: []string{"d07"}},
		},
	}
	p, err := NewPublisher(nil, Config{
		Region: "us-tx", Agency: "txdot", AgencyUnit: "d07",
		Registry: reg, JetStream: js,
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	return p
}

// Subjects are deterministic and externally load-bearing; pin the exact strings.
func TestPublisher_SubjectsGolden(t *testing.T) {
	js := &captureJS{}
	p := testPublisher(t, js)
	ctx := context.Background()
	occ := cloudevents.WithOccurredAt(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))

	if err := p.PublishOperationalStatus(ctx, "asc-001", &openitspb.OperationalStatus{}, occ); err != nil {
		t.Fatalf("PublishOperationalStatus: %v", err)
	}
	if err := p.PublishDetectorReport(ctx, "asc-001", &openitspb.DetectorReport{}, occ); err != nil {
		t.Fatalf("PublishDetectorReport: %v", err)
	}
	if err := p.PublishRsuBroadcastSample(ctx, "rsu-001", &openitspb.RsuBroadcastSample{}, occ); err != nil {
		t.Fatalf("PublishRsuBroadcastSample: %v", err)
	}

	want := []string{
		"openits.us-tx.txdot.d07.signal-control.asc-001.operational-status",
		"openits.us-tx.txdot.d07.signal-control.asc-001.detector-report",
		"openits.us-tx.txdot.d07.rsu.rsu-001.rsu-broadcast-sample",
	}
	if len(js.subj) != len(want) {
		t.Fatalf("subjects = %v, want %v", js.subj, want)
	}
	for i := range want {
		if js.subj[i] != want[i] {
			t.Errorf("subject[%d] = %q, want %q", i, js.subj[i], want[i])
		}
		if js.ceid[i] == "" {
			t.Errorf("ce-id[%d] is empty", i)
		}
	}
}

// ce-id is a pure function of (source, type, stable-time, payload): the same
// logical event published twice yields the same id. Guards the content-address.
func TestPublisher_CeIDDeterministic(t *testing.T) {
	occ := cloudevents.WithOccurredAt(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	ids := make([]string, 2)
	for i := range ids {
		js := &captureJS{}
		p := testPublisher(t, js)
		if err := p.PublishOperationalStatus(context.Background(), "asc-001", &openitspb.OperationalStatus{Mode: openitspb.OperationalStatus_MODE_NORMAL}, occ); err != nil {
			t.Fatalf("publish: %v", err)
		}
		ids[i] = js.ceid[0]
	}
	if ids[0] != ids[1] || ids[0] == "" {
		t.Errorf("ce-id not deterministic: %q vs %q", ids[0], ids[1])
	}
}
```

- [ ] **Step 2: Run to verify it passes against current code.**

Run: `go test ./internal/events/... -v`
Expected: PASS. If a subject string differs, the canned `want` is wrong — correct it to the actual current output and note it (do not change production code in this task).

- [ ] **Step 3: Commit.**

```bash
git add internal/events/publisher_test.go
git commit -m "test(events): golden subjects + ce-id determinism for publisher (P1b guard)"
```

---

### Task 2: Add generic `New` + `specByToken` + catalog self-consistency test

**Files:**
- Modify: `internal/cloudevents/envelope.go` (add `specByToken` + `New`, near `newEnvelope` ~line 404)
- Test: `internal/cloudevents/new_test.go` (create)

**Interfaces:**
- Produces: `cloudevents.New(ceType, region, agency, agencyUnit, id string, opts ...EnvelopeOption) *Envelope` — the single generic constructor. Panics on an unparseable ce-type or unknown service token (programmer error, mirroring `newEnvelope`'s ID panic).

- [ ] **Step 1: Add `specByToken` and `New`** in `internal/cloudevents/envelope.go`, immediately after `newEnvelope`:

```go
// specByToken maps a service token to its internal serviceSpec. It is the
// lookup behind the generic New constructor. Built from the same svc* vars the
// per-service wrappers used.
var specByToken = map[string]serviceSpec{
	svcSignalControl.token: svcSignalControl,
	svcDMS.token:           svcDMS,
	svcESS.token:           svcESS,
	svcRSU.token:           svcRSU,
	svcRampMetering.token:  svcRampMetering,
	svcGateway.token:       svcGateway,
	svcTrafficSensor.token: svcTrafficSensor,
	svcPerception.token:    svcPerception,
}

// New constructs an envelope for any ceType. The service token and event name
// are decoded from ceType via parseCEType, so there is no hand-typed eventName
// to drift out of sync with the ce-type. ceType must be one of the Type*
// constants; an unparseable ce-type or unknown service token panics (a
// programmer error, like newEnvelope's ID validation).
func New(ceType, region, agency, agencyUnit, id string, opts ...EnvelopeOption) *Envelope {
	d, ok := parseCEType(ceType)
	if !ok {
		panic(fmt.Sprintf("cloudevents: unparseable ce-type %q", ceType))
	}
	spec, ok := specByToken[d.Service]
	if !ok {
		panic(fmt.Sprintf("cloudevents: unknown service token %q in ce-type %q", d.Service, ceType))
	}
	return newEnvelope(spec, ceType, region, agency, agencyUnit, id, d.EventName, opts...)
}
```

- [ ] **Step 2: Write the catalog self-consistency test.** Create `internal/cloudevents/new_test.go`:

```go
package cloudevents

import "testing"

// New must produce, for every ce-type in the catalog, an envelope whose
// service token, event name, and subject tail match the ce-type — proving the
// generic path is self-consistent with parseCEType across all events.
func TestNew_MatchesCatalogForEveryEvent(t *testing.T) {
	const region, agency, unit, id = "us-tx", "txdot", "d07", "dev-001"
	for _, d := range AllEvents() {
		env := New(d.Type, region, agency, unit, id)
		if env.Type != d.Type {
			t.Errorf("%s: Type = %q, want %q", d.Type, env.Type, d.Type)
		}
		if env.EventName != d.EventName {
			t.Errorf("%s: EventName = %q, want %q", d.Type, env.EventName, d.EventName)
		}
		if env.Service != d.Service {
			t.Errorf("%s: Service = %q, want %q", d.Type, env.Service, d.Service)
		}
		wantSubjectTail := d.Service + "." + id + "." + d.EventName
		if got := env.Subject; len(got) < len(wantSubjectTail) || got[len(got)-len(wantSubjectTail):] != wantSubjectTail {
			t.Errorf("%s: Subject = %q, want tail %q", d.Type, got, wantSubjectTail)
		}
		if env.ID == "" {
			t.Errorf("%s: empty ce-id", d.Type)
		}
	}
}
```

- [ ] **Step 3: Run tests.**

Run: `gofmt -s -w . && go test ./internal/cloudevents/... -v`
Expected: PASS for the whole catalog.

- [ ] **Step 4: Commit.**

```bash
git add internal/cloudevents/envelope.go internal/cloudevents/new_test.go
git commit -m "feat(cloudevents): add generic New constructor + catalog self-consistency test"
```

---

### Task 3: Route the publisher + heartbeat through `New` (ceType instead of constructor funcs)

Switch the two callers of the per-event constructors to pass a ce-type string and build via `New`. Leave the old constructors in place for now (Task 4 deletes them); this task keeps the tree compiling and the goldens green.

**Files:**
- Modify: `internal/events/publisher.go` (the `envelopeConstructor` type, `publish` signature, ~40 typed method call sites, the `faultRoute` struct + `faultRoutes`/`rsuFaultKinds` table, `PublishSynth`)
- Modify: `internal/heartbeat/heartbeat.go` (`publishHeartbeat`'s `NewGatewayHeartbeatEnvelope` call and `publishGatewayEvent`'s `makeEnvelope` param)

**Interfaces:**
- Consumes: `cloudevents.New` (Task 2) and the `cloudevents.Type*` constants.

- [ ] **Step 1: Change `publish` to take a ce-type string.** In `internal/events/publisher.go`, replace the `envelopeConstructor` type and the `publish` method's envelope construction. The current `publish` builds `env := makeEnvelope(p.region, p.agency, p.agencyUnit, controllerID, opts...)`. Change the signature `makeEnvelope envelopeConstructor` → `ceType string`, and that line to:

```go
	env := cloudevents.New(ceType, p.region, p.agency, p.agencyUnit, controllerID, opts...)
```

Delete the `envelopeConstructor` type declaration. Everything else in `publish` (marshal, `AssignContentID(data)`, traceparent opt, JS publish, metrics) stays byte-for-byte.

- [ ] **Step 2: Update every typed `Publish*` method call site.** Each method body is `return p.publish(ctx, id, cloudevents.NewXEnvelope, payload, opts...)`. Replace `cloudevents.NewXEnvelope` with the `Type*` constant that `NewXEnvelope` passes internally — the pairing is regular (`NewOperationalStatusEnvelope`→`cloudevents.TypeOperationalStatus`, `NewRsuSrmReceivedEnvelope`→`cloudevents.TypeRsuSrmReceived`, `NewLegacyEssFaultRaisedEnvelope`→`cloudevents.TypeLegacyEssFaultRaised`, etc.). For any constructor whose name doesn't obviously map, open its one-line body in `envelope.go` and use the `Type*` constant it passes. `go build` catches a wrong constant (undefined) but NOT a valid-but-wrong one, so verify each against the constructor body.

- [ ] **Step 3: Convert the fault-routing table to ce-type strings.** In `PublishSynth`'s support code, `faultRoute` holds `raised envelopeConstructor` / `cleared envelopeConstructor`. Change both fields to `raised string` / `cleared string` (ce-types), and update `faultRoutes` entries accordingly: `raised: cloudevents.NewFaultRaisedEnvelope` → `raised: cloudevents.TypeFaultRaised`; `cloudevents.NewRsuFaultRaisedEnvelope` → `cloudevents.TypeRsuFaultRaised`; `NewDmsFaultRaisedEnvelope`→`TypeDmsFaultRaised`; `NewEssFaultRaisedEnvelope`→`TypeEssFaultRaised`; `NewRampMeteringFaultRaisedEnvelope`→`TypeRampMeteringFaultRaised`; and the matching `*Cleared`. The two call sites in `PublishSynth` that do `p.publish(ctx, controllerID, route.raised, ev, faultOpt)` / `route.cleared` now pass a string, which matches the new `publish` signature — no further change. `faultRouteFor`, `rsuFaultKinds`, and the kind-resolution logic are unchanged.

- [ ] **Step 4: Update heartbeat.** In `internal/heartbeat/heartbeat.go`: `publishHeartbeat` calls `cloudevents.NewGatewayHeartbeatEnvelope(p.region, p.agency, p.agencyUnit, p.pollerID)` → `cloudevents.New(cloudevents.TypeGatewayHeartbeat, p.region, p.agency, p.agencyUnit, p.pollerID)`. `publishGatewayEvent` takes `makeEnvelope func(region, agency, agencyUnit, pollerID string, opts ...cloudevents.EnvelopeOption) *cloudevents.Envelope` and its two callers pass `cloudevents.NewGatewayDegradedEnvelope` / `cloudevents.NewGatewayRecoveredEnvelope`. Change `publishGatewayEvent`'s param to `ceType string` and its body `env := makeEnvelope(...)` → `env := cloudevents.New(ceType, p.region, p.agency, p.agencyUnit, p.pollerID)`, and update the two callers to pass `cloudevents.TypeGatewayDegraded` / `cloudevents.TypeGatewayRecovered`.

- [ ] **Step 5: Build, vet, and run the goldens + full suite.**

Run: `gofmt -s -w . && go build ./... && go vet ./... && go test ./...`
Expected: clean build/vet; **`internal/events` and `internal/cloudevents` goldens PASS unchanged** (subjects + ce-id determinism), `internal/heartbeat` PASS, rest green. If a golden subject or the catalog test changed, STOP — the collapse altered output; do not "update" the golden to match.

- [ ] **Step 6: Commit.**

```bash
git add internal/events/publisher.go internal/heartbeat/heartbeat.go
git commit -m "refactor(events,heartbeat): construct envelopes via cloudevents.New(ceType)"
```

---

### Task 4: Delete the now-unused per-event + per-service constructors

**Files:**
- Modify: `internal/cloudevents/envelope.go` (delete the 8 per-service wrappers and the ~75 `NewX Envelope` convenience constructors)

- [ ] **Step 1: Delete the dead constructors.** In `internal/cloudevents/envelope.go`, remove the 8 per-service wrapper functions (`NewSignalControlEventEnvelope`, `NewDMSEventEnvelope`, `NewESSEventEnvelope`, `NewRSUEventEnvelope`, `NewRampMeteringEventEnvelope`, `NewGatewayEventEnvelope`, `NewTrafficSensorEventEnvelope`, `NewPerceptionEventEnvelope`) and the per-event convenience constructors (`NewFaultRaisedEnvelope` … `NewPerceptionFaultClearedEnvelope`) — the entire "per-event convenience constructors" and "per-service" wrapper blocks. Keep `newEnvelope`, `New`, `specByToken`, `dataschemaURL`, the `Envelope` type, the option functions, `AssignContentID`, `ToNATSHeaders`, and `FromNATSHeaders`.

- [ ] **Step 2: Let the compiler find any remaining reference.**

Run: `go build ./...`
Expected: clean. If the build reports an undefined `cloudevents.NewX Envelope`, that call site was missed in Task 3 — fix it to `cloudevents.New(cloudevents.TypeX, …)` (or the appropriate typed publisher method), then rebuild. Do NOT re-add the deleted constructor.

- [ ] **Step 3: Run vet + the full suite (goldens are the correctness guard).**

Run: `gofmt -s -w . && go vet ./... && go test ./...`
Expected: clean vet; all tests green, including the unchanged `internal/events` subject golden and `internal/cloudevents` catalog test. Confirm `wc -l internal/cloudevents/envelope.go` dropped substantially (was 801).

- [ ] **Step 4: Commit.**

```bash
git add internal/cloudevents/envelope.go
git commit -m "refactor(cloudevents): delete ~75 per-event + 8 per-service envelope constructors

All construction now flows through the generic New(ceType). Guarded by the
publisher subject golden and the catalog self-consistency test."
```

---

## Self-Review

**Spec coverage (design §5 "collapse the ~50 typed methods behind emit.New"):** `New` is the single constructor (Task 2); publisher + heartbeat build through it (Task 3); the 75+8 constructors are deleted (Task 4). The pre-analysis (0/67 hand-typed vs derived mismatches) is why no per-constructor equivalence test is needed — the risk it would guard cannot occur. Guards: Task-1 subject/ce-id golden (end-to-end, unchanged across Tasks 3–4) + Task-2 catalog self-consistency + the compiler (Task 4 Step 2). The publisher's ~40 typed `Publish*` methods are intentionally KEPT (they are the callers' API and the future Sink's surface); only their internal envelope construction changes — collapsing them is out of scope for P1b.

**Placeholder scan:** No "TODO"/"similar to Task N". Task 2/3's mechanical steps give the transformation rule plus concrete examples for the irregular names (Legacy ESS, gen-2 fault/mode, gateway); the regular majority is a documented 1:1 rule (`NewXEnvelope`→`TypeX`) with `go build` + golden as the net.

**Type consistency:** `New(ceType, region, agency, agencyUnit, id string, opts ...EnvelopeOption) *Envelope` is used with that signature in Task 3 (publisher `publish`, heartbeat). `faultRoute.raised/cleared` change from `envelopeConstructor` to `string`; the `PublishSynth` call sites already pass those fields positionally into `publish`, whose 3rd param is now `ceType string` — consistent. `events.JSPublisher` fake matches `PublishMsg(ctx, *nats.Msg, ...jetstream.PublishOpt) (*jetstream.PubAck, error)`. `registry.Registry`/`registry.Agency` field names match `internal/registry/agency.go`.
