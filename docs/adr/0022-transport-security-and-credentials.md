# ADR 0022: Transport security and credentials

**Status:** Accepted (2026-10-15)

## Context
The collector has been built loopback-first: it publishes to a NATS server on
the same host, serves its management surface on loopback, and polls devices
on the cabinet network. Every security property it has today follows from
that topology rather than from a decision, and three things are about to test
it:

- **A cabinet's traffic leaves the box.** The fleet plan has each cabinet's
  NATS leaf-node to a hub over cellular. Whatever the collector publishes
  locally is, from then on, replicated across networks nobody in this
  project controls.
- **The local broker may not stay local.** Nothing stops an operator setting
  `-nats` to a broker on another host — the collector would then speak
  plaintext across the cabinet network, and there is no supported way to give
  it credentials except embedding them in the URL.
- **Credentials have already leaked once by accident.** PR #29 needed three
  rounds of security review to stop a password in `-nats` reaching logs and
  stderr, and the final fix still notes that the same password is visible to
  anyone who can run `ps`. The URL was never a good place for a secret; the
  collector simply had no other.

The device side has its own exposure: the `ntcip` adapters speak SNMP v1 and
v2c only, so community strings cross the cabinet network in plaintext, and
they sit in `collector.yaml`.

This record states the trust boundaries once, and decides what the collector
enforces at each. Each decision is either already implemented (and cited), or
small and implementable now, or explicitly deferred with the condition that
should bring it forward.

### The boundaries

| Boundary | Between | Today |
|---|---|---|
| Local broker | collector ↔ NATS on the same host | loopback, plaintext, no credentials |
| Uplink | cabinet NATS ↔ hub (leaf node) | not built; broker configuration |
| Field network | collector ↔ devices | SNMP v1/v2c, community in plaintext |
| Management surface | local processes ↔ collector | loopback-only, Host-checked (#32) |
| Logs and shipped events | collector ↔ journal, and the bus | credential-scrubbed (#29, #35) |
| Supply chain | source ↔ release artifact | pinned actions, cooldown, govulncheck, checksums (#21, #36) |

## Decision

**1. NATS credentials come from a file, never the URL.**

- A new `-nats-creds <path>` flag takes a NATS `.creds` file (a user JWT and
  its nkey seed: the format NATS's decentralized auth and leaf-node accounts
  already use, so one credential format serves the cabinet and the hub).
- A `-nats` URL containing userinfo — `user:pass@` or a bare `token@` — is
  **refused at boot** as a usage error. This is a deliberate tightening of
  the trust boundary (ADR 0014): it removes the only path by which a secret
  ever reached logs, stderr or the process table, rather than scrubbing it
  after the fact. #29's scrubbing stays as defence in depth.
- The creds file must grant nothing to other users (no `o` bits), checked at
  boot the way `ssh` checks a private key. It is installed `0640
  root:vikasa-collector`, like the config: readable by root and the service's
  group only. A world-readable credential is a misconfiguration, and boot is
  where it is cheapest to say so.

**2. Plaintext only on loopback.**

- A loopback `-nats` (127.0.0.0/8, `::1`, `localhost`) stays plaintext. TLS on
  loopback costs certificate management and protects nothing a local root
  could not already read.
- A **non-loopback `-nats` must be `tls://`** — refused at boot otherwise. A
  new `-nats-ca <path>` names the CA to verify the broker against, for the
  private CAs a fleet will run; without it the system roots are used. The
  collector never skips verification, and there is no flag to make it.
- Mutual TLS is not added. The `.creds` file already authenticates the
  collector; client certificates would be a second identity to rotate for no
  gain.

**3. Least privilege on the broker is documented, and tested.**

The collector's NATS user needs exactly: publish on
`openits.<region>.<agency>.<agency_unit>.>` and
`openits-collector.<region>.<agency>.<agency_unit>.>`; the JetStream API
subjects for the streams it provisions (ADR 0011) and the
`VIKASA_SYNTH_STATE` bucket (ADR 0017); and its own `_INBOX.>` for replies.
It needs nothing else — in particular no subscribe on the subject roots it
publishes to. The implementing change derives the exact permission set,
proves it with a test that runs the collector against a broker granting only
that set, and documents it in the deploy guide, so an operator never has to
guess or grant `>`. (Done: the set is in the deploy guide, and
`TestLeastPrivilegeGrantIsSufficient` runs the collector under it.)

**4. The uplink is the broker's job, with requirements stated here.**

The collector never speaks to the hub. The leaf-node link between the
cabinet's NATS and the hub is broker configuration owned by the deployment,
and it must have: TLS; a per-cabinet credential scoped to that cabinet's two
subject roots (the ADR 0011 grammar makes that a prefix grant); and a
JetStream domain, so cabinet-local state never federates upward (ADR 0017).
How those credentials are stored and rotated across a fleet — TPM, secure
element, or files — is the fleet spec's open question M3 and is not decided
here.

**5. Device side: SNMP v1/v2c stays, as read-only, and community strings are
config secrets.**

- The collector is collect-only (ADR 0004, ADR 0021): it issues GETs and never
  SETs. Deployments should give it a **read-only community** and, where the
  device supports it, an SNMP view limited to the objects the adapter reads.
  The deploy guide says so.
- Community strings live in `collector.yaml`, so the config file is a secret:
  installed `0640 root:vikasa-collector`, readable by root and the service
  only, never logged — no log line carries connection configuration today
  (audited in #35) — and never shipped in an event.
- That is why the unit runs as a **static** system user rather than
  `DynamicUser=`: a dynamic user has no stable group to grant the secret to.
  `LoadCredential=` would avoid needing one, but it could not be shown to
  work in this project's test environment, and a deployment instruction is
  only worth giving once it has been run. It stays the natural upgrade.
- **SNMPv3 (USM, authPriv) is deferred.** Bring it forward when an agency
  requires it, or when a deployment's cabinet network is not segregated from
  non-ITS equipment, whichever comes first. The transport seam already
  exists (`sdk/transport/snmp`), so it is an addition to the connection block,
  not a redesign.

**6. Already decided, recorded here so the picture is in one place.**

- **Management surface:** loopback-only bind, refused at boot otherwise, and
  only `Host: localhost` or a loopback IP answered, which closes DNS
  rebinding (#32). It is unauthenticated by design: anything that can reach
  loopback can already read the process.
- **Logs and shipped events:** credentials are scrubbed from every logged or
  returned error by literal replacement, not by pattern (#29), and the log
  records shipped as events carry only what the local log does (#35).
- **Supply chain:** SHA-pinned actions with a 14-day cooldown, govulncheck,
  a pinned toolchain, and release checksums (#21, #36). Artifact signing and
  provenance (cosign, SLSA) are deferred until releases are consumed outside
  the team that builds them — at that point a checksum file on the same
  release page stops being evidence of anything.

## Consequences

- A cabinet can point its collector at a remote or authenticated broker for
  the first time, safely, and a secret can no longer reach `ps`, the
  journal, a shell history or a unit file by way of the URL.
- **Breaking, pre-1.0:** a `-nats` with embedded credentials, or a plaintext
  non-loopback `-nats`, now fails boot. No deployment uses either.
- The collector still provisions its own streams and bucket, which is why it
  needs JetStream API permissions at all. A deployment that wants the
  collector to hold publish-only rights would need provisioning moved out of
  it (a flag that skips it and expects the streams to exist); that is left
  for when an operator asks, because it trades a smaller grant for a
  boot-time dependency on someone else having provisioned correctly.
- SNMP community strings remain readable to anyone on the cabinet network who
  can capture traffic. That is an accepted risk under decision 5's triggers,
  not an oversight.

## Alternatives considered

- **Keep credentials in the URL and rely on scrubbing.** Rejected: #29 showed
  scrubbing is easy to get subtly wrong (a parser differential leaked parts
  of passwords until the third fix), and no scrubber reaches `ps`.
- **Username/password or token flags.** Rejected: a flag value is as visible
  to `ps` as a URL. Files are the only argument form that keeps the secret
  out of the process table.
- **TLS everywhere, loopback included.** Rejected: certificate distribution
  and rotation for a connection that never leaves the host.
- **SNMPv3 now.** Rejected for now: real per-device key management for a
  read-only, segregated network with no agency asking for it. The triggers in
  decision 5 say when that changes.
- **Collector-side mTLS to the hub, skipping the local broker.** Rejected by
  ADR 0011 and the fleet spec's D2: the local broker is the durability story,
  and the leaf link is where hub transport belongs.
