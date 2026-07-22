---
name: wire-emitter
description: Work on the wire-emitter layer — mapping domain events to openits-models payloads, adopting a new openits-models release (pin bump), or adding ce-type mappings. Use this whenever a task touches internal/wire, mentions openits-models versions/protos/ce-types, CloudEvents encoding, or "publish X to the bus" — and when reviewing changes under internal/wire.
---

# Wire emitters and openits-models pins

`internal/wire` is the ONLY layer allowed to know wire schemas
(ADR 0002; CI-enforced by `scripts/lint-boundary.sh`). An emitter maps
domain events to `(proto payload, ce-type)` via the `wire.Emitter`
interface in `internal/wire/emitter.go`. Everything below `internal/wire`
— adapters, `sdk/model`, the synth engine — must stay ignorant of
openits-models, so that model-release churn is always a one-package edit.

Two emitter families exist by design:
- `internal/wire/health` — the collector-owned health schema (ADR 0007),
  JSON bodies, `openits-collector.health.*` ce-types. It does not track
  openits-models releases; leave it alone during pin bumps.
- `internal/wire/<version>` — one package per pinned openits-models
  release, protobuf bodies, catalog ce-types. The design for the first
  one is `docs/specs/2026-07-21-openits-models-emitter-design.md`; read
  it before emitter work.

## Rules that make the layer work

- **openits-models is consumed as tagged semver releases only** — never a
  `replace` on a checkout. Version coexistence happens across the fleet
  (config selects the emitter at boot, ADR 0005), never inside a process.
- **The mapping is dumb by design.** Field-by-field copies, explicit enum
  switches. No reflection, no mapping DSL, no clever generality — a
  reviewer must be able to check each mapping against the two schemas by
  reading it. Where the domain is richer than the wire model, make an
  explicit **map-or-drop** decision per field/event and record it in a
  comment at the decision site.
- **Unclaimed events drop loudly** (metric + log) at the emitter chain —
  `Encode` returns `ok=false` for "not mine". Never claim an event you
  can't faithfully encode, and never guess: a shared event (fault-raised,
  mode-changed) with an unknown `DeviceKind` is not claimed.
- **`CETypes()` must be complete and sorted.** Boot validation renders
  every reported ce-type through the operator's subject template; an
  emitter that under-reports defeats that check silently.
- **ce-types are catalog-verbatim** (`openits.<service>.<event>.v<major>`),
  and each carries a constant `ce-dataschema` URL pinned to that release's
  schema-registry revision. openits-models ships no Go catalog API, so
  these are hard-coded constants locked by golden tests — that's
  deliberate: a pin bump should *show up* as a reviewable constants diff.

## Adopting a new openits-models release (pin bump)

1. Read the release's CHANGELOG and diff its `bindings/nats/asyncapi.yaml`
   ce-type set against the current emitter's `CETypes()`.
2. For an additive release: copy the current emitter package to a new
   `internal/wire/<newversion>`, update `go.mod` to the new tag, adjust
   mappings/constants, and claim any newly-available events that were
   dropping (check the drop metrics/warnings for candidates).
3. For a breaking release: same, but every golden diff is a decision —
   confirm each against the models changelog, don't just regenerate.
4. Update the emitter selection (`model_version` in config) default only
   when the fleet is ready; both packages compiling side by side is the
   normal state (ADR 0002's S2 scenario).
5. Goldens: fixture event in → exact header set + payload bytes out, per
   mapped event. Regenerate deliberately; a golden that changed without a
   mapping edit means the models module changed under you.

## Envelope invariants (all emitters, `internal/publish` + `internal/cloudevents`)

CloudEvents binary mode: `ce-*` NATS headers, raw payload body. `ce-id`
is content-addressed (deterministic — JetStream replay dedup depends on
it; never randomize). `ce-type` stays catalog-verbatim and `ce-source`
stays canonical regardless of the operator's subject template — subjects
are routing, the envelope is identity (ADR 0009).

Gate: `make check` and `go test ./... -race`.
