# openits-models regeneration → collector migration analysis (2026-07-10)

`openits-models` HEAD `ddd9579` ("Generate wire proto from YANG; delete hand-authored proto")
regenerated the proto from YANG. The collector (`replace => ../openits-models`) no longer compiles.
This is **not a pure rename** — several concepts were removed or are un-generated upstream.

## Mechanical renames (safe, ~70% of the work)

| Old (openitspb.) | New (openitspb.) |
|---|---|
| `WireSource_IndianaTag` (nested) | `Indiana` (top-level) — fields `IndianaCode`/`IndianaParam` unchanged. ~15 sites in indianamap.go + decoder.go |
| `PreemptionEventDetail` (message) | `PreemptionEvent` — ingest.go, publisher.go, synthetic.go, generator.go |
| `PreemptionActivated_PreemptionType` | `PreemptionType` (top-level); `PREEMPTION_TYPE_UNSPECIFIED` → `PREEMPTION_TYPE_NONE` (now 0) |
| `FaultRaised_Severity` | `FaultSeverity` (top-level); `SEVERITY_*` → `FAULT_SEVERITY_*` (no UNSPECIFIED; INFO=0) |
| `PhaseState` / `PHASE_STATE_*` | `ToState` / `TO_STATE_*` (field `PhaseStateChange.ToState` unchanged; no `FLASH`) |
| `PhaseTerminationReason` | `TerminationReason` |
| `CoordChangeKind` | `ChangeKind` |
| `DetectorEventKind` | `Transition` (`TRANSITION_*`) |
| `OverlapEventKind` | `Event` (`EVENT_*`) |
| `PedEventKind` | `OpenitsSignalControlPedestrianEventsEvent` (long module-qualified prefix) |
| `TspEventKind` | `OpenitsSignalControlTspEventsEvent` (long prefix) |
| `PreemptStage` | `Stage` (`STAGE_*`) |
| `ZoneIncidentDetected_Severity` | `Severity` (top-level); default → `SEVERITY_INFO` |
| `TrafficIntervalReport_Lane` | `Lane` (top-level); field `TrafficIntervalReport.Lanes` → `Lane` (singular, still a slice) |
| `TrafficSensorStatusReport_OPERATIONAL_STATUS_ACTIVE/INACTIVE` | `OperationalStatus_OPERATIONAL_STATUS_ACTIVE/INACTIVE` (ACTIVE=0) |

Field renames on surviving messages: `ModeChanged.From/To` → `Prior/Current` (now **string**);
`PreemptionActivated.ActivatedAt`/`PreemptionCleared.ClearedAt`/`FaultRaised.FirstObserved`/`FaultCleared.ClearedAt`
→ `OccurredAt`. `WireSource`/`WireSource_Indiana` names unchanged (moved to `openits.types.v1`, same Go pkg).

Affected files (all in the collector): `sdk/driver/driver.go`, `internal/atspm/decoders/{decoder,indianamap/indianamap,synthetic/synthetic,synthetic/generator}.go`,
`internal/atspm/ingest/ingest.go`, `internal/events/{publisher,synth/synth}.go`,
`internal/translator/{signalcontrol,rsu}/translator.go`, `internal/trafficvision/{decode,mapping}.go`,
plus the test files added this session (translator/synth/publisher tests). `internal/cloudevents/*` and
`internal/trafficvision/ingest.go` need **no** changes (string constants / message-pointer types only).

## Blocking decisions / genuine losses

- **C.1 — `OperationalStatus` message + `ControllerMode` enum: GONE (no replacement).** The YANG
  `operational-status` is a `container` (state), not a `notification`, so the from-YANG generator emits no
  message. The collector's steady "current controller mode" heartbeat (`driver.Reading.Mode`,
  `synth.Events.OperationalStatus`, `Publisher.PublishOperationalStatus`, `ntcipModeToOpenITS`) has no proto home.
  Decision: (a) drop the heartbeat, rely on `ModeChanged` only (loses the steady time series); (b) make
  `driver.Reading.Mode` a string and build `ModeChanged{Prior,Current}` (still no periodic snapshot message);
  (c) ask upstream to make `operational-status` a notification / wire it into codegen. **Likely upstream is
  incomplete, not intentional.**

- **C.2 — Fault `Category` (CONFLICT/CABINET/POWER/…): GONE from the generic FaultRaised/ControllerFaultEvent.**
  The common fault YANG folded category into the `kind` identityref. `sdk/driver.Fault.Category`, both SNMP
  translators' `faultBitmap` category column, and `synth.diffFaults` need Category dropped or folded into
  `Description`/`Kind`. Lossy for downstream filtering. (Per-service `Perception/TrafficSensor FaultRaised`
  still keep a `Category string`.)

- **C.3 — TrafficVision numeric fields → `string` (decimal64).** `Lane.{Occupancy,Density,SpeedAverageKmh,…}`
  and `{TrafficSensorStatusReport,ZoneIncidentDetected}.{Latitude,Longitude}` are now `string`. Needs
  `strconv.FormatFloat` (centralize a helper), not a type rename. (Confidence/TrackId stayed int — check field-by-field.)

- **C.4 — TrafficVision camera diagnostics + incident media: UN-GENERATED upstream (generation gap).**
  `TrafficSensorStatusReport_Camera`/`_License` and `ZoneIncidentDetected.{Snapshot,Clip,ClipInput}` exist only
  as vendor-augment YANG (`yang/augments/trafficvision-*`) that was never wired into the proto generator — no
  `schema.proto`, no Go code. The old hand-authored proto inlined them; the regeneration correctly didn't.
  NOT fixable by a collector rename. Either upstream generates the augments (recommended, preserves data) or
  the collector drops camera+incident-media fields from its TrafficVision payloads (real data loss). This means
  **the regeneration is incomplete for TrafficVision.**

- **C.5 — enum renumbering is WIRE-BREAKING.** Dropping synthetic `*_UNSPECIFIED=0` renumbered several enums
  (PreemptionType, FaultSeverity, Severity, traffic-sensor OperationalStatus). Source-compatible after rename,
  but any persisted/replayed old JetStream bytes with old varint enum values will decode to different meanings.
  Confirm no old-format replay dependency.

## Bottom line
The mechanical renames are straightforward but cannot yield a green build alone — `driver.Reading.Mode` (C.1)
and `Fault.Category` (C.2) are struct fields threaded through the renamed code, so those decisions gate the
migration. C.4 indicates the upstream regeneration is not yet complete for TrafficVision augments.
