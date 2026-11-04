# Configuration reference

Everything that configures a running collector: every `collector.yaml`
field and every command-line flag, with its type, default, and the check
that runs at boot. `collector.yaml` itself explains *why* each field
exists; this page lists *what* is valid.

The collector refuses to start on a value it does not accept, rather than
warning and carrying on — see [config is the trust
boundary](invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized).
Decoding is strict too: a key not listed here, or a second YAML document
in the file, fails boot instead of being ignored.

## The command line

Eight flags, parsed by `cmd/collector/main.go`. None has a YAML
equivalent: they describe where and how this process runs, not the tenant
or the devices.

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `-config` | path | — | Path to `collector.yaml`. **Required**. |
| `-nats` | URL, or a comma-separated list of URLs | `nats://127.0.0.1:4222` | The cabinet-local NATS server. The collector connects in the background: it boots and polls without a broker, queuing events until one is reachable ([ADR 0020](../adr/0020-broker-absence-is-latency.md)). Each URL must parse; a URL carrying credentials (`user:pass@`, `token@`) is refused, and so is a non-loopback URL that is not `tls://` ([ADR 0022](../adr/0022-transport-security-and-credentials.md)). |
| `-nats-creds` | path | — | A NATS `.creds` file (user JWT and nkey seed) to authenticate with — the only way to give the collector a broker credential. Must exist and grant nothing to other users (no `o` permission bits; install it `0640` or stricter). |
| `-nats-ca` | path | — | The CA certificate a `tls://` broker is verified against, for a private CA. Must exist. Without it the system roots are used. Verification is never skipped. |
| `-log-level` | `debug` \| `info` \| `warn` \| `error` | `info` | Minimum level written to the local log. Case-insensitive. |
| `-log-format` | `text` \| `json` | `text` | `text` is slog's `key=value` lines; `json` is one object per line, for a log shipper. |
| `-status-addr` | `host:port` | `127.0.0.1:9471` | The management surface (below). The host must be loopback — `127.0.0.1` (any `127.x`), `::1` or `localhost` — because the surface is unauthenticated; the port must be 1–65535. Empty disables the surface. |
| `-version` | bool | `false` | Print the build version to stdout and exit. Release builds stamp it with `-ldflags "-X main.version=..."`; an unstamped build prints the module version Go recorded (the tag for `go install …@vX.Y.Z`, a pseudo-version for a build in a git checkout), and `dev` only when there is none. |

Logs go to stderr.

### Exit codes

The code tells a supervisor who has to fix the problem.

| Code | Meaning |
|---|---|
| `0` | Clean shutdown (SIGINT or SIGTERM), `-version`, or `-help`. |
| `1` | The config file or the runtime failed: a `collector.yaml` that does not load or validate, an adapter that cannot be built, a subject template that cannot route some ce-type, or a `-status-addr` port already in use. |
| `2` | The command line is wrong: an unknown flag, a missing `-config`, an unrecognized `-log-level` or `-log-format`, a non-loopback or malformed `-status-addr`, a malformed `-nats`, credentials in `-nats`, a plaintext non-loopback `-nats`, a missing or other-readable `-nats-creds`, or a missing `-nats-ca`. |

A second SIGINT or SIGTERM during shutdown kills the process at once,
for an operator who will not wait out the drain.

### Why the broker address is a flag

`-nats` defaults to loopback because the collector and its broker share
the cabinet ([ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md)'s
cabinet-local JetStream). It has no config-file field on purpose: the
broker's address is a property of the host, not of the tenant or the
devices, and moving a cabinet's broker should not need a config revision
that fleet tooling then has to reconcile.

An unreachable broker is therefore not a startup failure. It is the one
exception to fail-fast that [ADR
0014](../adr/0014-config-is-the-trust-boundary.md) allows: the URL is
still checked at boot, but a broker that is down, or slower to start than
the collector, is waited for rather than treated as bad config.

### The management surface

With `-status-addr` set (the default), the collector serves three
read-only endpoints on loopback (`internal/manage`;
[management-surface spec §1](../specs/2026-08-09-management-surface-design.md#1-local-surface)).
Each answers `GET` only.

| Endpoint | Answers | Status |
|---|---|---|
| `/livez` | Is the process responsive? Use it to restart, never to judge a rollout. | `200` |
| `/readyz` | Has this version proven itself — has at least one device poll succeeded? **Latches**: once ready, a later outage never makes it unready. Does not depend on the broker. Use it to gate a rollout. | `200` ready, `503` not yet |
| `/status` | A JSON document for a technician (below). | `200` |

`/status` carries:

- `version`, `config_revision` (the first 12 hex characters of the SHA-256
  of the config file's bytes), `boot_id` (random per process),
  `started_at`, `uptime_s`, `ready`;
- `broker`: `connected` and `queued` (events waiting to be published);
- `counters`: the collector's cumulative counters since boot — the same
  set the heartbeat carries, listed in
  [`asyncapi.yaml`](../../asyncapi.yaml)'s `instance.heartbeat` message;
- `devices`: per configured device, `id`, `last_success`, `last_error`,
  `last_error_at` and `consecutive_failures`.

A request whose `Host` header is not `localhost` or a loopback IP is
refused with `421`. The loopback bind keeps other machines out; the `Host`
check also stops DNS rebinding, where a web page open in a browser on the
same host points its own domain at 127.0.0.1.

A supervisor's start-up grace period must exceed twice the longest
`poll_interval`: the first poll comes no sooner than one interval plus
start jitter.

## Fields

| Field | Type | Required | Default | Validation |
|---|---|---|---|---|
| `region` | string | Yes | — | Must match `^[a-z0-9][a-z0-9-]*$` (`tokenRe` in `internal/cloudevents/subject.go`, checked by `Tenant.Validate`). |
| `agency` | string | Yes | — | Same pattern as `region`. |
| `agency_unit` | string | Yes | — | Same pattern as `region`. |
| `site` | string | Yes | — | Same pattern as `region`. Not part of the identity triple, but it stands in for the device id on the collector's own events, in both the subject and `ce-source` — see the note below. |
| `collector_id` | string | Yes | — | Must match `^[a-zA-Z0-9_-]+$` (`deviceIDRe` in `internal/config/config.go`). Published as `observed-by` on every catalog event and as `collector_id` on the collector's own events. |
| `model_version` | string | Yes | — | Selects the catalog emitter ([ADR 0005](../adr/0005-one-catalog-version-per-instance.md)). Must be one of `wire.SupportedModelVersions` (`internal/wire/versions.go`). This build supports only `openits/v1`; anything else fails boot. |
| `subject` | object | No | omitted | Holds `subject.template` and `subject.vars`. Omitting it gives the default seven-token grammar. |
| `subject.template` | string | No | `"{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}"` (`DefaultTemplate` in `internal/subject/subject.go`) | Dot-separated tokens, each either a literal or exactly one whole-token placeholder. Placeholders that vary per event: `{namespace}`, `{service}`, `{event}`, `{version}`, `{device_id}`. Fixed for the process: `{region}`, `{agency}`, `{agency_unit}`, `{site}`, `{prefix}` (defaults to `openits`) and any key in `subject.vars`; an undefined placeholder fails boot. The template must yield a static stream binding, so it may not begin with a per-event placeholder other than `{namespace}` — one starting with `{service}` or `{device_id}` fails (`ValidateBindable`). At boot every ce-type the collector can emit is rendered for every device and must land inside its stream's binding (`ValidateCETypes`). |
| `subject.vars` | map[string]string | No | empty | Operator-defined fixed tokens. A key may not redefine a reserved name (`namespace`, `service`, `event`, `version`, `region`, `agency`, `agency_unit`, `site`, `device_id`); a value must be a legal NATS token (non-empty; no `.`, `*`, `>` or whitespace). |
| `heartbeat_interval` | duration | No | `300s` | How often the collector publishes `openits-collector.instance.heartbeat.v1` ([ADR 0019](../adr/0019-collector-self-health.md)). About 6.5 MB per cabinet per month at the default. Below `10s` fails boot: that is a debugging cadence, not a fleet-health one. Each collector beats at a stable offset within the interval derived from its `collector_id`, so a fleet's beats are spread out and each one's stays regular across restarts. |
| `log_ship_level` | `debug` \| `info` \| `warn` \| `error` | No | `warn` | The lowest level of the collector's own log records that are also published as `openits-collector.instance.log.v1` ([ADR 0019](../adr/0019-collector-self-health.md)). At most 20 per `heartbeat_interval`; the excess is counted as `logs_suppressed` on the heartbeat. Independent of `-log-level`, which governs only the local log. Any other value fails boot. |
| `devices` | list of objects | Yes | — | At least one device. |
| `devices.id` | string | Yes | — | Non-empty, unique across devices, and only letters, digits, `-` and `_` (`[a-zA-Z0-9_-]+`), whatever the subject template. It is the last segment of every `ce-source` URN, so a `:` would add a segment and a `.` would split a subject; both fail boot. |
| `devices.vendor` | string | Yes | — | With `device_kind`, must name a registered adapter: registry key `"<vendor>-<device_kind>"`. This build registers `ntcip-asc` and `ntcip-dms`. |
| `devices.device_kind` | string | Yes | — | Validated together with `vendor`, above. |
| `devices.poll_interval` | duration | No | `5s` | Must not be negative. Zero (or omitted) means `5s`, not "as fast as possible". |
| `devices.connection` | map | Yes (shape is adapter-defined) | — | Opaque to the core: the selected adapter parses and validates it when it is built. Adapters reject unknown keys and wrong-typed values. For `ntcip` it is one `snmp:` block with `address` (required, `host:port`), `community` (default `public`) and `version` (`v1`/`1` or `v2c`/`2c`; default `v2c`) — all strings, so quote a numeric value: `community: "1234"`. For `maxtime` it is one `http:` block: `base_url` (required; `http(s)://host[/prefix]`, the prefix being the MIB API's, default `/maxtime`, `/maxtime-cv` on the CV build; no credentials in it), `auth` (`basic` or `maxprofile`; required exactly when `username`/`password` are set), `username`, `password`, `timeout` (duration string, default `5s`), `insecure_skip_verify` (bool, default `false`), `mib_batch` (bool, default `true`: one GET per poll; `false` reads one object per GET), and `asclog`, a mapping whose presence also reads the high-resolution log ([ADR 0025](../adr/0025-event-reader-with-checkpointing.md)): `timezone` (required, IANA name of the controller clock's zone), `interval` (duration, default `15s`, at least `1s`), `path` (default `/v1/asclog/xml/full`, at the host root; some builds serve it under the MIB prefix), `since` (bool, default `true`: ask only for rows since the cursor), `enabled` (bool, default `true`). |

## Notes on non-obvious fields

### `collector_id`

`collector_id` has no default. Deriving it from `agency` or `site` would be
silently wrong the moment a cabinet runs two collectors, and the mistake
would surface as mislabelled provenance in the data lake rather than as a
failure to start. See [ADR 0014](../adr/0014-config-is-the-trust-boundary.md).

### `region` / `agency` / `agency_unit` / `site`

`region`, `agency` and `agency_unit` are the profile's identity triple and
appear in every `ce-source` URN. The first three also compose the default
subject grammar.

**`site` is a partial exception**, and the precise version matters. It is
not one of the triple's segments. But when an event has no device — the
collector's own `instance.*` events (`started`, `stopping`, `heartbeat`,
`log`) — `SourceFor` (`internal/cloudevents/subject.go`) uses `site` as the
URN's trailing id, and the subject template renders it in place of
`{device_id}`:

```
urn:openits:collector:<region>:<agency>:<agency_unit>:<site>
openits-collector.<region>.<agency>.<agency_unit>.instance.<site>.heartbeat
```

`ce-source`'s literal bytes are the first input hashed into `ce-id`
([the derivation](../explanation/wire-boundary.md#ce-id-a-deterministic-ulid-not-a-content-hash)),
so changing `site` changes the `ce-id` of every collector event published
afterwards. Device events are unaffected by `site`, but changing any of
the triple changes theirs. Treat all four as identity, not labels.

[ADR 0015](../adr/0015-ce-source-urn-scheme.md) defines the URN scheme;
[the subjects-vs-envelope
rule](invariants.md#subjects-are-operator-configurable-the-cloudevents-envelope-stays-canonical)
explains why neither is derived from the other.

### `subject`, `subject.template`, `subject.vars`

Subject grammar belongs to the operator.
[ADR 0009](../adr/0009-configurable-subject-templates.md) explains why it
is configurable at all, and
[ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md) why the default
is rooted at the namespace with one stream per root. The collector
provisions one JetStream stream per root, named from its binding: for the
default template and tenant `us-ga`/`metro`/`d01`, `OPENITS-US-GA-METRO-D01`
capturing `openits.us-ga.metro.d01.>` and
`OPENITS-COLLECTOR-US-GA-METRO-D01` capturing
`openits-collector.us-ga.metro.d01.>`. The names are logged at boot.

### `devices.vendor` / `devices.device_kind`

Together these select the adapter at boot. See
[ADR 0003](../adr/0003-stable-sdk-in-tree-adapters.md) for why `ntcip` is a
vendor in its own right (the generic, standards-only implementation)
rather than a special case.
