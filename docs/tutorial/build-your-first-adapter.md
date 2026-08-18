# Build your first adapter

This is a hands-on path from `git clone` to watching an event you caused
arrive on the bus. It offers no choices: one vendor (`acme`, invented for
this tutorial), one device kind (`asc`, a signal controller), one facet
(signal status), one outcome. Where you'd normally decide something, this
document just tells you what to type.

Ten minutes of reading, about an hour of doing. No real device and no
external broker: everything runs against an in-memory fake SNMP agent
(`sdk/transport/snmp/snmptest.Static`) and an embedded, in-process NATS
server that the test suite starts and tears down itself.

## 1. What you'll build

You'll add `internal/vendors/acme/`, a new vendor adapter for a fictional
signal controller. It implements the collector's `StateReader` contract,
polls one (made-up) SNMP OID, and produces one `sdk/model` facet:
`model.SignalStatus`. You'll register it, prove it reports a failure
honestly before proving it reports success correctly, and then run it for
real against the collector's actual pipeline — config loading, the poll
loop, the synth engine, the wire emitter, the publisher — and watch the
event it produces land on a NATS subject as a CloudEvent.

If you want the whole picture before touching code,
[`docs/explanation/architecture.md`](../explanation/architecture.md) draws
the four-stage pipeline (adapter → synth engine → wire emitter → publisher)
this tutorial walks in practice. You don't need to read it first — every
concept it names shows up here at the point you need it, with a link back —
but if you like to see the map before the walk, that's the page.

**The `acme` adapter you're about to build is teaching material.** It is not
a real vendor, its OID is invented, and it is deliberately never committed
to this repository — it lives only in this document's code blocks. When
you're done, you'll delete your throwaway clone and the real repo will show
no trace of it.

## 2. Check your setup

```bash
git clone <this repo> vikasa-collector-tutorial
cd vikasa-collector-tutorial
make check
```

Expected: every package's tests pass, the boundary lint is clean, and the
docs lint is clean. You should see something close to:

```
go vet ./...
go test ./...
ok  	github.com/Vikasa2M/vikasa-collector/cmd/collector	...
ok  	github.com/Vikasa2M/vikasa-collector/internal/app	...
...
./scripts/lint-boundary.sh
lint-boundary: clean (2 roots transitively, 14 packages for direct imports) against openits-models
lint-boundary selftest: Rule A fires correctly
lint-boundary replace-rule selftest: Rule C fires correctly
./scripts/lint-docs.sh
lint-docs: clean (... docs, ... skills)
```

There is nothing else to install. No SNMP agent, no NATS server, no
container. Two things make that true, and both matter for what you're about
to build, not just for running the existing suite:

- **The "device" is `snmptest.Static`.** Every adapter test in this repo,
  including the one you're about to write, replays a fixed `OID → value`
  map instead of talking to a real SNMP agent. `Static` implements the same
  `snmp.Client` interface a real device connection would, so the adapter
  code under test cannot tell the difference.
- **The NATS server is an embedded test dependency.** `github.com/nats-io/nats-server/v2/server`
  is a real, in-process JetStream server the test suite starts on a random
  port and shuts down when the test ends — not a fake, not a mock, the
  actual broker the collector publishes to in production, just started
  inside the `go test` process instead of as a separate one.

[`docs/explanation/testing-strategy.md`'s "Fixture replay" section](../explanation/testing-strategy.md#fixture-replay-reproducible-not-verified)
goes deeper on exactly what a fixture-backed test like this proves (the
adapter parses what it's handed correctly) and what it cannot prove by
construction (that the fixture resembles what a real device sends) — worth
reading once you're past this tutorial and writing a real adapter.

Next, confirm the machinery you're about to build against actually works,
before you add anything to it:

```bash
go test ./internal/app/ -run TestEndToEnd -v
```

Expected:

```
=== RUN   TestEndToEndHealthEventsReachJetStream
--- PASS: TestEndToEndHealthEventsReachJetStream (0.01s)
PASS
ok  	github.com/Vikasa2M/vikasa-collector/internal/app	0.012s
```

Open `internal/app/app_test.go` and read `TestEndToEndHealthEventsReachJetStream`.
This is the shape you'll reuse in step 8: it starts an embedded `nats-server`,
builds a small `adapter.Registry` with a fake adapter registered directly
against it (no real transport), subscribes to every subject on the bus
*before* starting the collector, calls the real `app.Run` — the same
function `cmd/collector/main.go` calls in production — and asserts specific
CloudEvents show up. Nothing here is a special test-only code path; it is
the real collector, pointed at a fake device and an embedded broker.

## 3. Read the reference implementation first

Before writing anything, read `internal/vendors/ntcip/asc.go` — the one
adapter that ships in this repo today, and the model to build from. Three
things in its `Read` method are worth noticing, because they're the parts a
first draft gets wrong:

1. **Per-facet failure isolation.** `Read` calls `readSignalStatus`,
   `readFaultSet`, and `readDetectors` unconditionally and independently.
   One OID not answering fails *that* facet — it never stops the others
   from being read and reported.
2. **The absence-of-evidence rule, in practice.** `readSignalStatus` and
   `readFaultSet` both distinguish "the device told us nothing" (record a
   `model.FacetError`, touch nothing else) from "the device told us zero"
   (a real, present, empty-or-quiet facet value — zero alarm bits is a
   healthy controller, not a read failure).
3. **A synthesized-index table read that avoids ~510 round trips.**
   `readDetectors`'s doc comment explains why it builds the full list of
   indexed OIDs up front and issues one batched `Get`, rather than walking
   the table one OID at a time.

[`docs/explanation/adapter-to-model.md`](../explanation/adapter-to-model.md)
goes deep on all three states a facet can be in (present-with-data,
present-and-empty, absent) if you want the full reasoning behind point 2
before you write your own adapter's failure handling.

**One warning before you copy anything: `ntcip-asc`'s fixtures do not meet
this repo's own testing bar, and its alarm-bitmap table admits as much in
its own comment.** ADR 0008 requires *recorded* fixtures — raw responses
captured from a real or simulated device. `internal/vendors/ntcip/asc_test.go`'s
fixtures are hand-typed Go map literals instead, and the adapter's alarm
bitmap comment says outright that it has "never been validated against a
physical controller." This is a known, tracked gap, not something to
replicate — see
[`docs/README.md`'s known-gaps list](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state)
for the full accounting. Copy the *structure* — failure isolation, the
absence-of-evidence handling, the golden-test shape — not the fixture
provenance. This tutorial's own fixtures are hand-typed too (there is no
real acme device to record from — it's invented), which is fine for
teaching material that never ships, but would not clear review for a real
contribution. Section 9 comes back to this.

## 4. Write the adapter

Create `internal/vendors/acme/asc.go`:

```go
// Package acme is a teaching adapter for the "build your first adapter"
// tutorial. ACME is not a real vendor; the OID below is invented. Nothing
// here should be copied into a real vendor integration — see
// internal/vendors/ntcip/asc.go for the reference implementation.
package acme

import (
	"context"
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// oidOperationStatus is the (fictional) OID the acme-asc controller answers
// with its current operating mode.
const oidOperationStatus = ".1.3.6.1.4.1.99999.1.1.0"

var ascDescriptor = adapter.Descriptor{
	Vendor: "acme", DeviceKind: "asc", Caps: adapter.CapState,
}

type asc struct {
	deviceID string
	client   snmp.Client
	now      func() time.Time
}

// NewASC wraps an SNMP client as the acme-asc StateReader. Exported so
// fixture tests can inject a client.
func NewASC(deviceID string, client snmp.Client) adapter.StateReader {
	return &asc{deviceID: deviceID, client: client, now: time.Now}
}

func (a *asc) Descriptor() adapter.Descriptor { return ascDescriptor }
func (a *asc) Close() error                   { return a.client.Close() }

func (a *asc) Read(ctx context.Context) (*model.Snapshot, error) {
	vals, err := a.client.Get(ctx, []string{oidOperationStatus})
	if err != nil {
		// The whole Get failed: the device is unreachable. That is a hard
		// Read error which the runner turns into a health event — not a fault.
		return nil, fmt.Errorf("acme-asc %s: %w", a.deviceID, err)
	}
	snap := &model.Snapshot{DeviceID: a.deviceID, SampledAt: a.now().UTC()}
	a.readSignalStatus(snap, vals)
	return snap, nil
}

func (a *asc) readSignalStatus(snap *model.Snapshot, vals map[string]int64) {
	op, ok := vals[oidOperationStatus]
	if !ok {
		// Mandatory OID unanswered: report the facet failed rather than
		// fabricating state (absence of evidence is never a state change).
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "operation-status OID unanswered",
		})
		return
	}
	snap.Facets = append(snap.Facets, model.SignalStatus{Mode: modeFromOperation(op)})
}

func modeFromOperation(v int64) model.ControllerMode {
	switch v {
	case 2:
		return model.ModeNormal
	case 4:
		return model.ModeFlash
	default:
		return model.ModeUnknown
	}
}
```

This is deliberately the smallest adapter that is still a *real* one: one
OID, one facet, but the same shape as `ntcip-asc` — a hard `Read` error
only for a transport failure, a `FacetError` for a mandatory value that
didn't answer, and a real facet value otherwise. Nothing here is a
simplification you'd have to unlearn; a second facet on this same adapter
would follow exactly the same pattern as `readSignalStatus`, one method per
facet, called unconditionally from `Read`.

Confirm it compiles:

```bash
go build ./internal/vendors/acme/...
```

## 5. Register it

An adapter that isn't registered doesn't exist as far as the collector is
concerned — [`docs/explanation/pluggability.md`](../explanation/pluggability.md)
covers why this is a deliberate, in-tree, compile-time decision rather than
a plugin system. Registering `acme-asc` is two pieces: a `RegisterTo`
function the package exposes, and one line wiring it into the binary.

Create `internal/vendors/acme/register.go`:

```go
package acme

import (
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// RegisterTo registers acme-asc. The connection block is
//
//	connection:
//	  snmp: { address: "host:161", community: "public" }
func RegisterTo(r *adapter.Registry) {
	r.Register(ascDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseSNMPBlock(conn)
		if err != nil {
			return nil, fmt.Errorf("acme-asc %s: %w", deviceID, err)
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

Now edit `cmd/collector/main.go`. Only two lines change from the file
you cloned: one new import (`.../internal/vendors/acme`) and one new call
in `RegisterAdapters` (`acme.RegisterTo(r)`) — the one place in the whole
binary that decides which vendors it ships with. Everything else in the
file, including `func main()` below, is unchanged. To make that
unmistakable rather than something you have to work out, here is the
complete file with those two lines in place — replace
`cmd/collector/main.go` with this:

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

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/acme"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/ntcip"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

var version = "dev" // set via -ldflags "-X main.version=..."

// RegisterAdapters wires every compiled-in adapter into the registry. This is
// the one place the binary decides which vendors it ships with; contributing an
// adapter means adding a line here plus internal/vendors/<vendor>/<kind>.go.
func RegisterAdapters(r *adapter.Registry) {
	ntcip.RegisterTo(r)
	acme.RegisterTo(r)
}

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
	RegisterAdapters(reg)

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

Confirm the whole binary still builds with `acme` wired in:

```bash
make check
```

This should still be green — `acme-asc` is registered and compiled in, but
nothing has polled it yet, because `collector.yaml` doesn't mention it. That
happens in step 8.

## 6. Prove a failed read emits nothing

Write this test before the happy path. It is the invariant most likely to
be got wrong on a first adapter, and it is worth understanding *why* before
you write the version that succeeds: **[absence of evidence is never a state
change](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).**
A facet the device didn't answer must never be reported as a default or
empty value — it must be absent from the snapshot entirely, with a
`FacetError` explaining why. Get this backwards and a timed-out OID reads
downstream as "the controller reported mode unknown," which is a real,
false, published event about a device that never said anything of the
kind.

That one sentence is this tutorial's paraphrase, kept here because you need
it in front of you while writing the test. The linked row in
`invariants.md` is the canonical wording — it names the ADR that decided
the rule and the tests that enforce it today, and it is the one kept true
if the two ever drift apart.

Create `internal/vendors/acme/asc_test.go`:

```go
package acme

import (
	"context"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// Written before the happy path: absence of evidence is never a state
// change, and this is the invariant most likely to be got wrong.
func TestASCUnansweredOperationStatusIsFacetError(t *testing.T) {
	a := NewASC("asc-1", &snmptest.Static{Values: map[string]int64{}})
	snap, err := a.Read(context.Background())
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
```

`snmptest.Static{Values: map[string]int64{}}` replays an *empty* OID map —
every `Get` call returns nothing, exactly like a real agent that never
answered. Run it:

```bash
go test ./internal/vendors/acme/ -v
```

Expected:

```
=== RUN   TestASCUnansweredOperationStatusIsFacetError
--- PASS: TestASCUnansweredOperationStatusIsFacetError (0.00s)
PASS
ok  	github.com/Vikasa2M/vikasa-collector/internal/vendors/acme	0.001s
```

## 7. Write the golden read test

Now the happy path: a fixed input produces an exact, asserted output.
Replace `internal/vendors/acme/asc_test.go` with the following — same file,
now with both tests and the `reflect` import the new one needs:

```go
package acme

import (
	"context"
	"reflect"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// Written before the happy path: absence of evidence is never a state
// change, and this is the invariant most likely to be got wrong.
func TestASCUnansweredOperationStatusIsFacetError(t *testing.T) {
	a := NewASC("asc-1", &snmptest.Static{Values: map[string]int64{}})
	snap, err := a.Read(context.Background())
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

func TestASCReadGolden(t *testing.T) {
	fx := map[string]int64{oidOperationStatus: 2} // 2 == normal
	a := NewASC("asc-1", &snmptest.Static{Values: fx})
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	f, ok := snap.Facet(model.KindSignalStatus)
	if !ok {
		t.Fatal("missing signal-status facet")
	}
	want := model.SignalStatus{Mode: model.ModeNormal}
	if got := f.(model.SignalStatus); !reflect.DeepEqual(got, want) {
		t.Fatalf("SignalStatus = %+v, want %+v", got, want)
	}
}
```

Run the package's tests again:

```bash
go test ./internal/vendors/acme/ -v
```

Expected:

```
=== RUN   TestASCUnansweredOperationStatusIsFacetError
--- PASS: TestASCUnansweredOperationStatusIsFacetError (0.00s)
=== RUN   TestASCReadGolden
--- PASS: TestASCReadGolden (0.00s)
PASS
ok  	github.com/Vikasa2M/vikasa-collector/internal/vendors/acme	0.001s
```

Both tests green. Your adapter reports a failed read honestly and a
successful read correctly — the two facts every adapter in this repo has to
get right, proven independently, in that order.

## 8. Watch it reach the bus

This is the payoff: the same `app.Run` the real binary calls, pointed at
your adapter and a fake device, publishing to a real embedded broker.

One thing to know before you write this test: the factory you registered in
step 5 (`RegisterTo`) dials a real SNMP connection over UDP. That's correct
for production, but this tutorial has no real device for it to dial — and
per step 2, it doesn't need one. So this test builds its own tiny
`adapter.Registry` entry, the same way `TestEndToEndHealthEventsReachJetStream`
(step 2) does for its own fake adapter: register `acme-asc` directly against
a factory that calls `NewASC` with `snmptest.Static`, skipping the network
entirely. `RegisterTo` still exists, is still wired into
`cmd/collector/main.go`, and is still what a real deployment uses — this
test just isn't a real deployment.

Create `internal/vendors/acme/e2e_test.go`:

```go
package acme

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp/snmptest"
)

// TestWatchEventReachJetStream runs the real collector spine — config,
// runner, synth engine, wire emitter, publisher — against an embedded
// nats-server and the acme-asc adapter reading a fake device
// (snmptest.Static). No real device, no external broker: exactly what
// internal/app/app_test.go's own end-to-end test does for the collector's
// health events, aimed here at a domain event instead.
func TestWatchEventReachJetStream(t *testing.T) {
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	// A registry with acme-asc built directly against snmptest.Static — no
	// real SNMP dial, so the "device" is the fixture, not the network.
	reg := adapter.NewRegistry()
	reg.Register(ascDescriptor, func(deviceID string, _ map[string]any) (adapter.Adapter, error) {
		fx := map[string]int64{oidOperationStatus: 2} // 2 == normal
		return NewASC(deviceID, &snmptest.Static{Values: fx}), nil
	})

	cfgYAML := `
collector_id: tutorial-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: asc-1, vendor: acme, device_kind: asc, poll_interval: 20ms, connection: {} }
`
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe BEFORE starting the app, same reason app_test.go does: a
	// core NATS sub only sees JetStream traffic published after it exists.
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	seen := make(chan *nats.Msg, 64)
	sub, err := nc.Subscribe(">", func(m *nats.Msg) {
		if m.Header.Get("ce-type") != "" {
			seen <- m
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = app.Run(ctx, cfg, reg, ns.ClientURL(), "test") }()

	const want = "openits.signal-control.operational-status-report.v1"
	deadline := time.After(8 * time.Second)
	for {
		select {
		case m := <-seen:
			ceType := m.Header.Get("ce-type")
			t.Logf("subject=%s ce-type=%s ce-source=%s ce-id=%s",
				m.Subject, ceType, m.Header.Get("ce-source"), m.Header.Get("ce-id"))
			if ceType == want {
				return // found it — the point of this test
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}
```

Run it:

```bash
go test ./internal/vendors/acme/ -run TestWatchEventReachJetStream -v
```

Expected — the exact `ce-id` values are different every run (they're a
deterministic hash of, among other things, the observation timestamp, so a
new run at a new time produces new ids; see
[`docs/explanation/wire-boundary.md`](../explanation/wire-boundary.md) if
you want the full derivation), but the subjects and ce-types are not:

```
=== RUN   TestWatchEventReachJetStream
    e2e_test.go:91: subject=openits-collector.us-ga.metro.d01.health.collector.collector-started ce-type=openits-collector.health.collector-started.v1 ce-source=urn:openits:collector:us-ga:metro:d01:cab-1 ce-id=01M09A1W6PPFSW7TVJKDHRMYYD
    e2e_test.go:91: subject=openits.us-ga.metro.d01.signal-control.asc-1.operational-status-report ce-type=openits.signal-control.operational-status-report.v1 ce-source=urn:openits:controller:us-ga:metro:d01:asc-1 ce-id=01M09A1W74NWZEGW4RMVKGNBW5
--- PASS: TestWatchEventReachJetStream (0.02s)
PASS
ok  	github.com/Vikasa2M/vikasa-collector/internal/vendors/acme	0.023s
```

Two events, in order: the collector's own boot event first
(`collector-started`, on the `openits-collector.*` namespace — collector
health always publishes there, never mixed with device telemetry), then the
event your adapter caused: `operational-status-report`, on
`openits.us-ga.metro.d01.signal-control.asc-1.operational-status-report`.
Read that subject against the default template
(`{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}`,
[configuration reference](../reference/configuration.md#fields)) and every
token traces back to something you wrote: `asc-1` is the device id from
`collector.yaml`, `signal-control` is where `asc` devices live in the
profile's namespace, and `operational-status-report` is the event your
`readSignalStatus` caused by successfully reading one OID.

That's the whole loop, closed: your `Read` produced a `model.SignalStatus`
facet, `synth.NewSignalDiffer` (`internal/synth/signal.go`) turned it into a
`model.OperationalStatusReport` domain event on the very first poll (no
prior state needed for a status report — only *transition* events like
`ModeChanged` need one), the `openits` wire emitter mapped that event kind
plus your device's `asc` kind to a real catalog ce-type, and the publisher
rendered the subject and shipped it. Nothing in this chain is
tutorial-specific — it's the same four stages
[`architecture.md`](../explanation/architecture.md) describes, run for real
against code you wrote in the last twenty minutes.

You didn't have to decode the CloudEvents payload's bytes to see this
proof, and there's a real reason why: `internal/vendors/acme` — like
`internal/vendors/ntcip` and every vendor adapter — is not allowed to
import `openits-models`, the module that defines what those payload bytes
mean (that's ADR 0002's boundary, enforced by `scripts/lint-boundary.sh`;
see [`docs/explanation/wire-boundary.md`](../explanation/wire-boundary.md)
for why the line is drawn there). The `ce-*` headers you just read —
`ce-type`, `ce-source`, `ce-id` — are the CloudEvents envelope, and reading
them requires nothing past `nats.go`'s `Msg.Header`. Decoding the payload
body itself is `internal/wire/openits`'s job; `internal/wire/openits/golden_test.go`
is where that actually happens, one fixed input encoded to exact,
pre-verified bytes per ce-type.

## 9. What you skipped, and where to go next

A few things this tutorial deliberately didn't make you do, and what to
read before you do them for real:

- **Recorded fixtures.** Both test files above use hand-typed
  `map[string]int64` literals, same as `ntcip-asc`'s do — fine here, because
  `acme` is invented and there is no real device to record from, and this
  code is never merged. A real adapter contribution has to clear ADR 0008's
  bar instead: recorded raw transport responses, not hand-typed ones. The
  tool that would make recording easy — a fixture recorder plus adapter
  conformance kit — does not exist yet; it's tracked as
  [successor work in `docs/README.md`'s known-gaps list](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state),
  alongside the exact same gap in `ntcip-asc`'s own fixtures. Until it
  exists, "record from a real or simulated device" is a manual step you do
  yourself and describe in the PR.
- **The full test bar for a real adapter.** You wrote a golden test and a
  failed-read test for one facet. A real adapter needs one of each *per
  facet*, plus connection-parse rejection tests and a capability-bits check
  — the complete checklist is
  [`docs/reference/test-requirements.md`'s "A new adapter" section](../reference/test-requirements.md#a-new-adapter).
- **A real vendor, and a real first contribution.** Five of the eight facet
  kinds this collector models are fully diffed and wired to real ce-types
  with no adapter producing them yet —
  [`docs/reference/starter-tasks.md`](../reference/starter-tasks.md) explains
  why landing one of those is the safest, highest-leverage first PR in this
  repo, and lists exactly what each of the five needs.
- **The canonical steps, without the training wheels.**
  [`docs/how-to/add-a-vendor-adapter.md`](../how-to/add-a-vendor-adapter.md)
  is the guide this tutorial is a rehearsal for — the same path written for
  someone shipping a real adapter instead of an invented one: package
  layout, connection parsing, capability bits, what belongs in the PR and
  what does not. Read it before you write the real one; the
  [`add-vendor-adapter`](../../.claude/skills/add-vendor-adapter/SKILL.md)
  skill is the terse checklist form of the same workflow.

Before you start that PR, delete your throwaway clone from step 2 — nothing
in it should follow you into a real branch. And if you skipped ahead and
still have an `acme` adapter sitting in *this* repo's working tree, this is
the point to check `git status --short` and make sure it isn't there:
`acme` was never meant to leave this document.
