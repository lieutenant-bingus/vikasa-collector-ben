# ADR 0026: Detector samples carry their collection period

**Status:** Proposed (2026-11-05); implemented for `maxtime-asc`
**Amends:** the detector-samples contract in `sdk/model/detector.go`
(`VolumeCount` was always a cumulative counter).

## Context
The detector differ assumes `VolumeCount` is a cumulative counter and
publishes the increase between polls. NTCIP 1202 says otherwise:
`detectorVolume` is the "volume of traffic crossing the associated
detectorNumber during the collection period" (0-254, 255 = overflow), with
`volumeOccupancySequence` changing when a period completes
([NTCIP 1202 v02.19](https://www.ntcip.org/file/2018/11/NTCIP1202v0219f.pdf)).
In YANG terms it is a per-interval gauge, not a `counter32`. OpenConfig
keeps the two apart (counters with discontinuity time; interval statistics
with their interval) because a consumer cannot recover one from the other.

The MaxTime lab (firmware 2.15.1, 2026-11-04, SNMP-driven actuations)
showed the cost. Detector 1 was pulsed 1 s in 5. The MIB API's `Volume`
held the last completed period's total, which was 9, then 169, then 180 at
17:15, 17:30 and 17:45. The differ published 9, 160 and 11 at whichever
5 s poll followed each roll. Those are differences between period totals,
reported over a 5 s interval: wrong counts and a wrong rate. Two more
findings:

- The period was 900 s (`detectorSampleTime` stepped 900 s, and
  `detectorSampleDuration` read 9000, in tenths). `volumeOccupancyPeriod`
  and the API's `VOccPer` both said 60. Configured period is not evidence.
- `detectorSampleTime` is the period's end, in controller-local wall-clock
  seconds since 1970. 1793812500 arrived at 17:15:02 EST, when the
  17:00-17:15 period closed, and decodes to 17:15 UTC. The adapter must
  read it in the controller's zone, as it does for log timestamps.
- The API's `percentOccupancy` stayed 0 while SNMP `detectorOccupancy`
  read 40 (20%) and 200 (100%). It is not the occupancy the controller
  reports over NTCIP.

## Decision
`DetectorSamples` gains an optional `Period` (sequence, end, duration).
With it set, `VolumeCount` is that period's total. The differ reports a
period once, the first time its sequence appears, as is, over the period's
own interval. Polls that see the same sequence emit nothing. Without
`Period`, the cumulative behaviour stands unchanged.

The first period seen after a start is not reported. Detector state is
volatile, so that period may already have been published, and a duplicate
count is worse than a missing one. That loses at most one period per
restart, the same cost the cumulative path pays.

An adapter sets `Period` only from the controller's own period-completion
evidence (sequence plus sample time and duration), never from the
configured period.

## Consequences
- The wire is unchanged: a detector-report's interval is now the
  collection period. At 900 s that is one report per detector set per
  15 min, not one per poll.
- `maxtime-asc` reads `Volume`, `Occupanc` (NTCIP half-percent; the UI's
  Data Collection Status page uses it), `VOccSeq`, `detectorSampleTime` and
  `detectorSampleDuration` (tenths of a second) in its one batched GET. On
  2026-11-05 the lab compared these against SNMP across a roll and they
  matched: sequence 119 to 120, end 09:30:00 EST, 9000, det 1 at 180/40,
  det 3 at 1/198. `percentOccupancy` is no longer read. Without the
  sequence or the duration, the detector facet fails rather than fall back
  to deltas. The period's end needs the controller's zone, which comes from
  `asclog.timezone`. With no asclog block, the end is unknown and the
  report is anchored at the poll.
- End to end, in the lab on 2026-11-05 (fixture `asc-lab-period.json`): one
  detector-report per roll and none between rolls, each over 900 s and
  equal to SNMP for the period that closed (det 1 180 at 20.0%, det 3 at
  99.0%). A 60 s restart neither repeated nor dropped a period. The first
  period seen after each start was not reported, as decided.
- The 900 s period is `VOccPer` (60) × `auxVolOccNumPeriods` (15), shown
  read-only as `auxVolOccPeriodDuration`.
- The `ntcip` adapter reads `maxVehicleDetectors` at `…2.1.2.3.0` and
  volume/occupancy at `…2.1.2.4.1.{2,4}`. In NTCIP 1202 those are
  `maxVehicleDetectorStatusGroups` and the status-group table. The objects
  are `…2.1.2.1.0` and `…2.1.2.5.4.1.{1,2}`, confirmed by the lab walk.
  That is a separate upstream fix, and it should adopt `Period` from
  `…2.1.2.5.1.0` (sequence) at the same time.
