# Adopt an openits-models release

This is the guide for moving the openits-models pin forward and catching
`internal/wire/openits` up with whatever changed underneath it. It is not
the guide for adding a mapping that doesn't exist yet — that's
[`map-an-event-to-the-wire.md`](map-an-event-to-the-wire.md), and this
document sends you there for the parts the two share rather than
repeating them: the probe-before-you-trust-the-generated-code ritual, and
the mechanics of adding a `ceTypeFor` entry and an `Encode` case.

## The pin mechanism

The collector pins openits-models at a **main-HEAD pseudo-version**, not a
tagged release — confirm it yourself:

```
$ grep openits-models go.mod
github.com/Vikasa2M/openits-models v0.2.3-0.20260807005833-235e8780f44c
```

That string is what `go get github.com/Vikasa2M/openits-models@main`
resolved to on the day of the last bump: a real, reproducible module
version (a pseudo-version names an immutable commit) that happens to look
nothing like a semver tag. [ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md)
is why: both repos are owned by the same team, pre-v1, moving in lockstep,
and requiring a tag per pin would turn every models fix found while
writing an emitter into a release round trip. The rule holds until
openits-models reaches v1.0.0, or sooner if any of ADR 0010's three expiry
triggers fires first (an outside consumer pins openits-models, the
collector needs two model versions compiled side by side, or
openits-models adopts a compatibility promise between releases) — at that
point the collector returns to tagged pins and a versioned
`internal/wire/<version>` package layout with no further ADR needed.

**What did not change:** a bump is still never a `replace` directive.
`scripts/lint-boundary.sh`'s Rule C fails the build on one in `go.mod`,
lockstep or not — see
[the two openits-models rows in `invariants.md`](../reference/invariants.md#the-openits-models-pin-carries-no-replace-directive)
for both halves stated precisely, including which half is machine-enforced
and which is `Review (manual)` today.

## Procedure

1. **Check the current pin.** `grep openits-models go.mod` — the version
   you're moving away from.
2. **Probe the new module before writing any code.** openits-models'
   prose and its generated code have disagreed before, so treat the
   pinned module as ground truth, not its docs. Follow
   [`map-an-event-to-the-wire.md`'s "Ground truth: probe, don't read"
   section](map-an-event-to-the-wire.md#ground-truth-probe-dont-read) —
   resolve the *new* version in a scratch module and run it, don't just
   read the diff. Diff the release's `bindings/nats/asyncapi.yaml`
   ce-type set against the emitter's current `CETypes()`
   (`internal/wire/openits/emitter.go`) so you know which ce-types are
   newly available and which existing mappings the release could have
   moved underneath — enum renumbering and `identityref` spelling changes
   don't show up as a Go compile error.
3. **Move the pin.**

   ```bash
   go get -u github.com/Vikasa2M/openits-models@main
   ```

   While lockstep holds, only one models release is ever compiled in, so
   edit the existing `internal/wire/openits` package in place — do not
   create a version-suffixed package for this. That layout starts at the
   first *tagged* pin, per ADR 0010.
4. **Adjust mappings and `ce-dataschema` constants** for anything the
   probe in step 2 turned up, and claim any newly-available ce-types that
   were previously dropping — the emitter's drop warnings name the
   candidates. Adding an individual mapping follows
   [`map-an-event-to-the-wire.md`'s "Adding a ce-type mapping"
   steps](map-an-event-to-the-wire.md#adding-a-ce-type-mapping) exactly;
   this document doesn't repeat that checklist.
5. **Re-run the goldens and treat every diff as a decision.**

   ```bash
   go test ./internal/wire/openits/... -run TestGoldens -v
   ```

   A golden that moved without a mapping edit means the module changed
   under you, not that the golden needs regenerating — confirm each diff
   against the actual models change before updating the pinned bytes.
   [`testing-strategy.md`'s golden-tests section](../explanation/testing-strategy.md#golden-tests-pin-bytes-not-meaning)
   explains why byte-exact goldens are the right shape for this and what
   regenerating without checking throws away. `TestGoldensCoverEveryCEType`
   (same file) fails the build if a ce-type reachable through `CETypes()`
   has no golden case — a newly-claimed mapping needs a golden case added,
   not just the routing entry.
6. **If the fleet genuinely needs two models releases compiled at once**
   (ADR 0002's S2 scenario, arriving for real), copy the emitter to
   `internal/wire/<newversion>` instead of editing in place, and update
   the `model_version` default only when the fleet is ready to move — two
   packages coexisting is the normal state during a migration, not
   something to rush past. This is rare during lockstep; most bumps are
   step 3 above, in place.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

`make check` runs `go vet`, the full suite, and `scripts/lint-boundary.sh`
— the same gate that fails the build on a stray `replace` directive or on
anything outside `internal/wire` reaching for an openits-models type.
`go test ./... -race` matters here because emitters are called
concurrently, one goroutine per polled device. `gofmt -l .` should print
nothing.

## What this does not check for you

Nothing in this repo verifies that the emitter's mapping table still
agrees with the *newly pinned* release's own catalog — the goldens only
prove the mapping matches what was recorded at the last bump, and
`TestGoldensCoverEveryCEType` only proves the goldens keep up with the
emitter's own routing table, not with openits-models. That gap is
tracked, not silent — see
[`docs/README.md`'s known-gaps entry for it](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state).
Step 2's probe is the only thing standing in for that check today, which
is why it isn't optional.
