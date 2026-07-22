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
