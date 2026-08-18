# Deploy a collector

*Stub. Fleet deployment is designed but not built.*

**Owner:** successor B — the fleet-deployment and management-surface work
described in `docs/specs/`, not part of this documentation effort.

## What's missing, plainly

Nothing in this repository updates a running collector, exposes a
readiness or liveness endpoint, or reports version and config drift to a
fleet control plane. Two specs describe that work in detail without it
having shipped yet:

- [`docs/specs/2026-08-09-fleet-deployment.md`](../specs/2026-08-09-fleet-deployment.md)
  — host mechanism versus fleet control plane, what's invariant across
  every open branch (a readiness signal, version and config-revision
  reporting, tolerating an absent broker at startup) versus what's
  genuinely undecided (who executes an update, who reverts a bad one).
- [`docs/specs/2026-08-09-management-surface-design.md`](../specs/2026-08-09-management-surface-design.md)
  — the local HTTP surface (`/readyz`, `/livez`, `/status`), the outbound
  fleet-observability events, and why the inbound direction is deferred
  rather than ruled out.

`docs/README.md` frames `specs/` as a staging tier for designs that
haven't shipped, not documentation of the running system — these two
specs are exactly that: real designs, no corresponding code yet.

## What exists today

A single-instance run, for development or a single cabinet by hand:

```bash
go run ./cmd/collector -config collector.yaml
```

Every `collector.yaml` field — type, default, validation — is documented
in [`docs/reference/configuration.md`](../reference/configuration.md).
There is no supervisor integration, update mechanism, or fleet-facing
surface beyond that; the collector boots, validates its config, and
starts polling, per
[`README.md`'s Status section](../../README.md#status).

This stub will be replaced with real operating instructions once
successor B lands a management surface to document.
