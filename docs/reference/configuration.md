# Configuration reference

This is the reference for everything that configures a running collector:
every `collector.yaml` field and every command-line flag — types, defaults,
and the validation `Config.validate` (`internal/config/config.go`) actually
runs at boot. **Config is the trust boundary** — see
[`docs/reference/invariants.md`](invariants.md)'s "Config is the trust
boundary" row for why the collector refuses to start rather than warn.
`collector.yaml` itself keeps the *why*: the rationale for each field's
existence and shape. This document is the *what*: the exhaustive list of
valid values. Decoding is strict: a key not listed here, or a second
YAML document in the file, fails boot rather than being ignored.

## The command line

`collector.yaml` is not the whole configuration surface. Eight flags are
parsed by `cmd/collector/main.go`, and none of them has a YAML equivalent:
they describe where and how this process runs, not the tenant or device
inventory.

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `-config` | path | — | Path to `collector.yaml`. **Required**. |
| `-nats` | URL | `nats://127.0.0.1:4222` | The cabinet-local NATS server. Passed to `publish.Start`, which connects in the background: the collector boots and polls without it, queuing events until it is reachable ([ADR 0020](../adr/0020-broker-absence-is-latency.md)). A URL carrying credentials (`user:pass@`, `token@`) is refused, and so is a non-loopback broker that is not `tls://` ([ADR 0022](../adr/0022-transport-security-and-credentials.md)). |
| `-nats-creds` | path | — | A NATS `.creds` file (user JWT and nkey seed) to authenticate with. Must exist and grant nothing to other users (no `o` permission bits). The only way to give the collector a broker credential. |
| `-nats-ca` | path | — | The CA certificate a `tls://` broker is verified against, for a private CA. Without it the system roots are used. Verification is never skipped. |
| `-log-level` | `debug` \| `info` \| `warn` \| `error` | `info` | Minimum level written. Case-insensitive. |
| `-log-format` | `text` \| `json` | `text` | `text` is slog's `key=value` lines; `json` is one object per line, for a log shipper. |
| `-status-addr` | `host:port` | `127.0.0.1:9471` | The management surface (below). Must be loopback — `127.0.0.1`, `::1` or `localhost` — because it is unauthenticated; anything else is refused at boot. Empty disables it. |
| `-version` | bool | `false` | Print the build version and exit. Set at build time via `-ldflags "-X main.version=..."`; without a stamp it prints the module version Go recorded — the tag for `go install …@vX.Y.Z`, a VCS pseudo-version for a build in a git checkout — and `dev` only when there is none. |

Logs go to stderr; `-version` goes to stdout. Exit codes separate who has to
fix the problem: **2** means the command line itself is wrong (a missing
`-config`, an unknown flag, an unrecognized `-log-level` or
`-log-format` value, a non-loopback `-status-addr`, credentials in `-nats`,
a plaintext non-loopback `-nats`, or a missing or other-readable
`-nats-creds` — rejected at boot,
never defaulted); **1** means the
config or the runtime failed; **0** is a clean shutdown or `-version`.

`-nats` defaults to loopback because the collector and the broker are
expected to be co-resident in the cabinet (ADR 0011's cabinet-local
JetStream). There is no config-file field for it deliberately: the broker
address is a property of where the process is running, not of the tenant or
device inventory `collector.yaml` describes, and a cabinet moving its broker
should not require a config revision that fleet tooling would then have to
reconcile.

An unreachable `-nats` is therefore not a startup failure. It is the one
sanctioned exception to fail-fast (ADR 0014): the address is still
validated as a URL at boot, but a broker that is down, or simply slower to
start than the collector, is waited for rather than treated as bad config.

### The management surface

With `-status-addr` set (the default), the collector serves three read-only
`GET` endpoints on loopback
([management-surface spec §1](../specs/2026-08-09-management-surface-design.md#1-local-surface)):

| Endpoint | Answers | Status |
|---|---|---|
| `/livez` | Is the process responsive? For restarts only. | `200` |
| `/readyz` | Has this version proven itself: at least one successful device poll? **Latches** — a later outage never makes it unready — and does not depend on the broker. For gating a rollout. | `200` ready, `503` not yet |
| `/status` | JSON for a technician: `version`, `config_revision` (a SHA-256 prefix of the config file's bytes), `boot_id`, `uptime_s`, `ready`, `broker` (`connected`, `queued`), the collector's `counters`, and per device its last success, last error and consecutive failures. | `200` |

Requests whose `Host` header is not `localhost` or a loopback IP are
refused with `421`. The loopback bind keeps other machines out; the `Host`
check also keeps out DNS rebinding, where a page in a browser on the same
host points its own domain at 127.0.0.1.

A supervisor's start-up grace must exceed twice the longest `poll_interval`:
the first poll comes no sooner than one interval plus start jitter.

## Fields

| Field | Type | Required | Default | Validation |
|---|---|---|---|---|
| `region` | string | Yes | — | Must match `^[a-z0-9][a-z0-9-]*$` (`internal/cloudevents/subject.go` `tokenRe`, checked via `Tenant.Validate`). |
| `agency` | string | Yes | — | Same pattern as `region`. |
| `agency_unit` | string | Yes | — | Same pattern as `region`. |
| `site` | string | Yes | — | Same pattern as `region`. Not one of the URN's identity-triple segments, but it *is* the URN's `<id>` segment on device-less events — see the note below before treating it as id-safe. Also used for stream naming and health context. |
| `collector_id` | string | Yes | — | Must match `^[a-zA-Z0-9_-]+$` (`deviceIDRe` in `internal/config/config.go`, checked by `Config.validate`). Published as `observed-by` on every event. |
| `model_version` | string | Yes | — | Selects the catalog emitter (ADR 0005). Must be one of `wire.SupportedModelVersions` (`internal/wire/versions.go`); this build supports only `openits/v1`, and anything else fails boot. A second value appears only when a second versioned emitter package is compiled in ([ADR 0018](../adr/0018-tagged-model-pins.md)). |
| `subject` | object | No | Omitted entirely | No validation of its own; see `subject.template` and `subject.vars`. Omitting the block reproduces the default seven-token grammar exactly. |
| `subject.template` | string | No | `"{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}"` (`internal/subject/subject.go` `DefaultTemplate`) | Must parse, and must be able to yield a static stream binding — its leftmost token must not vary per event (`tmpl.ValidateBindable()`, called from `Config.validate`). A service-first or flat template fails this check. |
| `subject.vars` | map[string]string | No | empty | Keys may not redefine a reserved name (`namespace`, `service`, `event`, `version`, `region`, `agency`, `agency_unit`, `site`, `device_id`) — enforced in `subject.New` against `reservedNames` (`internal/subject/subject.go`). |
| `devices` | list of objects | Yes | — | At least one device is required (`Config.validate`). |
| `devices.id` | string | Yes (per device) | — | Must be non-empty and unique across all devices (`Config.validate`'s per-device loop). |
| `devices.vendor` | string | Yes (per device) | — | Combined with `device_kind`, must resolve to a registered adapter — registry key `"<vendor>-<device_kind>"` (`reg.Known`, called from `Config.validate`'s per-device loop). |
| `devices.device_kind` | string | Yes (per device) | — | Validated together with `vendor`, above. |
| `heartbeat_interval` | duration | No | `300s` | How often the collector publishes `openits-collector.instance.heartbeat.v1` ([ADR 0019](../adr/0019-collector-self-health.md) clause 3). At the default, about 6.5 MB per cabinet per month. Below `10s` fails boot: that is a debugging cadence, not a fleet-health one. Beats land on a stable, per-`collector_id` offset within the interval, so a fleet spreads and each collector's beat stays regular across restarts. |
| `log_ship_level` | `debug` \| `info` \| `warn` \| `error` | No | `warn` | The lowest level of the collector's own log records also published as `openits-collector.instance.log.v1` ([ADR 0019](../adr/0019-collector-self-health.md) clause 10). At most 20 per `heartbeat_interval`; the excess is counted as `logs_suppressed` on the heartbeat. Independent of `-log-level`, which governs only the local log. |
| `devices.poll_interval` | duration | No | `5s` | Must not be negative; a zero value is defaulted to `5s` by `Config.validate` rather than treated as "poll as fast as possible". |
| `devices.connection` | map[string]any | Yes (per device, shape adapter-defined) | — | Opaque to the core; parsed and validated by the selected adapter, not by `Config.validate`. Adapters reject unknown keys and wrong-typed values here too — for `ntcip`, only `snmp.address`, `snmp.community` and `snmp.version` are accepted, all strings (quote a numeric community: `community: "1234"`). For `cameleon` / `acs`, the block is `srtp.address` (required `host:18245`), optional `srtp.timeout`, `dsw.start` and `dsw.length` (the `%R` word block), `gates` (each `id`, `dsw` inside that block, and `kind` of `warning`, `barrier`, or `gate`), an optional `cabinet` with `lf_fault`, `sp_fault`, `door`, and `gate_estop` (only bit 0 of each word), and an optional `plc` block: `cabinet_id` (1..63), `program_crc` and `config_crc` (32-bit hex strings), and `watchdog_stall` (duration, default `5s`, `0s` disables). `%R90`–`%R94` must sit inside `dsw` when `plc` is set. |

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
trailing `<id>` segment whenever the event has no device, which means the
collector's own `instance.*` events (`started`, `stopping`, `heartbeat`):

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
