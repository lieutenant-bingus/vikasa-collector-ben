# Contributing to vikasa-collector

Thanks for your interest in the OpenITS edge collector. Before writing code,
skim the architecture docs — the layering rules below are CI-enforced, and
PRs that fight them will be asked to restructure rather than merged:

- `README.md` — the one-diagram architecture and current status.
- `docs/adr/` — the accepted decision records. ADR 0002 (wire emitter
  boundary), ADR 0003 (in-tree adapters), and ADR 0008 (fixture bar) shape
  most contributions.

## The gate

```
make check    # vet + tests + boundary lint — exactly what CI runs
```

## Contributing a vendor adapter

Adapters are the most common contribution. The contract:

- Implement `sdk/adapter.StateReader` (or `EventReader`) returning only
  `sdk/model` types, and register a `Descriptor{Vendor, DeviceKind}`.
- Adapters own their transport entirely (SNMP, HTTP, serial, …) behind an
  opaque `connection` config block.
- Adapters must **not** import openits-models — only `internal/wire`
  may (ADR 0002, CI-enforced).
- Ship recorded fixtures with golden tests: **no fixtures, no merge**
  (ADR 0008). Record from a real device where possible; scrub anything
  deployment-identifying (addresses, community strings, site names) from
  recordings before committing.

## Commit conventions

Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`, …), present-tense
subjects. Keep generated or vendored content out of PRs unless the PR is
about regenerating it.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
Security reports go through [SECURITY.md](SECURITY.md), not public issues.
