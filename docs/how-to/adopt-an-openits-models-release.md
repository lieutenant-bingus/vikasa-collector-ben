# Adopt an openits-models release

This is the guide for moving the openits-models pin forward and catching
`internal/wire/openits` up with whatever changed underneath it. It is not
the guide for adding a mapping that doesn't exist yet — that's
[`map-an-event-to-the-wire.md`](map-an-event-to-the-wire.md), and this
document sends you there for the parts the two share rather than
repeating them: the probe-before-you-trust-the-generated-code ritual, and
the mechanics of adding a `ceTypeFor` entry and an `Encode` case.

## The pin mechanism

The collector pins openits-models at a **semver release tag** — confirm it
yourself:

```
$ grep openits-models go.mod
	github.com/Vikasa2M/openits-models v0.3.0
```

[ADR 0018](../adr/0018-tagged-model-pins.md) is why, and the short version is
that the previous policy failed. Until 2026-08-23 the pin was a `main`-HEAD
pseudo-version ([ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md)),
paired with a review-only rule that it stay current. It didn't: the pin sat
two releases behind, past a breaking change, at a commit that was a CI
dependency bump in the models repo rather than a models change at all. A
stale branch pin has no observable moment of violation, so there was nothing
for review to catch.

Tagged pins make the shape checkable, and `scripts/lint-boundary.sh`'s Rule D
now fails the build on a pseudo-version. Adopting a release becomes a
deliberate, dated act with a changelog to read — which is the point, because
that changelog is where a `feat!` announces itself.

**What did not change:** a bump is still never a `replace` directive.
`scripts/lint-boundary.sh`'s Rule C fails the build on one in `go.mod` — see
[the "no `replace` directive" row in `invariants.md`](../reference/invariants.md#the-openits-models-pin-carries-no-replace-directive).

**What CI still will not tell you:** whether the tag you are on is the
*newest* tag. Rule D checks the version string's shape, never its recency —
that stays [a review item](../reference/invariants.md#the-pinned-release-is-a-current-one-not-an-old-tag).
Dependabot proposes gomod bumps after a 14-day cooldown, so a new release
usually arrives as a pull request, but the decision to adopt is yours.

**Versioned emitter packages are a separate question.** ADR 0010 tied
`internal/wire/openits_v1`-style packages to tagged pins; ADR 0018 untied
them. Tagged pins are here now, versioned packages are not, and they start
only when a fleet genuinely needs two models releases in one binary. Edit
`internal/wire/openits` in place.

## Procedure

1. **Check the current pin, then read what happened since.**
   `grep openits-models go.mod` gives the tag you're moving away from; the
   release notes between it and your target are the cheapest warning you
   will get about a breaking change. A `feat!` there means step 2 is not
   optional.
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
   go get github.com/Vikasa2M/openits-models@vX.Y.Z
   ```

   The chosen tag, explicitly — never `@main`, and never `-u`, which would
   move every other dependency at the same time and bury the models change
   in the diff. Only one models release is ever compiled in, so edit the
   existing `internal/wire/openits` package in place; the version-suffixed
   layout is not what tagged pinning turns on (see above).
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

See [`map-an-event-to-the-wire.md`'s Verify
section](map-an-event-to-the-wire.md#verify) for what each command checks
and why. Worth calling out for a pin bump specifically: `make check`'s
`scripts/lint-boundary.sh` run is also what catches a `replace` directive
left behind from local debugging during the bump — the exact shortcut ADR
0010 still forbids — and Rule D, which fails if the pin came out as a
pseudo-version because `@main` slipped into the `go get`.

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

The v0.3.0 adoption is the worked example of why. It moved two things no
compiler could see: the `object-class` identity hierarchy was hoisted from
`openits-perception-types` into `openits-types`, so nine identityref strings
the emitter builds by concatenation were silently pointing at identities that
no longer existed; and `openits-perception-events` revved, so four ce-types'
`ce-dataschema` constants were pointing at a superseded registry revision.
Both build cleanly. Both were found by probing the module and diffing the
schema-registry revisions, exactly as steps 2 and 4 describe.
