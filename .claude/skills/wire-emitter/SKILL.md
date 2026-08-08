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
  catalog ce-types. It splits into `internal/wire/<version>` packages once
  more than one models release has to compile at the same time (ADR 0002's
  S2). The design is
  `docs/specs/2026-07-21-openits-models-emitter-design.md` — read it **and
  its Revisions section**, which records where the body has been overtaken
  by later models releases.

## Ground truth: probe, don't read

openits-models' prose and its generated code have disagreed more than
once, and the prose is the one that lies. Before mapping anything, resolve
the module at the pinned version in a scratch Go module and **run** it —
construct the message, marshal it, print the bytes. Shapes looking right
is not a result; a probe that never produced a byte proves nothing.

```bash
mkdir /tmp/probe && cd /tmp/probe && go mod init probe
go get github.com/Vikasa2M/openits-models@<the version in our go.mod>
# then a main.go that builds the message, marshals it, and prints hex
```

Things that have surprised people, worth checking rather than assuming:

- **Enums may have no `UNSPECIFIED` zero value.** Several start their
  first real member at 0, so a zero field is a meaningful value, not
  "unset" — you cannot distinguish them on the wire.
- **`identityref` leaves are plain `string`** on the generated structs,
  carrying `defining-module:identity-name`. They are not Go enums, and the
  identity set is not always what the domain enum's name suggests — check
  which module defines the identity you mean, since several services
  define same-named ones.
- **Some numeric-looking leaves are strings** (YANG `decimal64` renders as
  a string), so a field the domain holds as an integer may need formatting
  rather than conversion.
- **`ce-id-spec.md` ships a test vector.** Reproduce it from the real
  generated type before trusting any `ce-id` implementation — it exercises
  the payload encoding and the digest chain together. If it doesn't
  reproduce, the implementation is wrong, not the vector.

Record what you verify, and against which version, so the next person
re-probes only what the pin bump could have moved.

## Rules that make the layer work

- **openits-models is consumed as tagged semver releases** — never a
  `replace` on a checkout, which would break reproducibility for everyone
  who doesn't have your working tree. Version coexistence happens across
  the fleet (config selects the emitter at boot, ADR 0005), never inside a
  process. Whatever the current pin is, it is a real, immutable module
  version in `go.mod`; check there rather than assuming, and see ADR 0002
  and any ADR amending it for the pinning rule in force.
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
2. Move the pin to the new release, adjust mappings and `ce-dataschema`
   constants, and claim any newly-available events that were dropping
   (the drop warnings name the candidates). While only one models release
   has to compile, edit the existing emitter package in place.
3. When the fleet needs two releases at once, copy the emitter to
   `internal/wire/<newversion>` instead, so both compile together (ADR
   0002's S2). Update the `model_version` default only when the fleet is
   ready to move; two packages coexisting is the normal state, not a
   migration to rush.
4. Re-probe the generated code for anything the release could have moved
   (see "Ground truth" above) — enum numbering and identityref spellings
   change without any signal at the Go type level.
5. Goldens: fixture event in → exact header set + payload bytes out, per
   mapped event. Every diff is a decision — confirm each against the
   models change, never regenerate wholesale. **A golden that moved
   without a mapping edit means the module changed under you**, which is
   the whole reason the goldens pin bytes rather than shapes.

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
