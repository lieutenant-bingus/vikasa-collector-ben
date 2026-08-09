# Fleet Deployment and Update Plan

**Status:** Draft — two open questions below block Phase 1, see "Decide first"
**Scale:** 10,000–15,000 cabinets, one statewide DOT operating its own fleet
**Decision record:** ADR 0012 (host-executed updates), ADR 0011 (subject roots)

## Shape

Two layers, deliberately separated so a new host type is an executor rather
than a redesign:

| Layer | Owns | Linux | IOx |
|---|---|---|---|
| **Host mechanism** | fetch, swap, restart, local revert | systemd + `podman auto-update` | IOx runtime + app versioning |
| **Fleet control plane** | who updates when, gating, halt | *(out of scope for this repo, which defines only its inputs)* | same |

The contract between them is **`cohort → desired version`**. On Linux a cohort
is a container tag and a rollout is moving the tag; on IOx it is a version the
management plane deploys. Neither executor is ours to maintain.

The collector never updates itself (ADR 0012). It reports version, exposes
readiness, and reports drift from its expected version.

## Decide first

These two shape everything downstream and are far cheaper to settle now than
after 15,000 units exist.

### D1 — Readiness signal: what does "healthy" mean, and how is it exposed?

`podman auto-update` reverts when a systemd unit fails to start. That guards the
wrong condition: the collector can start cleanly, fail to reach any device, and
sit there with a live process. Health-gated rollback needs a signal that means
*working*, not *running*.

Proposed meaning: booted, config accepted, and **at least one successful device
poll**. That last clause is what makes it real — it is the difference between
"the binary runs on this host" and "this cabinet is collecting."

Open: exposure mechanism. A local HTTP endpoint is the obvious choice and works
identically under systemd healthchecks and IOx. A file or exit-code contract is
smaller but harder to gate on. Needs a decision before Phase 1.

### D2 — One artifact or two?

The collector needs a cabinet-local NATS with JetStream. On Linux that is
comfortably a second container. On IOx it competes for a very tight envelope,
and it is a second app lifecycle to manage on a host that makes lifecycles
expensive.

Option: embed the NATS server in the collector process. Not speculative — the
test suite already runs one in-process. One artifact, one lifecycle, much
friendlier to constrained hosts. The cost is coupling: a collector restart takes
the broker with it, so a hub cannot drain during that window. Since the store is
on disk and the hub catches up afterwards, this is probably acceptable, but it
should be measured rather than assumed.

Blocked on M1 below.

## Measure early

### M1 — JetStream write volume against flash endurance

The architecture leans on cabinet-local JetStream absorbing WAN outages
("local JetStream IS the durability story"). At the ~5 events/sec/cabinet
upstream sizes for, that is continuous writing — and on IOx it is likely an SD
card with limited write endurance and a small allocation.

If it does not hold, the consequences are structural, not tuning: tighter
retention, memory storage with accepted loss on power failure, or JetStream not
living in the cabinet at all on constrained hosts. It also decides D2.

### M2 — IOx resource envelope on the actual IE4000 model and IOS version

Varies enough by model and release that a general answer is not trustworthy.
Decides whether collector-plus-NATS fits at all.

### M3 — Does the DOT's cabinet already contain compute?

ATC 5201 mandates a Linux-capable runtime, and upstream's vendor guide leans on
exactly that. If there is already an industrial PC or ATC controller in the
cabinet, the hardware-cost argument for IOx largely evaporates and IOx becomes a
fallback rather than a destination. At 15,000 units the difference is millions
of dollars, so this is worth answering before committing to either path.

## Phases

### Phase 1 — Collector-side participation (this repo, open source)

Everything ADR 0012 makes load-bearing. Small, portable, and a prerequisite for
any unattended rollout.

- Readiness signal per D1.
- Expected-version config plus a drift report on the bus when the running
  version disagrees.
- Confirm the boot event carries enough to identify a build, not just a
  semantic version.

### Phase 2 — Linux reference deployment (this repo)

This ships *with* the collector: the recipe an agency can run as-is, with no
additional tooling.

- Container image and systemd unit.
- `podman auto-update` wired to the readiness signal so revert gates on working,
  not running.
- Outbound leaf connection to a hub; credentials scoped per cabinet. Subject
  permissions fall out of the ADR 0011 grammar — a hub can grant
  `openits.<region>.<agency>.<unit>.>` without also exposing collector
  internals, because health lives on its own root.
- Config delivery. At 15,000 cabinets configs are *generated* from the agency's
  device inventory, not hand-authored; the collector already accepts whatever a
  generator produces.

### Phase 3 — Fleet control plane (out of scope for this repo)

Consumes only public interfaces — health events, version reports, readiness.
Nothing about driving a fleet requires privileged access to the collector, so
any implementation can do it.

- Inventory and cohort membership.
- Desired version per cohort; rollout by tag promotion.
- Gate on signals already on the bus: the new version published a boot event
  *and* kept its devices reachable for N minutes. Both signals exist today —
  `CollectorStarted` carries version, `DeviceStatusChanged` carries
  reachability.
- Halt on cohort failure. This is the capability no host executor provides and
  the reason the plane exists.

### Phase 4 — IOx executor

Same `cohort → desired version` contract, different executor.

- IOx application packaging.
- Rollout through Cisco's management plane rather than tag promotion.
- Whatever M1/M2 force on storage and artifact layout.

## Risks

**Strict boot plus unattended update bricks cabinets.** "Refuse to start on
anything unrecognised" is right for correctness and hazardous for a fleet: push
a config a new binary rejects and the cabinet needs a truck roll. Health-gated
rollback is the mitigation and it is mandatory, not a nicety — the strictness
makes it more necessary rather than less.

**Bandwidth.** A ~20 MB image across 15,000 cellular links is ~300 GB per full
rollout, metered. Cohorts help; delta or layer reuse helps more. Worth costing
before the first fleet-wide update rather than after.

**Mixed fleet is the steady state, not a transition.** A statewide DOT across
15,000 cabinets will have several hardware generations at once, and ADR 0005
already assumes version coexistence across the fleet. The two-layer split is
mandatory rather than tidy.

**NATS topology at this scale.** 15,000 leaf nodes into a single cluster is a
lot; regional hubs are likely. Not urgent, but it interacts with credential
scoping and should not be discovered late.
