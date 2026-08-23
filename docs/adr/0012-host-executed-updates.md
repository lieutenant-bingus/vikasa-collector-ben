# ADR 0012: Host-executed updates; the collector participates but never updates itself

**Status:** Accepted (2026-08-09)

## Context
The first operator is a statewide DOT running 10,000–15,000 cabinets. Cabinets
sit behind cellular NAT with no reachable inbound address, so anything that
reaches them does so because they dialled out. Two host types are expected: a
Linux box in the cabinet first, and Cisco IOx application hosting on IE4000-class
switches later — IOx is not a maybe, so a design that only works on Linux would
be rewritten rather than extended.

The tempting simplification is to make the collector update itself: one
mechanism, no dependency on podman versions, systemd behaviour, or Cisco's
management plane.

It does not survive contact with the failure modes. A process that replaces
itself still needs something to restart it afterwards, and something to decide
the replacement is bad and revert — and that decider cannot be the new binary,
because the new binary is the suspect. Self-update therefore does not remove the
external dependency; it relocates it into code we would own forever: atomic
swap, signature verification, resumable download over metered flaky links, and
revert-when-the-new-code-is-broken. That is a project in itself, and it is not
the project we are here to build.

Meanwhile every host we will deploy to already ships an executor that does
fetch, swap and revert-on-failure: systemd with `podman auto-update` on Linux,
the IOx runtime with app versioning on Cisco. What neither can do is judge
whether the collector is actually *working*. `podman auto-update` reverts when a
systemd unit fails to come up, but a collector can start cleanly, fail to reach
a single device, and sit there looking healthy forever.

That gap — not the update mechanism — is the part that is genuinely ours.

## Decision
The collector does not update itself and contains no update machinery. Hosts
execute updates using their native mechanism. The collector **participates** in
three ways, all host-agnostic:

1. **It reports its version** at boot, on the bus (`CollectorStarted` already
   carries it).
2. **It exposes a local readiness signal** a supervisor can gate on, meaning
   "booted, config accepted, and at least one successful device poll." Not
   "the process started" — that is the condition that fails to catch the
   interesting breakage.
3. **It can be told the version it is expected to be running**, and reports
   loudly on the bus when it is not. Drift becomes observable without the
   collector owning any part of resolving it.

The shared contract between the fleet control plane and any host is
**`cohort → desired version`**. Everything else is host-specific: on Linux a
cohort is a container tag and a rollout is moving it; on IOx it is a version the
management plane deploys. A new host type is a new executor, not a new design.

## Consequences
Two executors to support rather than one, and neither is ours to maintain. That
asymmetry is the point: the cost of supporting an executor is much smaller than
the cost of owning one, and it stays smaller as host types are added.

The readiness signal becomes load-bearing and must be built before any
unattended rollout. Without it, host rollback guards the wrong condition and
will happily keep a broken collector running because its process is alive.

The collector's strictness gets sharper teeth. "Config is the trust boundary,
refuse to start on anything unrecognised" is right for correctness and hazardous
unattended: a config a new binary rejects bricks a cabinet that then needs a
truck roll. At 15,000 units that is a budget line, not a bad day. Health-gated
rollback is therefore not optional — the strictness makes it *more* necessary,
not less.

Fleet-wide version skew becomes visible for free, because every collector
already announces its version and can announce disagreement with its expected
one.

The fleet control plane consumes only public interfaces — health events,
version reports, readiness. Nothing about driving a fleet requires privileged
access to the collector, so any implementation can do it.

## Alternatives considered
**Self-update inside the collector** (rejected; see Context). Attractive for
portability and rejected because it cannot escape needing an external
supervisor, so it buys uniformity at the price of owning the hardest and most
dangerous code in the system.

**Watchtower** (rejected outright). Pulls latest and restarts, with no cohorts,
no health gating, no rollback and no bandwidth control. It is a mechanism for
updating everything at once, which is the one thing a 15,000-unit safety-adjacent
fleet must never do.

**`podman auto-update` alone, with no control plane** (rejected as incomplete).
It is the right local executor, and it is per-host: each box pulls when its own
timer fires. There is no cohort and no way to halt after the first hundred
failures. Correct for the local half, silent about the half that matters at
fleet scale.

**A custom agent on each host** (rejected). Sits between the control plane and
the executor, and immediately raises the question of what updates the agent.
Using the host's own executor makes that question disappear on both host types.
