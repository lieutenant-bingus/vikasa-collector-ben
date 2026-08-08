# Task 0 — Plan 2a: openits-models emitter

**Date:** 2026-08-08
**Verdict:** CLEAN — proceed
**Probed against:** openits-models `v0.2.3-0.20260807005833-235e8780f44c`
(main HEAD `235e878`), via a throwaway Go module that resolves the real
dependency and marshals real messages. Not against docs.

The Plan 2 spec was written 2026-07-21 against v0.2.2. Its load-bearing
claims were re-probed before any emitter code was written, because the
whole slice hangs on another repo's contract.

## Probes

1. **[LOAD-BEARING] The published `ce-id` test vector reproduces from the
   real generated Go type.** → **PASS, non-vacuous.**
   `dmsv1.MessageActivationFailed{Reason: "validation"}` marshals
   (deterministic) to exactly `1a0a76616c69646174696f6e`, and the full
   chain — `SHA-256` over source ‖ type ‖ stable-time ‖ payload with
   `0x1f` separators, then `ULID(ce-time-ms, digest[0:10])` — yields
   `01KY4V4VG0ZNQNVEQB1WEBSX24`, matching `docs/ce-id-spec.md`.
   This single probe covers the proto package, field numbering, the
   marshaling mode, the separator convention, and the digest chain at
   once. Anything wrong in that stack would have produced a different
   string, not a plausible-looking one.

2. **[LOAD-BEARING] The module is importable at the path we assume.** →
   **PASS with a CATCH elsewhere.** Module path is
   `github.com/Vikasa2M/openits-models`, matching the repo, and it
   resolves and builds against Go 1.26. But `scripts/lint-boundary.sh`
   carried a comment asserting the module declares
   `github.com/openits/openits-models`. That was false. The lint itself
   was never affected (it greps the repo name, which covers both
   spellings), but the stated rationale was wrong — comment corrected.

3. **Enum numbering matches what the domain model assumes.** → PASS, with
   a wrinkle worth recording. `commonv1.FaultSeverity` is
   `INFO=0 WARNING=1 MINOR=2 MAJOR=3 CRITICAL=4` — **no `UNSPECIFIED`
   zero value**, so `model.FaultSeverity` maps 1:1 by value exactly as
   `sdk/model/enums.go` predicted. The wrinkle: a zero on the wire is a
   real INFO, not "unset", so absence is not expressible.
   `dmsv1.ErrorType` has the same shape (`SYNTAX=0`).

4. **Mode and kind leaves are strings, not Go enums.** → PASS.
   Identityrefs render as `module:identity-name` plain strings, and
   `DetectorReportDetector.Occupancy` is a string (YANG decimal64)
   against the domain's `OccupancyTenths uint16`.

5. **Go package names for the import block.** → `commonv1`,
   `signalcontrolv1`, `dmsv1`, `typesv1`.

## Catches folded in before coding

These were found in the same audit and are already reflected in the spec's
Revisions section and in the tracking issues, so they are not open:

- `ce-id` was specified as a no-op in the Plan 2 spec; upstream made a
  ULID derivation normative after that spec was written.
- `ce-source` entity-kind is not a passthrough of `DeviceKind`.
- `DMSDisplayStateChanged` is claimable and was wrongly deferred.
- `sequence`, `observed-by`, and `kind` are mandatory `event-header`
  leaves that the mapping table omitted.

## Still open (not blocking 2a)

- **`ModeStandby` has no controller-mode identity upstream.** The set is
  coordinated / free / flash / preempt / priority / manual / off.
  Resolution: do not claim a mode event whose value has no identity — let
  it drop loudly, consistent with the unknown-`DeviceKind` rule — and
  raise it upstream rather than guessing a near-neighbour.
- **`ce-source` segment order is contradictory upstream** (entity-kind
  first everywhere except the `ce-id-spec.md` test vector). Blocks the
  envelope slice, not the emitter mapping, because `ce-id` hashes the
  literal source bytes.
