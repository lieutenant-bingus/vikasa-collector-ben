# Configuration reference

This is the field-by-field reference for `collector.yaml`: types, defaults,
and the validation `Config.validate` (`internal/config/config.go`) actually
runs at boot. **Config is the trust boundary** — see
[`docs/reference/invariants.md`](invariants.md)'s "Config is the trust
boundary" row for why the collector refuses to start rather than warn.
`collector.yaml` itself keeps the *why*: the rationale for each field's
existence and shape. This document is the *what*: the exhaustive list of
valid values.

## Fields

| Field | Type | Required | Default | Validation |
|---|---|---|---|---|
| `region` | string | Yes | — | Must match `^[a-z0-9][a-z0-9-]*$` (`internal/cloudevents/subject.go` `tokenRe`, checked via `Tenant.Validate`). |
| `agency` | string | Yes | — | Same pattern as `region`. |
| `agency_unit` | string | Yes | — | Same pattern as `region`. |
| `site` | string | Yes | — | Same pattern as `region`. Not part of the CE-source URN; used for stream naming and health context. |
| `collector_id` | string | Yes | — | Must match `^[a-zA-Z0-9_-]+$` (`deviceIDRe` in `internal/config/config.go`, checked by `Config.validate`). Published as `observed-by` on every event. |
| `model_version` | string | Yes | — | Must be non-empty (`Config.validate`). No format beyond that — selecting between catalog versions is not yet wired (ADR 0005, ADR 0010). |
| `subject` | object | No | Omitted entirely | No validation of its own; see `subject.template` and `subject.vars`. Omitting the block reproduces the default seven-token grammar exactly. |
| `subject.template` | string | No | `"{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}"` (`internal/subject/subject.go` `DefaultTemplate`) | Must parse, and must be able to yield a static stream binding — its leftmost token must not vary per event (`tmpl.ValidateBindable()`, called from `Config.validate`). A service-first or flat template fails this check. |
| `subject.vars` | map[string]string | No | empty | Keys may not redefine a reserved name (`namespace`, `service`, `event`, `version`, `region`, `agency`, `agency_unit`, `site`, `device_id`) — enforced in `subject.New` against `reservedNames` (`internal/subject/subject.go`). |
| `devices` | list of objects | Yes | — | At least one device is required (`Config.validate`). |
| `devices.id` | string | Yes (per device) | — | Must be non-empty and unique across all devices (`Config.validate`'s per-device loop). |
| `devices.vendor` | string | Yes (per device) | — | Combined with `device_kind`, must resolve to a registered adapter — registry key `"<vendor>-<device_kind>"` (`reg.Known`, called from `Config.validate`'s per-device loop). |
| `devices.device_kind` | string | Yes (per device) | — | Validated together with `vendor`, above. |
| `devices.poll_interval` | duration | No | `5s` | Must not be negative; a zero value is defaulted to `5s` by `Config.validate` rather than treated as "poll as fast as possible". |
| `devices.connection` | map[string]any | Yes (per device, shape adapter-defined) | — | Opaque to the core; parsed and validated by the selected adapter, not by `Config.validate`. |

## Notes on non-obvious fields

### `collector_id`

`collector_id` has no default because deriving it from `agency`/`site` would
be silently wrong the moment a cabinet runs two collectors — the mistake
would surface as mislabelled provenance in the data lake, not a boot
failure. See [ADR 0014](../adr/0014-config-is-the-trust-boundary.md) and the
"Config is the trust boundary" row in
[`docs/reference/invariants.md`](invariants.md).

### `region` / `agency` / `agency_unit` / `site`

These four compose the CE-source URN (all but `site`) and the default
subject grammar (all four). See
[ADR 0015](../adr/0015-ce-source-urn-scheme.md) for the URN scheme itself,
and the "Subjects are operator-configurable; the CloudEvents envelope stays
canonical" row in [`docs/reference/invariants.md`](invariants.md) for why
neither is derived from the other.

### `subject`, `subject.template`, `subject.vars`

Subject grammar is operator-owned. See
[ADR 0009](../adr/0009-configurable-subject-templates.md) for why it is
configurable at all, and [ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md)
for the default grammar's namespace-rooted, one-stream-per-root shape. Both
are cited from the "Subjects are operator-configurable" row in
[`docs/reference/invariants.md`](invariants.md), which is the canonical
restatement of the enforced rule.

### `devices.vendor` / `devices.device_kind`

Together these select the adapter at boot — registry key `"<vendor>-<device_kind>"`.
See [ADR 0003](../adr/0003-stable-sdk-in-tree-adapters.md) for why `ntcip` is
itself a registrable vendor (the generic, standards-only implementation)
rather than a special case.
