# Contributing to vikasa-collector

Thanks for your interest in the OpenITS edge collector. Before writing code,
skim the architecture docs — several of the rules below are CI-enforced
(exact wording and enforcement per rule: [`invariants.md`](docs/reference/invariants.md)),
and PRs that fight them will be asked to restructure rather than merged:

- `README.md` — the one-diagram architecture and current status.
- [`docs/README.md`](docs/README.md) — the documentation hub. It routes by
  task, and it is the fastest way to find the page you actually need.
- [`docs/tutorial/build-your-first-adapter.md`](docs/tutorial/build-your-first-adapter.md)
  — never seen this repo? This takes a fresh clone through to a real event
  on the bus in one sitting. It is the fastest way to learn the shape of a
  contribution before making one.
- [`docs/reference/starter-tasks.md`](docs/reference/starter-tasks.md) — two
  tracks. Five device domains are modeled, diffed and wired to real ce-types
  with no adapter producing them; landing one is the highest-leverage first
  PR and touches `internal/vendors/<vendor>/` alone — but it needs access to
  the hardware, which is a precondition of adapter work rather than a hurdle
  at the end of it. If you don't have a device, the same page lists open gaps
  that need none.
- `docs/adr/` — the accepted decision records. ADR 0002 (wire emitter
  boundary), ADR 0003 (in-tree adapters), and ADR 0008 (fixture bar) shape
  most contributions.

## The gate

```
make check    # vet + tests + boundary lint — exactly what CI runs
```

## Contributing a vendor adapter

Adapters are the most common contribution.
[`docs/how-to/add-a-vendor-adapter.md`](docs/how-to/add-a-vendor-adapter.md)
is the canonical step-by-step guide; the contract it implements:

- Implement `sdk/adapter.StateReader` (or `EventReader`) returning only
  `sdk/model` types, and register a `Descriptor{Vendor, DeviceKind}`.
- Adapters own their transport entirely (SNMP, HTTP, serial, …) behind an
  opaque `connection` config block.
- Adapters don't reach for openits-models types — see [the boundary
  rule](docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models).
- Ship recorded fixtures with golden tests — see [the fixture
  rule](docs/reference/invariants.md#no-fixtures-no-merge). Record from a
  real device where possible; scrub anything deployment-identifying
  (addresses, community strings, site names) from recordings before
  committing.

## What a reviewer will check

[`.claude/skills/review-adapter-contribution/SKILL.md`](.claude/skills/review-adapter-contribution/SKILL.md)
is the maintainer-side checklist for an adapter PR. It is plain markdown and
it is not a secret — read it before opening the PR. The machine checks are
`make check` and `go test ./... -race`; that document covers everything those
cannot see, including fixture provenance, absence-of-evidence handling, and
which files an adapter contribution should and should not touch.

## Commit conventions

Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`, …), present-tense
subjects. Keep generated or vendored content out of PRs unless the PR is
about regenerating it.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
Security reports go through [SECURITY.md](SECURITY.md), not public issues.
