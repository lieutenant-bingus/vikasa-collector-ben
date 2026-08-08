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
- `internal/wire/openits` — the openits-models emitter: protobuf bodies,
  catalog ce-types. Unsuffixed while the pin is lockstep-with-HEAD, since
  exactly one models version is ever compiled in; it splits into
  `internal/wire/<version>` at the first tagged pin (ADR 0010). The design
  is `docs/specs/2026-07-21-openits-models-emitter-design.md` — read it
  **and its Revisions section** before emitter work; the body was written
  against v0.2.2 and several items were corrected on 2026-08-08.

## Rules that make the layer work

- **openits-models is pinned at main HEAD** (pseudo-version) while both
  repos move in lockstep pre-v1 (ADR 0010, amending 0002) — never a
  `replace` on a checkout, which would break reproducibility. Tagged pins
  and versioned emitter packages return at openits-models v1.0.0, or
  sooner if a consumer outside this team pins it. Version coexistence
  happens across the fleet (config selects the emitter at boot, ADR 0005),
  never inside a process.
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

1. Read the CHANGELOG (or the commit range) and diff its
   `bindings/nats/asyncapi.yaml` ce-type set against the current emitter's
   `CETypes()`.
2. While the pin is lockstep-with-HEAD (ADR 0010): `go get -u
   github.com/Vikasa2M/openits-models@main`, adjust mappings/constants
   in place, and claim any newly-available events that were dropping
   (check the drop warnings for candidates). No new package — there is
   only ever one.
3. Once tagged pins resume: copy the emitter package to
   `internal/wire/<newversion>` and pin the tag, so both compile side by
   side (ADR 0002 S2).
4. Either way, every golden diff is a decision — confirm each against the
   models change, don't just regenerate. A golden that moved without a
   mapping edit means the models module changed under you, which during
   lockstep is the expected way you find out.
5. Goldens: fixture event in → exact header set + payload bytes out, per
   mapped event. Regenerate deliberately.
6. Once there are two emitter packages, update the `model_version` default
   only when the fleet is ready; both compiling side by side is the normal
   state (ADR 0002's S2 scenario).

## Envelope invariants (all emitters, `internal/publish` + `internal/cloudevents`)

CloudEvents binary mode: `ce-*` NATS headers, raw payload body.
`ce-type` stays catalog-verbatim and `ce-source` stays canonical
regardless of the operator's subject template — subjects are routing, the
envelope is identity (ADR 0009).

`ce-id` and `ce-source` are **openits-models' contract, not ours** — the
collector is only its reference *implementation*. Read that repo's
`ce-id-spec.md` and `bindings/nats/README.md`; do not reason from the
current code, which predates both.

- `ce-id` is a **ULID**: `SHA-256(ce-source ‖ ce-type ‖ stable-time ‖
  payload)` with `0x1f` separators, then `ULID(timestamp = ce-time-ms,
  randomness = digest[0:10])`. Its leading 48 bits ARE `ce-time` — that
  is an input, not a clock read, so it stays deterministic and replay
  dedup still works. Never randomize it, and never "simplify" it back to
  a bare content hash; the published test vector is the golden.
- `ce-source` is `urn:openits:<entity-kind>:<region>:<agency>:<unit>:<id>`.
  `entity-kind` is NOT `DeviceKind` — `asc` → `controller`, `dms` →
  `sign`, collector-level → `collector`.
- The two are coupled: `ce-id` hashes the literal `ce-source` bytes, so a
  change to source formatting changes every id.

Gate: `make check` and `go test ./... -race`.
