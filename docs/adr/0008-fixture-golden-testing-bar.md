# ADR 0008: Fixture-golden testing bar for adapters

**Status:** Accepted (2026-07-12)

## Context
A zoo of contributed adapters is only maintainable if adapters are
reviewable and regression-testable without vendor hardware on the
reviewer's desk.

## Decision
Every adapter PR ships fixtures: recorded raw transport responses (SNMP
values by OID, API JSON bodies) plus the expected `model.Snapshot`/events.
`sdk/transport` packages ship test fakes that replay fixtures. **No
fixtures, no merge.** Core guards: table-driven synth differ tests with
fixed timestamps; emitter goldens (domain event → exact bytes + ce-type);
emitter catalog-conformance (every producible ce-type must exist in the
pinned models release's asyncapi.yaml); byte-literal subject goldens; and
the CI boundary lint (ADR 0002).

## Consequences
Adapter review = read the mapping + eyeball fixtures; hardware-free CI;
drift between collector and models catalog is a CI failure, not a
production surprise. Cost: recording fixtures is real work on first
integration — accepted as the price of admission.

## Alternatives considered
Mock-heavy unit tests (rejected: they test the mock); live-device
integration environments as the bar (rejected: unavailable to most
contributors and to CI).
