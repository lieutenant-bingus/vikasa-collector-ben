# Record fixtures from a device

*Stub. The tool this guide would document does not exist yet.*

**Owner:** successor A — a fixture recorder and adapter conformance kit,
tracked as its own piece of follow-on work, not part of this
documentation effort.

## What's missing, plainly

ADR 0008 requires an adapter's fixtures to be recorded raw transport
responses, not hand-typed values — see
["No fixtures, no merge" in `invariants.md`](../reference/invariants.md#no-fixtures-no-merge).
This repo has no tool that captures a real device session into a fixture,
and no `testdata/` mechanism of any kind: every fixture, including the
one shipping adapter's, is a Go literal committed alongside the test that
uses it. That means the recorded-fixture bar cannot currently be met by
anything this repo provides — see
[`docs/README.md`'s known-gaps entry](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state)
for the full account, including that `ntcip-asc`'s own `healthyFixture` is
a hand-typed map and its alarm-bitmap table has never been validated
against a physical controller.

## What to do meanwhile

Capture a real session by hand: run the adapter's transport against the
actual device (or the vendor's simulator, if one exists), record the raw
values it returns, and commit them as the Go literal this repo's fixtures
already take the form of. Then write, in the fixture's own comment, what
you captured, from what device or simulator, and when — that comment is
what a reviewer will actually judge the fixture on.
[`testing-strategy.md`'s "Provenance is a review question, not a
test" section](../explanation/testing-strategy.md#provenance-is-a-review-question-not-a-test)
is why: file format can't distinguish a recording from an invention, so
provenance is the only thing that can, and it explains what to write and
what a reviewer looks for.

[`add-a-vendor-adapter.md`](add-a-vendor-adapter.md) covers the rest of an
adapter contribution's testing bar; this stub only covers the fixture
step it currently can't hand you a tool for.
