# Record fixtures from a device

*Stub. The tool this guide would document does not exist yet.*

**Owner:** successor A — a fixture recorder and adapter conformance kit,
tracked as its own piece of follow-on work, not part of this
documentation effort.

## What's missing, plainly

ADR 0008 requires an adapter's fixtures to be recorded raw transport
responses, not hand-typed values — see
["No fixtures, no merge" in `invariants.md`](../reference/invariants.md#no-fixtures-no-merge).
No tool in this repo captures a real device session into a fixture; what
that means for the fixtures that do exist is
[`testing-strategy.md`'s "Fixture replay: reproducible, not verified"
section](../explanation/testing-strategy.md#fixture-replay-reproducible-not-verified).
The recorded-fixture bar cannot currently be met by anything this repo
provides — see
[`docs/README.md`'s known-gaps entry](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state)
for the full account, including that `ntcip-asc`'s own `healthyFixture` is
a hand-typed map and its alarm-bitmap table has never been validated
against a physical controller.

## What to do meanwhile

Capture a real session by hand and document its provenance in the
fixture's own comment — what you captured, from what device or simulator,
and when.
[`add-a-vendor-adapter.md`'s "Meet the test bar"
section](add-a-vendor-adapter.md#meet-the-test-bar) already walks through
this in full for the one place it currently matters, a new adapter's
fixtures, and none of that guidance is adapter-specific — it applies here
unchanged until a recorder exists. Why the comment is what actually
counts, not the file it's written in, is
[`testing-strategy.md`'s "Provenance is a review question, not a
test" section](../explanation/testing-strategy.md#provenance-is-a-review-question-not-a-test).
