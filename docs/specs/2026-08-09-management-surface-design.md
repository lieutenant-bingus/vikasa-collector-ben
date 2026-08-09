# Collector management surface

**Date:** 2026-08-09
**Status:** Draft
**Depends on:** ADR 0004 (pull-only), ADR 0007 (collector-owned health schema),
ADR 0011 (namespace-rooted subjects), ADR 0012 (host-executed updates)
**Context:** `docs/plans/2026-08-09-fleet-deployment.md`

## Goal

Define everything the collector exposes for operation at fleet scale, so a host
supervisor can gate a rollback correctly and a control plane can answer "is this
cabinet working" without reaching into it.

Three directions, deliberately asymmetric:

| Direction | Mechanism | Exists today |
|---|---|---|
| **Local** — supervisor and on-site technician | HTTP on loopback | no |
| **Outbound** — fleet observability | events on the bus | partly |
| **Inbound** — desired state | config file, no control channel | n/a |

## 1. Local surface

An HTTP listener bound to loopback. Three endpoints.

### `/readyz` — has this version proven itself?

Ready means **booted, config accepted, and at least one successful device
poll**. The last clause is what makes it real: it is the difference between
"the binary runs on this host" and "this cabinet is collecting." A config that
points at the wrong addresses starts cleanly and polls nothing, and that is
exactly the failure a rollback must catch.

**Readiness LATCHES.** Once true it stays true for the life of the process.

That is not laziness, it is the whole design. If readiness could go false, and
the supervisor restarts on unready, then a comms outage that takes every device
offline produces a restart loop — the collector killed repeatedly for a fault
it did not cause and cannot fix, during the exact window an operator needs it
reporting. Ongoing device health is what `DeviceStatusChanged` is for.

**Not gated on the broker.** A collector polling devices while the local broker
is down is working; it is the broker that is broken. Folding broker
connectivity into readiness produces rollbacks for the wrong reason.

**Startup grace matters.** The first poll arrives no sooner than the poll
interval plus the runner's start jitter, so the supervisor's grace period must
exceed twice the longest configured interval. Too tight and every deployment
looks like a failure.

### `/livez` — is the process wedged?

Continuous, unlatched, and deliberately weak: it answers whether the process is
responsive, not whether it is useful. Used by the supervisor to restart, never
to decide a rollout succeeded.

### `/status` — what is going on in this cabinet?

The on-site surface. A technician with a laptop should be able to answer "why
is this cabinet quiet" without shipping logs anywhere:

- collector version and build identity
- config revision, and expected version if configured
- uptime
- per device: last successful poll, last error, consecutive failures
- broker connected, events published, events dropped
- emitter drop counts by reason (no ce-type, unmappable value)

That last line matters: unclaimed events drop loudly to the log today, which is
invisible from outside. A counter makes a mapping gap observable in the field.

### Why HTTP

Portable across every host we are considering — systemd healthchecks, podman
`--health-cmd`, k8s probes if k3s ever lands, IOx later — and directly
inspectable by a human, which none of the alternatives are.

`sd_notify` (`Type=notify`, `READY=1`) is the systemd-native tightening and
podman supports passing the socket through with `--sdnotify=container`. It makes
readiness drive unit activation directly, so `podman auto-update`'s
rollback-on-failed-start gates on the right condition with no glue. Worth adding
as an addition to the HTTP endpoint, never as a replacement: it is
systemd-only and tells a human nothing.

## 2. Outbound surface

Collector-owned schema (ADR 0007), so none of this waits on an upstream
release. All of it rides `openits-collector.*`, which ADR 0011 already put on
its own subject root and its own stream.

**Exists:** `CollectorStarted` (carries version), `DeviceStatusChanged`
(reachability transitions).

**To add:**

- **Config revision on the boot event.** Desired state is
  `(binary version, config revision)`; both halves must be reportable or drift
  is only half visible.
- **A drift report** when the running version disagrees with the expected
  version. Makes a stalled or reverted rollout visible without the plane
  polling anything.
- **A periodic collector self-report.** This closes a real gap: with only
  transition events, a healthy quiet cabinet and a wedged-but-connected one
  look identical, because neither emits anything. A low-frequency status event
  carrying version, config revision, uptime and counters distinguishes them.
  At 15,000 cabinets on a five-minute interval that is ~50 msg/sec fleet-wide,
  which is noise against the ~75,000 msg/sec of telemetry.

## 3. Inbound surface — none for now, deferred rather than ruled out

**Desired state arrives as a config file on disk plus a restart.** Delivery is
someone else's problem: SSH and config management (Ansible) to begin with.

A correction worth recording, since an earlier draft argued this from ADR 0004:
**ADR 0004 does not govern this.** It is about how the collector interacts with
DEVICES — polling rather than receiving pushes — and says nothing about the
management interface. Citing it here was an over-application.

The reason inbound is deferred is simpler and better: **it does not touch the
collector.** Config file plus restart plus readiness gate is the interface, and
SSH, Ansible, an image rebuild, or a future NATS channel all drive it
identically. A decision that changes nothing in this repo is one that can wait
for evidence.

Also worth being honest about the security comparison, because an earlier draft
had it backwards. SSH grants a shell; a scoped NATS subscription does not. An
SSH key reaching every cabinet is a considerably larger prize than a per-cabinet
credential limited to one subject. SSH is the pragmatic starting point because
it already exists, not because it is the more conservative one.

**Triggers that would bring inbound-over-NATS forward:**

- **Cabinets are not routable.** Behind carrier NAT, SSH needs a tunnel or VPN
  fleet — at which point building on the NATS connection that already traverses
  NAT is cheaper than maintaining the tunnels.
- **Push stops converging.** SSH is push: a cabinet offline during a change
  silently misses it, and tracking and retrying becomes the automation's
  problem. Pull-based delivery converges for free. This bites somewhere above
  a thousand cabinets, not at first deployment.

**If it does arrive, two conditions:** the payload must be signed, so the trust
root is not merely a NATS credential on the cabinet; and last-good config must
be retained and restored automatically on a failed readiness gate. Without the
second, strict-boot turns the management channel into a bricking mechanism — a
cabinet that will not start cannot receive the fix.

**Declarative desired state only.** "You should be at config revision N" is
idempotent and convergent. Arbitrary remote command execution across 15,000
traffic cabinets is a materially different security proposition and should be
justified case by case, not enabled generally.

## 4. What the plane sees

Three signals answering three different questions. None substitutes for
another.

| Signal | Source | Answers |
|---|---|---|
| Leaf connect/disconnect | NATS `$SYS` at the hub | Is the cabinet *there*? |
| Health events + self-report | `openits-collector.>` | Is the collector *working*? |
| Boot event + drift | `openits-collector.>` | Is it running what we asked? |

The first is not on the collector's bus at all and cannot be — absence is not
an event. It is also what makes NATS-alongside pay off: when the collector dies
the leaf stays connected, so `$SYS` says *cabinet up* while health says
*collector silent*, which is "remote fix" rather than "truck roll."

## 5. Open problem: publish loss while the broker is down

Found while working through the broker/readiness interaction, and not yet
solved.

`publish.Publish` retries three times over roughly 750 ms and then the event is
**dropped**. The collector has no buffer. With NATS in its own container —
which D2 chose for good reasons — every broker restart, including its own
updates, is a data-loss window.

This is a consequence of the alongside decision that was not anticipated when
it was made. It does not reverse that decision: embedding the broker trades a
narrow loss window for a much worse failure mode, where a crash-looping
collector takes the cabinet dark. But it does need an answer.

Options, none yet chosen:

- **Bounded in-process buffer** with spill to disk. Reintroduces on-box
  durability the local broker was supposed to own, which is duplicative.
- **Much longer publish retry** with backoff, bounded by memory. Simplest;
  turns a restart window into latency rather than loss.
- **Accept the loss and make it visible** via a dropped-events counter in
  `/status` and the self-report. Honest, and possibly sufficient if broker
  restarts are rare and events are transitions rather than samples.

Worth deciding against measured broker restart duration rather than in the
abstract.

## Testing

Every guard here is one that must be shown to fail, per the standing rule:

- Readiness stays false when config parses but no device answers.
- Readiness latches: it does not go false when every device later goes
  unreachable.
- Readiness is unaffected by the broker being down.
- Drift is reported when expected and running versions disagree.
- The self-report fires on a quiet cabinet where nothing else emits.
