# Deploy a collector

How to run the collector on one cabinet's Linux host as a systemd service,
check that it is working, and upgrade or roll it back. Fleet-scale rollout —
a control plane that executes updates and reverts bad ones — is designed in
[`docs/specs/2026-08-09-fleet-deployment.md`](../specs/2026-08-09-fleet-deployment.md)
but not built; everything here is what that plane would drive on each host.

## What you need

- **A Linux host in the cabinet**, `amd64` or `arm64` — the fleet spec's
  Pi-class ARM boards and small x86 industrial PCs. The binary is static, so
  no libc or runtime is required, and the systemd unit assumes systemd.
- **A NATS server with JetStream on the same host**, listening on loopback
  (the collector's default `-nats` is `nats://127.0.0.1:4222`). The
  collector provisions its own streams and its synth-state bucket; it does
  not need the broker to be up first, and queues until it is
  ([ADR 0020](../adr/0020-broker-absence-is-latency.md)). For a cabinet
  that leaf-nodes to a hub, run it under its own JetStream domain so cabinet
  state never federates upward
  ([ADR 0017](../adr/0017-durable-synth-state.md)) — that is broker
  configuration, not collector configuration.
- **Network reach to the devices** in your config (SNMP on UDP 161 for the
  `ntcip` adapters). The collector only reads — it never issues an SNMP SET
  — so give it a **read-only community**, and where a device supports SNMP
  views, one limited to the objects its adapter reads. v1/v2c communities
  cross the cabinet network in plaintext
  ([ADR 0022](../adr/0022-transport-security-and-credentials.md)).

## 1. Get the release

Each release on GitHub carries
`vikasa-collector_<version>_linux_<arch>.tar.gz` and `checksums.txt`.
Download both, and verify before unpacking:

```bash
VERSION=0.1.0 ARCH=arm64   # or amd64
curl -LO https://github.com/Vikasa2M/vikasa-collector/releases/download/v${VERSION}/vikasa-collector_${VERSION}_linux_${ARCH}.tar.gz
curl -LO https://github.com/Vikasa2M/vikasa-collector/releases/download/v${VERSION}/checksums.txt
sha256sum --ignore-missing -c checksums.txt
tar -xzf vikasa-collector_${VERSION}_linux_${ARCH}.tar.gz
```

The archive holds the `vikasa-collector` binary, `collector.yaml.example`,
the systemd unit `vikasa-collector.service`, and the license files.

**Until the first release is published**, build the identical archives
yourself with `make release-snapshot` (goreleaser v2 on your PATH); they
land in `dist/`.

## 2. Install

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin vikasa-collector
sudo install -m 0755 vikasa-collector /usr/local/bin/vikasa-collector
sudo install -d -m 0755 /etc/vikasa-collector
sudo install -m 0640 -o root -g vikasa-collector collector.yaml.example /etc/vikasa-collector/collector.yaml
sudo install -m 0644 vikasa-collector.service /etc/systemd/system/
```

The config holds SNMP community strings, so it is a secret: `0640
root:vikasa-collector` makes it readable by root and the service and nobody
else. Keep it that way when you edit it.

Edit `/etc/vikasa-collector/collector.yaml` for this cabinet — tenant
identity, `collector_id`, `site`, and the devices. Every field, its default
and its validation is in
[`reference/configuration.md`](../reference/configuration.md). The
collector refuses to start on anything it does not understand — a misspelled
key, an unsupported `model_version`, a non-loopback `-status-addr` — and
exits with the reason, so a bad config shows up immediately in
`systemctl status` and the journal rather than as wrong data later.

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now vikasa-collector
systemctl status vikasa-collector   # a config error shows here, with the reason
```

The unit runs the collector as the unprivileged `vikasa-collector` user with
everything it does not need denied (`systemd-analyze security` rates it 1.3,
"OK").
It logs JSON to the journal; adjust `ExecStart` for `-log-level` or a
different `-nats` or `-status-addr`.

### An authenticated or remote broker

The default — an unauthenticated broker on loopback — needs nothing here.
When the broker requires a credential, or is on another host, give the
collector a NATS `.creds` file, and for another host a `tls://` URL
([ADR 0022](../adr/0022-transport-security-and-credentials.md)):

```bash
sudo install -m 0640 -o root -g vikasa-collector collector.creds /etc/vikasa-collector/collector.creds
sudo systemctl edit vikasa-collector   # then, in the override:
```

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/vikasa-collector -config /etc/vikasa-collector/collector.yaml -log-format json \
  -nats tls://broker.cabinet.lan:4222 \
  -nats-creds /etc/vikasa-collector/collector.creds \
  -nats-ca /etc/vikasa-collector/fleet-ca.pem
```

The collector refuses to start, with the reason, on credentials written into
`-nats` (they would be visible to `ps`), on a non-loopback broker that is not
`tls://`, and on a `.creds` file readable by other users. `-nats-ca` is only
for a private CA; without it the system roots verify the broker. Nothing
turns verification off.

**Grant the collector's user exactly this**, shown for tenant
`us-ga`/`metro`/`d01` under the default subject template — substitute your
own `region`, `agency` and `agency_unit`:

| Direction | Subjects | Why |
|---|---|---|
| publish | `openits.us-ga.metro.d01.>` | catalog events |
| publish | `openits-collector.us-ga.metro.d01.>` | the collector's own health and lifecycle events |
| publish | `$JS.API.INFO` | JetStream account check, made when the state bucket is opened |
| publish | `$JS.API.STREAM.CREATE.<stream>`, `$JS.API.STREAM.UPDATE.<stream>` for each of `OPENITS-US-GA-METRO-D01`, `OPENITS-COLLECTOR-US-GA-METRO-D01` and `KV_VIKASA_SYNTH_STATE` | the collector provisions its two streams and its state bucket |
| publish | `$KV.VIKASA_SYNTH_STATE.>` | writing synth state ([ADR 0017](../adr/0017-durable-synth-state.md)) |
| publish | `$JS.API.CONSUMER.CREATE.KV_VIKASA_SYNTH_STATE.>`, `$JS.API.CONSUMER.DELETE.KV_VIKASA_SYNTH_STATE.>`, `$JS.API.DIRECT.GET.KV_VIKASA_SYNTH_STATE.>` | reading synth state back at boot |
| subscribe | `_INBOX.>` | replies to the above; nothing else |

Stream names are the subject root upper-cased with `.` as `-`; a custom
`subject.template` changes them, and the collector logs the streams it
provisions at startup. The set is minimal as well as sufficient: a test runs
the collector under exactly this grant, and each line removed makes it fail.
With `nsc`:

```bash
nsc add user vikasa-collector \
  --allow-pub 'openits.us-ga.metro.d01.>,openits-collector.us-ga.metro.d01.>' \
  --allow-pub '$JS.API.INFO,$KV.VIKASA_SYNTH_STATE.>' \
  --allow-pub '$JS.API.STREAM.CREATE.OPENITS-US-GA-METRO-D01,$JS.API.STREAM.UPDATE.OPENITS-US-GA-METRO-D01' \
  --allow-pub '$JS.API.STREAM.CREATE.OPENITS-COLLECTOR-US-GA-METRO-D01,$JS.API.STREAM.UPDATE.OPENITS-COLLECTOR-US-GA-METRO-D01' \
  --allow-pub '$JS.API.STREAM.CREATE.KV_VIKASA_SYNTH_STATE,$JS.API.STREAM.UPDATE.KV_VIKASA_SYNTH_STATE' \
  --allow-pub '$JS.API.CONSUMER.CREATE.KV_VIKASA_SYNTH_STATE.>,$JS.API.CONSUMER.DELETE.KV_VIKASA_SYNTH_STATE.>' \
  --allow-pub '$JS.API.DIRECT.GET.KV_VIKASA_SYNTH_STATE.>' \
  --allow-sub '_INBOX.>'
```

Leave the subscribe list explicit: an empty one means "everything" to NATS.
A grant that is too tight shows in the journal as `broker refused an
operation`, naming the subject.

## 3. Check it is working

The loopback management surface answers three questions
([reference](../reference/configuration.md#the-management-surface)):

```bash
curl -s 127.0.0.1:9471/livez     # is the process responsive?
curl -s 127.0.0.1:9471/readyz    # has it polled a device successfully? (latches)
curl -s 127.0.0.1:9471/status    # versions, broker, counters, per-device state
journalctl -u vikasa-collector -f
```

`/readyz` returning `503` with every device in `/status` showing
`consecutive_failures` climbing is the classic misconfiguration — the
collector runs fine and reaches nothing. Allow at least twice the longest
`poll_interval` before judging: the first poll comes one interval plus start
jitter after boot.

Off the box, the collector announces itself on
`openits-collector.<region>.<agency>.<agency_unit>.instance.<site>.started`
and then heartbeats every `heartbeat_interval` on `…instance.<site>.heartbeat`
([ADR 0019](../adr/0019-collector-self-health.md)).

## 4. Upgrade and roll back

Replace the binary and restart:

```bash
sudo install -m 0755 vikasa-collector /usr/local/bin/vikasa-collector
sudo systemctl restart vikasa-collector
```

A restart is safe to do at any time:

- **Nothing standing is re-announced.** The engine's previous state lives in
  the broker's `VIKASA_SYNTH_STATE` bucket, so a fault that stood before the
  restart is not raised again, and a change that happened during it is
  reported once ([consumer contract](../reference/consumer-contract.md#restart-behaviour)).
- **Nothing queued is silently lost.** A clean stop drains the publish queue
  for up to five seconds; anything left is counted in `dropped_at_shutdown`.

Rolling back is the same operation with the previous binary. Gate either on
`/readyz` returning `200` within your grace period, and on the new `version`
in `/status`.

## What is not here yet

- **Automatic updates and rollback across a fleet** — the control plane in
  the fleet-deployment spec. The collector never updates itself
  ([ADR 0012](../adr/0012-host-executed-updates.md)).
- **Expected-version drift reporting** — `/status` and the heartbeat report
  the running version and `config_revision`, but nothing yet compares them
  with what a cabinet *should* be running.
- **`sd_notify`** — the unit is `Type=simple`; readiness is `/readyz`.
- **Packages and a container image** — releases ship tarballs; `.deb`/`.rpm`
  and an image for the fleet spec's podman path are not built.
