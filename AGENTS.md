# Agent instructions — vikasa-collector

Open-source edge collector for ITS cabinets: vendor adapters poll field
devices, the core diffs snapshots into domain events, wire emitters encode
them, and the publisher ships CloudEvents to the cabinet's local NATS
JetStream. Architecture rationale for the built system lives in
`docs/adr/` (accepted decision records) and `docs/explanation/` (how the
pieces fit together) — consult them before restructuring anything.
[`docs/README.md`](docs/README.md) is the documentation hub and routes to
all of it by task. The rules CI actually enforces are catalogued
canonically in
[`docs/reference/invariants.md`](docs/reference/invariants.md) — that
table is what to trust for exact wording, not this file.

## Commands

```bash
make check              # vet + golangci-lint (gofmt) + tidy + race tests + boundary/docs lints — the CI gate
make lint-docs          # just the docs lint: relative links, #anchors, skill sections
make vuln               # govulncheck; CI runs it as its own, non-blocking workflow
make fuzz               # coverage-guided fuzzing of every Fuzz target (FUZZTIME=10s each; CI runs 15s)
make dev                # whole pipeline locally: embedded NATS + synthetic device
make cross              # linux arm64/amd64 build; CI runs it after make check
go run ./cmd/collector -config collector.yaml   # needs a real broker AND real devices
```

## Layout

- `cmd/collector` — the binary; `adapters.go` holds `RegisterAdapters`, the
  one place adapters are registered. `cmd/dev` — the `make dev` harness.
- `sdk/` — the stable surface adapters build on: `sdk/adapter` (the
  `StateReader` contract, registry, strict connection-block helpers),
  `sdk/model` (facets and domain events), `sdk/transport/snmp` (+ its
  `snmptest` replay fake).
- `internal/vendors/ntcip` — `ntcip-asc` and `ntcip-dms`.
- `internal/vendors/cameleon` — `cameleon-acs` (reversible-lane gates over
  GE SRTP).
- `internal/synth` — one differ per facet kind. `internal/durable` keeps
  its previous state in the JetStream KV bucket `VIKASA_SYNTH_STATE`.
- `internal/wire/openits` — the openits-models emitter (`encode_<family>.go`
  per service); `internal/wire/health` — the collector-owned schema
  (described in `asyncapi.yaml`); `internal/wire/tier2` — the Tier 2
  conformance test.
- `internal/runner` (poll loops), `internal/publish` (NATS publisher and
  bounded queue), `internal/subject` (subject templates),
  `internal/cloudevents` (envelope, deterministic `ce-id`),
  `internal/manage` (loopback `/livez`, `/readyz`, `/status`),
  `internal/config`, `internal/app` (wires it all together).
- `internal/docs` — tests that keep `configuration.md`, `invariants.md` and
  `asyncapi.yaml` in step with the code.

## Layering rules

The openits-models boundary is three CI-enforced rules — see [the boundary
rule](docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models)
for exact wording:

- Adapters (`internal/vendors/`) and `sdk/` stay on `sdk/model` types and
  don't reach for openits-models.
- The dependency pin carries no `replace` directive — see [that
  rule](docs/reference/invariants.md#the-openits-models-pin-carries-no-replace-directive).
- The pin names a release tag, never a `main`-HEAD pseudo-version — see
  [that
  rule](docs/reference/invariants.md#the-openits-models-pin-names-a-release-tag)
  (ADR 0018).

Whether the tagged release is a *current* one is [a separate
row](docs/reference/invariants.md#the-pinned-release-is-a-current-one-not-an-old-tag)
and is **not** CI-enforced: a lint can check the version string's shape,
not its recency.

There is one openits-models emitter package, `internal/wire/openits`, and a
pin bump edits it in place. A second, versioned package begins only when a
fleet genuinely needs two model releases compiled into one binary.

The synth engine's iron rule concerns what a differ or adapter may do with
a failed or absent facet read — see [absence of
evidence](docs/reference/invariants.md#absence-of-evidence-is-never-a-state-change)
before writing either.

Subject routing and the CloudEvents envelope are deliberately kept
independent of each other — see [the subjects-vs-envelope
rule](docs/reference/invariants.md#subjects-are-operator-configurable-the-cloudevents-envelope-stays-canonical).

## Task guides

Detailed workflows live in `.claude/skills/` (readable as plain markdown
by any agent or human):

- `add-vendor-adapter` — new vendor × device-kind integrations
- `add-domain-facet` — new facets, differs, and domain events
- `add-transport` — a new `sdk/transport/<name>` package for a protocol nothing speaks yet
- `wire-emitter` — openits-models mappings and release pin bumps
- `review-adapter-contribution` — the maintainer-side checklist for an incoming adapter PR

## Testing bar

Every adapter read path ships recorded fixtures with golden tests — see
[the fixture
rule](docs/reference/invariants.md#no-fixtures-no-merge). Scrub
deployment-identifying data (addresses, community strings, site names)
from recordings before committing. Differ tests have their own matrix —
see [`test-requirements.md`'s "A new differ"
section](docs/reference/test-requirements.md#a-new-differ) for what a
differ PR must cover.

## Conventions

- Conventional Commits (`feat:` / `fix:` / `feat!:`); the changelog is
  generated by release-please — never hand-edit `CHANGELOG.md`.
- **No co-author or AI attribution lines in commits.**
- Work through pull requests; `main` is protected and requires the
  `check` status.
- Config is the trust boundary — see [the config
  rule](docs/reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized).
- Documentation changes: run `make lint-docs`, and add any new doc to the
  hub in `docs/README.md`.
