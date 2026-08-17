# ADR 0014: Config is the trust boundary; boot fails on the unrecognized

**Status:** Accepted (2026-08-17). Records a rule that predates this ADR;
written when its only sources were `AGENTS.md`, `collector.yaml`, and two
design specs scheduled for deletion.

## Context
A cabinet collector runs unattended behind cellular NAT. A misconfiguration
that is accepted at boot does not fail at boot — it surfaces later as
unroutable events, mislabelled provenance, or silently wrong data in the
lake, discovered downstream, long after the cheap moment to catch it would
have been.

## Decision
Everything is validated at boot in `Config.validate` (`internal/config/config.go`),
and the collector refuses to start on anything it does not understand:

- **Unknown vendor/device-kind**: `reg.Known(d.Vendor, d.DeviceKind)` — a
  device with no registered adapter is a boot failure, not a silently-skipped
  device.
- **Tenant tokens that would corrupt subject grammar or the source URN**:
  `c.Tenant().Validate()` rejects dots, colons, wildcards, uppercase, and
  empty region/agency/agency_unit/site tokens, because the URN is
  colon-delimited and the subject is dot-delimited, so a bad token would
  silently change the shape of both instead of failing loudly
  (`internal/cloudevents/subject.go`).
- **A subject template that can never yield a static stream binding**:
  `tmpl.ValidateBindable()` — the structural check that a template's
  leftmost token doesn't vary per event, done at config time even though the
  exhaustive per-ce-type check needs the emitters and happens later in
  `app.Run` (`internal/subject/subject.go`).
- **A missing or malformed `collector_id`**: must match `deviceIDRe`, because
  it is published as `observed-by` on every event.
- Also enforced the same way: a missing `model_version`, zero devices, a
  duplicate device ID, a missing device ID, and a negative `poll_interval`.

Prefer boot-time failure over publish-time surprise.

## Consequences
This is the same strictness ADR 0012 already leaned on when it wrote "config
a new binary rejects bricks a cabinet that then needs a truck roll" — that
tension is real and is not resolved here, only inherited: strictness is
correct for data integrity and hazardous unattended. That is what makes
health-gated rollback (ADR 0012's readiness signal) non-optional rather than
a nice-to-have — the strictness makes the rollback guard more necessary, not
less. This ADR does not weaken the rule to make that tension smaller.

**One sanctioned exception**, not yet implemented: an absent broker at
startup is a transient, not a config error. `publish.Connect`
(`internal/publish/publish.go`) currently dials NATS and provisions streams
during boot, so a broker that is slow to come up makes the collector exit —
which under health-gated rollback reads as a failed update and triggers a
spurious revert. Tolerating this is planned as fleet-plan Phase 1
(`docs/plans/2026-08-09-fleet-deployment.md`), and is written down there as
"a narrow exception to 'config is the trust boundary' rather than a
weakening of it: bad config still fails hard, an absent peer retries." That
plan is the source this ADR should be read alongside for the exception's
scope; this ADR is the source for the rule the exception is narrow against.

## Alternatives considered
**Warn and continue** (rejected): the failure modes above are all
silent-and-downstream — a warning in a cabinet log nobody reads is
equivalent to no check at all.

**Validate lazily at first publish** (rejected): moves the failure from boot
to the first publish attempt, which is 3am and a process that is already
carrying data, rather than the moment before the collector claimed to be
ready.
