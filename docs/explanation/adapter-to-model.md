# Facets and the synth engine: from a device reading to a domain event

[`architecture.md`](architecture.md) walks the whole pipeline —
adapter → synth → wire emitter → publisher — at the altitude of "what does
each stage own." This document stays in the first two stages and goes deep:
what a `Snapshot` actually is, what an adapter is and is not allowed to do
with one, and exactly how `synth.Engine.Apply` turns a snapshot into domain
events. This is the layer most contributors touch first — adding a facet,
adding a differ, or wiring a new adapter all live here — so it is worth
understanding in more than one paragraph.

## Snapshot: a header plus typed facets

Every poll produces one `model.Snapshot` (`sdk/model/model.go`):

```go
type Snapshot struct {
	DeviceID   string
	DeviceKind string
	SampledAt  time.Time
	Facets     []Facet
	Errors     []FacetError
}
```

There is no god-struct with one field per possible reading. A `Snapshot` is
a header (`DeviceID`, `DeviceKind`, `SampledAt`) plus a slice of `Facet`
values — typed structs that each implement `Facet interface{ FacetKind()
Kind }` — plus a slice of `FacetError` for anything the adapter tried to
read and could not.

`DeviceKind` is not something an adapter sets. `asc.Read` (the only method
an adapter implements to produce a `Snapshot`) never touches it; it is
stamped by the runner, once, after `Read` returns:

```go
// internal/runner/runner.go
snap.DeviceKind = r.deviceKind
```

(`r.deviceKind` itself comes from the adapter's `Descriptor()` at
construction time, not from `Read` — see `internal/runner/runner.go:47`.)
The reason is structural, not a style preference: `DeviceKind` has to be
trustworthy for every snapshot the runner produces, including the ones
where `Read` failed outright, so it cannot live inside the thing `Read`
builds. An adapter that wanted to lie about its own device kind has no code
path to do so.

## Eight facets, one per device kind

A facet is one typed slice of device state — signal-controller mode, a set
of raised faults, a batch of detector samples. There are eight registered
facet kinds today (`grep -n 'Kind[A-Za-z]* Kind = ' sdk/model/*.go`):

| Kind constant | Facet type | What it holds |
|---|---|---|
| `KindSignalStatus` | `SignalStatus` | signal-controller mode, conflict flash, active plan, preemption |
| `KindFaultSet` | `FaultSet` | every fault currently raised on the device |
| `KindDetectorSamples` | `DetectorSamples` | per-channel vehicle detector counts, one sample per answering channel |
| `KindDMSStatus` | `DMSStatus` | a dynamic message sign's control mode and display state |
| `KindCCTVStatus` | `CCTVStatus` | who is driving a camera and what its tours are doing |
| `KindTrafficIntervals` | `TrafficIntervals` | a completed measurement interval from a roadside traffic sensor |
| `KindZoneIncidents` | `ZoneIncidents` | incidents a perception sensor currently observes in its zones |
| `KindZoneIntervals` | `ZoneIntervals` | a completed aggregate counting interval over a perception sensor's zones |

(The design spec this document was harvested from named four — `SignalStatus`,
`FaultSet`, `DetectorSamples`, and a never-built `RSUBroadcastCounters` —
written when the domain model was much younger. `RSUBroadcastCounters`
never shipped; DMS, CCTV, the two traffic-sensor facets, and the two
perception facets were added since. Trust the table above, or re-run the
`grep` yourself — it is one command.)

Only one adapter exists in this repo today (`ntcip-asc`), and it produces
three of the eight (`SignalStatus`, `FaultSet`, `DetectorSamples`). The
other five are fully modeled, diffed, and wired to an emitter with no
device on the other end yet — adding an adapter for one is a matter of
building the transport read, not touching the domain model.

## Present, absent, or empty: three states a facet can be in

A facet kind, for one device on one poll, is in exactly one of three
states, and adapters have to get the distinction right:

1. **Present with data.** The adapter read the facet successfully and
   appended it to `snap.Facets`.
2. **Present and genuinely empty.** The adapter read the facet
   successfully and the device reported nothing to put in it — a
   controller with zero active faults, a sensor with zero detectors
   wired up, a quiet road with zero incidents. This is still case 1: the
   facet is appended to `snap.Facets`, it just happens to hold an empty
   slice.
3. **Absent.** The read failed — a mandatory OID did not answer, a
   request timed out, the device returned something the adapter could not
   parse. The adapter appends nothing to `snap.Facets` for this kind and
   instead appends a `FacetError` to `snap.Errors`.

`internal/vendors/ntcip/asc.go`'s `readFaultSet` shows both non-obvious
cases side by side:

```go
func (a *asc) readFaultSet(snap *model.Snapshot, vals map[string]int64) {
	bits, ok := vals[oidShortAlarmStatus]
	if !ok {
		// Defaulting to "no faults" here would clear every real fault
		// downstream — the exact gen-1 failure this design exists to prevent.
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: "short-alarm-status OID unanswered",
		})
		return
	}
	// Zero bits is an EMPTY fault set, not an error: a healthy controller.
	fs := model.FaultSet{}
	for _, f := range alarmBitmap {
		if bits&(1<<f.bit) == 0 {
			continue
		}
		fs.Faults = append(fs.Faults, model.Fault{
			ID: f.id, Severity: f.severity, Category: f.category, Description: f.description,
		})
	}
	snap.Facets = append(snap.Facets, fs)
}
```

If the alarm-status OID answers with `bits == 0`, the loop over
`alarmBitmap` appends nothing, and `fs` — an empty `FaultSet{}` — is still
appended to `snap.Facets`: case 2, present and empty, a healthy controller.
If the OID does not answer at all, the function returns before building a
`FaultSet` of any kind: case 3, absent, recorded as a `FacetError` instead.
The same shape shows up elsewhere in the domain model —
`DetectorSamples`' doc comment notes that "a controller with no detectors
yields an EMPTY facet, not an error... a `FacetError` would be a permanent
false alarm," and `ZoneIncidents`' says "a sensor observing a quiet road
yields an EMPTY facet, not an error."

Which of the three states a facet is in for a given poll determines
everything downstream: it is the only information `synth.Engine.Apply`
(next section) has to work with, since it never talks to the device
itself. What the engine does with each state — specifically, what happens
when a facet is absent — is a load-bearing rule with its own canonical
statement, not restated here; see
[`docs/reference/invariants.md`](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).

## `FacetError` and per-facet failure isolation

```go
type FacetError struct {
	Kind Kind
	Err  string
}
```

`FacetError` carries just enough to say which facet kind failed and why —
no retry count, no severity, no timestamp of its own (the snapshot already
has `SampledAt`). One poll can produce multiple `FacetError`s alongside
multiple successful facets: `ntcip-asc`'s `Read` calls `readSignalStatus`,
`readFaultSet`, and `readDetectors` independently in the same poll, and
each one decides for itself whether its own OID(s) answered. A timeout on
the alarm-status OID does not stop `readDetectors` from running, and does
not turn the whole poll into a failure — it produces one `FacetError` for
`KindFaultSet` in a `Snapshot` that may otherwise be entirely healthy.
Failure is isolated per facet, not per poll or per device.

## How `synth.Engine.Apply` consumes facets

```go
func (e *Engine) Apply(snap *model.Snapshot) []model.Event {
	...
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

The loop's only source of work is `snap.Facets`. It never looks at
`snap.Errors`, never asks "did anything fail this poll," and never
constructs a stand-in value for a kind it didn't find. For each facet that
*is* present, it looks up the one `Differ` registered for that facet's
`Kind()`, calls `Diff` against whatever facet of that kind it last saw for
this device (`dev[f.FacetKind()]`, `nil` on the very first poll), collects
whatever events `Diff` returns, and then updates its own memory to `f` —
this poll's value — for next time.

A facet kind that is absent this poll — case 3 above — simply never
appears in the range over `snap.Facets`, so the lookup at
`e.differs[f.FacetKind()]` and the call to `d.Diff(...)` never happen for
it this poll. There is no `if failed { ... }` branch that could have been
written wrong, because there is no branch at all for that case — the loop
only ever iterates what is present. What that structural fact guarantees
about the device's remembered state is the invariant linked in the
previous section; this paragraph describes the loop's shape, not what the
shape guarantees.

## Facets are per-device-kind, never per-vendor

`internal/synth`'s own package doc says it in one line: "One registered
`Differ` per facet kind; the engine never grows vendor or wire knowledge."
Nothing in `internal/synth` branches on which vendor produced a facet —
every registered differ's `Kind()` method returns a `model.Kind`, never a
vendor name, and there is no `Vendor` field anywhere in the package
(`grep -n 'Vendor\b' internal/synth/*.go` returns nothing).

This is a governance rail, not just an implementation detail: a facet
exists for a device **kind** — a signal controller, a DMS, a perception
sensor — never for one vendor's product line. If a contributor's vendor
adapter needs to represent something no existing facet captures, the
options are a new facet type reviewed at the kind level, or the
`Attributes map[string]string` escape hatch that wire emitters can map or
drop explicitly — not a one-off field bent to fit that vendor's controller.
A facet invented for one vendor's convenience means the domain model is
being bent to fit a device, and the next vendor's adapter for the same
device kind inherits the bend whether it wants to or not.

## Test coverage of the absence path

The absence-of-evidence invariant linked above is enforced by
`Engine.Apply`'s shape for all eight facet kinds — the loop has no
per-kind branch, so there is nothing kind-specific to get wrong. Whether
that has actually been *proven* per kind is a different question, and the
honest answer today is no. Only four of the eight registered differs have
a test that drives a failed read through and checks the outcome:

```
$ grep -cE 'func Test.*(Fail|Absent|Suspend|NeverClear)' internal/synth/*_test.go
internal/synth/cctv_test.go:0
internal/synth/detector_test.go:1
internal/synth/dms_test.go:2
internal/synth/fault_test.go:1
internal/synth/perception_test.go:0
internal/synth/signal_test.go:1
internal/synth/trafficsensor_test.go:0
```

Signal, fault, detector, and DMS have one; CCTV, traffic-interval,
zone-incident, and zone-interval have none. This document does not claim
otherwise, and does not fix it — it is tracked as open successor work in
[`docs/README.md`'s known-gaps list](../README.md#known-gaps-and-successor-work)
and in [`invariants.md`'s own row](../reference/invariants.md#absence-of-evidence-is-never-a-state-change)
for this rule.
