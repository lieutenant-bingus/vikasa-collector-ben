# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report suspected vulnerabilities privately through GitHub's
[**Report a vulnerability**](https://github.com/Vikasa2M/vikasa-collector/security/advisories/new)
flow (Security → Advisories → *Report a vulnerability*). This opens a private
advisory visible only to the maintainers.

Please include, as far as you can:

- the affected component (an adapter, the core, the publish path, a
  `scripts/` program, or a CI workflow);
- a description of the issue and its impact;
- steps to reproduce, or a proof of concept.

We aim to acknowledge a report within a few business days and will keep you
updated as we investigate and prepare a fix. We'll credit reporters who wish
to be named once a fix is released.

## Scope

This repository is an **edge collector** that runs inside ITS cabinets: it
polls field devices (SNMP and other vendor transports), normalizes readings,
and publishes CloudEvents to a local NATS JetStream. Security-relevant
reports we care about include:

- **Device-facing parsing** — a malformed or malicious device response
  (e.g. crafted SNMP data) that crashes the collector or corrupts what it
  publishes;
- **Config trust boundary** — config values that bypass boot validation and
  corrupt subject grammar, stream bindings, or event routing;
- **Publish path** — ways to forge, replay, or misroute events on the bus
  beyond what the deployment's NATS permissions allow;
- **Supply-chain / CI issues** — e.g. an unpinned or compromisable action,
  or a workflow that could leak the repository token or secrets.

Out of scope: vulnerabilities in the devices themselves, in NATS, or in
deployments' network configuration.
