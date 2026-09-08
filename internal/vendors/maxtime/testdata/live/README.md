# MAXTIME fixture provenance

`healthy.json` is a recorded response envelope from the MAXTIME controller at
`192.168.0.20`, captured on 2026-08-28 through the read-only
`/maxtime/api/mibs/<name>` endpoints. Each top-level key is one MIB name; each
value is the controller's JSON array response body exactly as returned (one
record per MIB).

The controller returned full alarm and detector tables. The fixture keeps the
two detector channels exercised by the golden test (`Volume` / `percentOccupancy`
keys `"1"` and `"2"`) so the test stays compact and deterministic. Values are
real zeros from the bench unit, not hand-typed placeholders.

Key values validated on capture:

| MIB | Index | Value | Adapter meaning |
|-----|-------|-------|-----------------|
| `controllerOpMode` | 1 | 5 | Running → `ModeNormal` |
| `PatrnSta` | 1 | 254 | Free → `ActivePlanID=0` |
| `FlashSta` | 1 | 2 | Off → `InConflictFlash=false` |
| `preemptStatus` | 1 | 0 | No preemption |

`asclog-sample.xml` is a trimmed subset of `GET /v1/asclog/xml/full` from the
same controller on 2026-09-08. The live endpoint returns a fixed ~10 001-event
ring buffer (~860 KB); the sample keeps enough records to cover every
`EventTypeID` observed in that capture (phase, overlap, and unmapped/vendor
codes) so decode tests stay small and auditable.

The fixtures are test input only. Live verification still runs against the
controller separately (`TestASCLive`, env-gated) and must not be required for
the normal test suite.
