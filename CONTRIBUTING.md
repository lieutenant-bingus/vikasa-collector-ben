# Contributing to vikasa-collector

Thanks for your interest in the OpenITS edge collector. Several of the rules
below are enforced by CI, and a PR that works against them will be asked to
restructure. [`invariants.md`](docs/reference/invariants.md) states each rule
exactly and says what enforces it.

## Where to start

- [`README.md`](README.md) — what the collector is, the pipeline in one
  diagram, and current status.
- [`docs/README.md`](docs/README.md) — the documentation hub. It routes by
  task and is the fastest way to find the page you need.
- [`docs/tutorial/build-your-first-adapter.md`](docs/tutorial/build-your-first-adapter.md)
  — a guided path from a fresh clone to a real event on the bus. The
  quickest way to learn the shape of a contribution.
- [`docs/reference/starter-tasks.md`](docs/reference/starter-tasks.md) — two
  tracks. If you have access to a device, the best first PR is an adapter
  for a device kind that is already modeled but has no adapter; it touches
  only `internal/vendors/<vendor>/`. Adapter work needs the hardware from
  the start, not just at the end. If you don't have a device, the same page
  lists open gaps that need none.
- [`docs/adr/`](docs/adr/README.md) — the decision records. ADR 0002 (wire
  emitter boundary), ADR 0003 (in-tree adapters) and ADR 0008 (fixture bar)
  shape most contributions.

## The gate

```bash
make check    # vet, golangci-lint (gofmt included), tidy, race tests, boundary and docs lints
```

`make check` is what the required `check` CI job runs. That job also
cross-compiles for linux `arm64`/`amd64` (`make cross`), fuzzes the
device-facing decode paths briefly (`make fuzz`), and validates the
GoReleaser config. `make vuln` (govulncheck) runs as a separate,
non-blocking workflow.

## Contributing a vendor adapter

Adapters are the most common contribution.
[`docs/how-to/add-a-vendor-adapter.md`](docs/how-to/add-a-vendor-adapter.md)
is the step-by-step guide. In short:

- Implement `sdk/adapter.StateReader`, returning only `sdk/model` types, and
  register a `Descriptor{Vendor, DeviceKind}`.
- The adapter owns its transport (SNMP, HTTP, serial, …) and parses its own
  `connection` config block, strictly: use `adapter.OnlyKeys` and
  `adapter.OptString` so a typo or wrong-typed value fails at boot.
- Don't import openits-models — see [the boundary
  rule](docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models).
- Ship recorded fixtures with golden tests — see [the fixture
  rule](docs/reference/invariants.md#no-fixtures-no-merge). Record from a
  real device, and scrub anything deployment-identifying (addresses,
  community strings, site names) before committing.

## What a reviewer will check

[`.claude/skills/review-adapter-contribution/SKILL.md`](.claude/skills/review-adapter-contribution/SKILL.md)
is the maintainer-side checklist for an adapter PR. It is plain markdown;
read it before you open the PR. It covers what `make check` cannot see:
fixture provenance, how failed reads are handled, and which files an adapter
PR should and should not touch.

## Commit conventions

[Conventional Commits](https://www.conventionalcommits.org/) (`feat:`,
`fix:`, `docs:`, `chore:`, `feat!:` for a breaking change), with
present-tense subjects. release-please builds `CHANGELOG.md` from them, so
never edit it by hand. Keep generated or vendored content out of PRs unless
the PR is about regenerating it.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
Report security issues through [SECURITY.md](SECURITY.md), not public
issues.
