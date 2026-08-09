# Fleet Deployment and Update Plan

**Status:** Draft — scoped to Linux edge compute; one open question blocks Phase 1
**Scale:** 10,000–15,000 cabinets, one statewide DOT operating its own fleet
**Host scope:** Linux edge compute in the cabinet. IOx is expected for some
DOTs later and is explicitly deferred, NOT designed out — see "Why the split
still earns its place" below.
**Decision record:** ADR 0012 (host-executed updates), ADR 0011 (subject roots)

## Shape

Two layers, deliberately separated so a new host type is an executor rather
than a redesign:

| Layer | Owns | Linux | IOx |
|---|---|---|---|
| **Host mechanism** | fetch, swap, restart, local revert | systemd + `podman auto-update` | *(deferred)* IOx runtime + app versioning |
| **Fleet control plane** | who updates when, gating, halt | *(out of scope for this repo, which defines only its inputs)* | same |

### Why the split still earns its place with one host type

It is not an abstraction layer and costs nothing to keep: there is no adapter
interface, no plugin, no indirection. It is a discipline — the collector does
not learn how it was deployed, and the control plane speaks only
`cohort → desired version`. Holding that line while there is one host type is
free; recovering it after the collector has grown deployment awareness is not.

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

### D2 — One artifact or two? — RESOLVED: two, NATS alongside

NATS runs as its own container alongside the collector. Embedding it in the
collector process was the alternative, and the only real argument for it was
IOx resource pressure, which is out of scope here.

Two reasons alongside wins independently of that:

**A collector that will not start must not take the cabinet dark.** With NATS
embedded, a crash-looping collector removes the broker and the leaf connection
with it, and from the hub "the collector rejected its config" and "the cabinet
lost power" become indistinguishable — both are just silence. Alongside, the
leaf stays up and that distinction survives, which at 15,000 units is the
difference between a remote fix and a truck roll. It matters most during a
rollout, which is exactly when collectors restart.

**Patch cadence.** nats-server is network-facing and releases on its own
schedule. Embedded, every NATS CVE becomes a collector release and a fleet-wide
rollout. Alongside, it is one container updated on its own.

**What would flip it:** an IOx-class host where two apps genuinely do not fit.
If that arrives, the worst outcome is embedded-on-IOx and alongside-on-Linux —
two deployment topologies AND two durability stories. Prefer a third option
there (no cabinet-local broker at all, collector buffers in process and connects
straight to a regional hub) over splitting the durability model.

## Measure early

### M1 — JetStream write volume against flash endurance

The architecture leans on cabinet-local JetStream absorbing WAN outages
("local JetStream IS the durability story"). At the ~5 events/sec/cabinet
upstream sizes for, that is continuous writing — and on IOx it is likely an SD
card with limited write endurance and a small allocation.

If it does not hold, the consequences are structural, not tuning: tighter
retention, memory storage with accepted loss on power failure, or JetStream not
living in the cabinet at all on constrained hosts. It also decides D2.

### M2 — IOx resource envelope *(deferred with the IOx path)*

Varies enough by model and release that a general answer is not trustworthy.
Decides whether collector-plus-NATS fits at all when IOx returns to scope.

### M3 — What is the Linux box, and does the cabinet already contain one?

Now the central question rather than a cost comparison. ATC 5201 mandates a
Linux-capable runtime and upstream's vendor guide leans on exactly that, so
existing cabinet compute may already satisfy this.

What the answer needs to cover, because each item lands somewhere in this plan:

- **Storage type.** SSD, eMMC or SD decides how seriously to take M1.
- **Secure element / TPM.** Decides how NATS credentials are stored and
  rotated across 15,000 cabinets.
- **Out-of-band access.** Decides whether "collector will not start" is
  recoverable remotely or is always a truck roll — which is the same triage
  concern that resolved D2.
- **Who owns the OS.** See D3.

### D3 — Who patches the host OS, and how?

Not yet decided, and the largest omission in this plan's first draft. The
collector and NATS are two small containers; the host OS underneath them is the
bigger attack surface and the bigger long-term update burden on 15,000
internet-adjacent field devices.

Recommendation to evaluate: an **immutable OS with atomic updates and
rollback** — Fedora IoT, Flatcar, or Ubuntu Core. The reason is that it gives
the OS layer the same shape this plan already relies on at the app layer:
staged, health-gated, revert-on-failure. A traditional distro with
unattended-upgrades gives none of that, and package-level updates on a fleet
this size have no rollback story at all.

The trade is that immutable hosts constrain what can be installed and how
operators debug them, which some DOT network teams will resist.

## Phases

### Phase 1 — Collector-side participation (this repo, open source)

Everything ADR 0012 makes load-bearing. Small, portable, and a prerequisite for
any unattended rollout.

- Readiness signal per D1. Its meaning is about DEVICES, not the broker: a
  collector polling happily while the local broker is briefly down is working
  and will drain when the broker returns. Folding broker connectivity into
  readiness would produce rollbacks for the wrong reason.
- **Tolerate the broker being absent at startup.** `publish.Connect` currently
  dials NATS and provisions streams during boot, so a broker that is slow to
  come up makes the collector exit — which under health-gated rollback reads as
  a failed update and triggers a spurious revert. A missing peer is a transient,
  not a config error. This is a narrow exception to "config is the trust
  boundary" rather than a weakening of it: bad config still fails hard, an
  absent peer retries.
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

### Phase 4 — IOx executor *(deferred)*

Same `cohort → desired version` contract, different executor. Deferred, not
designed out: the contract is what keeps this a packaging exercise rather than
a second product.

- IOx application packaging.
- Rollout through Cisco's management plane rather than tag promotion.
- Whatever M1/M2 force on storage and artifact layout, including the
  possibility of no cabinet-local broker at all.

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
