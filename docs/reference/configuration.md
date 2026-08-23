# Configuration reference

This is the reference for everything that configures a running collector:
every `collector.yaml` field and every command-line flag — types, defaults,
and the validation `Config.validate` (`internal/config/config.go`) actually
runs at boot. **Config is the trust boundary** — see
[`docs/reference/invariants.md`](invariants.md)'s "Config is the trust
boundary" row for why the collector refuses to start rather than warn.
`collector.yaml` itself keeps the *why*: the rationale for each field's
existence and shape. This document is the *what*: the exhaustive list of
valid values.

## The command line

`collector.yaml` is not the whole configuration surface. Three flags are
parsed by `cmd/collector/main.go`, and one of them — the broker address —
has no YAML equivalent at all:

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `-config` | path | — | Path to `collector.yaml`. **Required**; the binary exits 2 without it. |
| `-nats` | URL | `nats://127.0.0.1:4222` | The cabinet-local NATS server. Passed straight to `publish.Connect`, which dials it and provisions every stream during boot. |
| `-version` | bool | `false` | Print the build version and exit. Set at build time via `-ldflags "-X main.version=..."`; an unstamped build prints `dev`. |

`-nats` defaults to loopback because the collector and the broker are
expected to be co-resident in the cabinet (ADR 0011's cabinet-local
JetStream). There is no config-file field for it deliberately: the broker
address is a property of where the process is running, not of the tenant or
device inventory `collector.yaml` describes, and a cabinet moving its broker
should not require a config revision that fleet tooling would then have to
reconcile.

Note the boot ordering consequence: because `publish.Connect` dials during
boot, an unreachable `-nats` currently fails startup rather than being
retried. ADR 0014 says that should be the one sanctioned exception to
fail-fast and it is not implemented yet — see
[the gap list](../README.md#known-gaps-and-successor-work).

## Fields

| Field | Type | Required | Default | Validation |
|---|---|---|---|---|
| `region` | string | Yes | — | Must match `^[a-z0-9][a-z0-9-]*$` (`internal/cloudevents/subject.go` `tokenRe`, checked via `Tenant.Validate`). |
| `agency` | string | Yes | — | Same pattern as `region`. |
| `agency_unit` | string | Yes | — | Same pattern as `region`. |
| `site` | string | Yes | — | Same pattern as `region`. Not one of the URN's identity-triple segments, but it *is* the URN's `<id>` segment on device-less events — see the note below before treating it as id-safe. Also used for stream naming and health context. |
| `collector_id` | string | Yes | — | Must match `^[a-zA-Z0-9_-]+$` (`deviceIDRe` in `internal/config/config.go`, checked by `Config.validate`). Published as `observed-by` on every event. |
| `model_version` | string | Yes | — | Must be non-empty (`Config.validate`). No format beyond that — the value selects nothing today, and versioned emitter packages have not started (ADR 0005, [ADR 0018](../adr/0018-tagged-model-pins.md)). Tracked in [the gap list](../README.md#known-gaps-and-successor-work). |
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

`region`, `agency` and `agency_unit` are the profile's identity triple and
appear in every CE-source URN. All four compose the default subject grammar.

**`site` is a partial exception worth stating precisely**, because the
half-true version — "site is not in the URN" — is the one that gets someone
into trouble. It is not one of the triple's segments. But `SourceFor`
(`internal/cloudevents/subject.go`) falls back to `site` as the URN's
trailing `<id>` segment whenever the event has no device, which today means
every `collector-started` health event:

```
urn:openits:collector:<region>:<agency>:<agency_unit>:<site>
```

That matters beyond cosmetics. `ce-source`'s literal bytes are the first
field hashed into `ce-id`
([the derivation](../explanation/wire-boundary.md#ce-id-a-deterministic-ulid-not-a-content-hash)),
so changing `site` changes the `ce-id` of every collector-health event the
cabinet publishes afterwards. Device events are unaffected. Treat all four
tokens as identity, not as labels.

See
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
