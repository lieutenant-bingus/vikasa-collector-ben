# Runtime contract

What the `vikasa-collector` binary promises to whatever runs it: a systemd
unit, a container, a package, or a fleet agent. The project ships the binary
and this contract. How you package and supervise it is your choice
([ADR 0023](../adr/0023-ship-a-binary-and-a-runtime-contract.md)). The
[deploy guide](../how-to/deploy-a-collector.md) is one worked example,
systemd on a cabinet host, not the only supported way.

Anything here changing is a breaking change for packagers, and the release
notes call it out.

## The artifact

- One static executable, built with `CGO_ENABLED=0`, for `linux/amd64` and
  `linux/arm64`. It needs no libc, interpreter or shared libraries.
- Each release publishes `vikasa-collector_<version>_linux_<arch>.tar.gz`
  and a `checksums.txt` to verify them against. The archive also holds an
  example config and the reference systemd unit.
- `vikasa-collector -version` prints the build version on stdout and
  exits 0.

## Inputs

| Input | How | Notes |
|---|---|---|
| Configuration | `-config <path>` (required) | A secret: it holds SNMP community strings. Read once at boot; there is no reload, so restart to apply a change. Every field is in [`configuration.md`](configuration.md). |
| Command line | `-nats`, `-nats-creds`, `-nats-ca`, `-log-level`, `-log-format`, `-status-addr` | Defaults and validation are in [`configuration.md`](configuration.md#the-command-line). |
| Broker credential | `-nats-creds <path>` (optional) | A NATS `.creds` file. Boot fails if other users can read it. |
| Broker CA | `-nats-ca <path>` (optional) | For a `tls://` broker signed by a private CA. |
| Environment | `NOTIFY_SOCKET`, `WATCHDOG_USEC`, `WATCHDOG_PID` (all optional) | The supervisor's `sd_notify` socket and watchdog, set by systemd for a `Type=notify` unit or by podman with `--sdnotify=container`. The collector reads no other environment variable. A `NOTIFY_SOCKET` that is not an absolute path or an `@abstract` name fails boot with exit 2. |

One other invocation exists, and it is not for a supervisor:
`vikasa-collector record-fixture` polls one configured device once and
writes a test fixture for its adapter, for a person at the device. It
connects to no broker, and writes only the file its `-out` names. See
[`record-fixtures-from-a-device.md`](../how-to/record-fixtures-from-a-device.md).

## Files and privileges

- **Reads** the config, and the creds and CA files when given. **Writes
  nothing** to disk. Previous device state lives in the broker's
  `VIKASA_SYNTH_STATE` key-value bucket, so the filesystem can be read-only
  and the process can be replaced freely.
- **Needs no privileges or capabilities.** Run it as an unprivileged user
  that can read the config and creds files, and nothing else.

## Network

| Direction | What | Default |
|---|---|---|
| Outbound UDP | SNMP to each device in the config (`ntcip` adapters) | port from the device's `connection.snmp.address` |
| Outbound TCP | The NATS server with JetStream | `nats://127.0.0.1:4222`; a non-loopback broker must be `tls://` |
| Listen, loopback only | The management surface | `127.0.0.1:9471`; `-status-addr ""` disables it |

The broker does not have to be up first. The collector boots without it,
holds events in a bounded queue, and publishes once it connects
([ADR 0020](../adr/0020-broker-absence-is-latency.md)). The broker user needs
the grant listed in the
[deploy guide](../how-to/deploy-a-collector.md#an-authenticated-or-remote-broker).

## Output

- **Logs** go to stderr: `-log-format text` (the default) or `json`, one
  record per line. When the collector exits with an error, that error is
  the last line on stderr.
- **Events** go to the broker only. Warnings and errors are also shipped
  there as `instance.log` events (`log_ship_level` in the config).

## Health

| Endpoint | Answers | Use it for |
|---|---|---|
| `GET /livez` | 200 while the process is responsive | restarting a stuck process |
| `GET /readyz` | 200 once any device has been polled successfully, then always (it latches); 503 before | judging a rollout or rollback |
| `GET /status` | JSON: version, config revision, broker state, counters, per-device state | people and dashboards |

Only requests addressed to a loopback host are answered. Readiness does not
depend on the broker.

Under a supervisor that sets `NOTIFY_SOCKET`, the same signals arrive over
`sd_notify`:

- `READY=1` at the moment `/readyz` latches;
- `STOPPING=1` when shutdown begins;
- with `WATCHDOG_USEC` set, `WATCHDOG=1` at half the watchdog timeout.

The watchdog ping is sent while every device's poll loop is completing
rounds. A device that is down does not stop it, because its loop still turns,
so only a hung collector goes quiet and is restarted. The collector also announces itself on the bus
(`instance.started`, then a periodic `instance.heartbeat`), carrying its
version and config revision.

## Lifecycle

- **Start-up:** config and flags are validated before anything runs. A
  value the collector does not understand is refused, never defaulted
  ([ADR 0014](../adr/0014-config-is-the-trust-boundary.md)). Under
  `sd_notify` the unit stays "activating" until a device answers, and the
  collector polls and reports normally meanwhile. Give it an unbounded
  start timeout (`TimeoutStartSec=infinity`), or a cabinet whose devices
  are all down at boot is killed and restarted instead of reporting them
  unreachable. The exception is a rollout tool that rolls back on a failed
  start, such as `podman auto-update`: it needs a finite timeout to see a
  failure. The cost is that a cabinet whose devices are all down rolls back
  a good version.
- **Stop:** SIGTERM or SIGINT starts a bounded shutdown:
  1. polling stops;
  2. adapters are closed (up to 2 s);
  3. device state is written to the broker (up to 2 s);
  4. queued events are published (up to 5 s);
  5. the process exits 0.

  Allow at least 30 seconds before a forced kill. A second signal during
  shutdown kills the process at once. Events still queued are counted in
  `dropped_at_shutdown` and logged.
- **Exit codes:**

  | Code | Meaning | Who fixes it |
  |---|---|---|
  | 0 | clean shutdown, `-version` or `-help` | — |
  | 1 | the config is invalid, or the collector failed at runtime | the config's owner |
  | 2 | the command line is wrong | whoever wrote the unit or manifest |

  The full list of causes is in
  [`configuration.md`](configuration.md#the-command-line).
- **Restart:** it is always safe. State survives in the broker, so a fault
  that stood before a restart is not raised again.

## Updates

The collector never updates itself
([ADR 0012](../adr/0012-host-executed-updates.md)). Replace the binary and
restart it. Gate the rollout on `/readyz` (or the unit reaching "active"
under `sd_notify`) and on the new `version` in `/status`. Roll back the same
way.

## One per cabinet

Run one collector per cabinet broker. Its state bucket has a fixed name
([ADR 0017](../adr/0017-durable-synth-state.md)), so two collectors on one
broker would overwrite each other's state.
