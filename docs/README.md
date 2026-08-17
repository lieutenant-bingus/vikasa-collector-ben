# Documentation hub

Routed by what you're trying to do, not by which tier a document happens to
live in. If your task isn't listed, the rest of this directory is small
enough to skim: `adr/` (why), `reference/` (what), `specs/` (frozen
point-in-time design records), `notes/` and `plans/` (in-flight work).

| I want to... | Start here |
|---|---|
| See the repo for the first time | [`../README.md`](../README.md) — the one-diagram architecture and current Status |
| Add a vendor adapter | [`reference/starter-tasks.md`](reference/starter-tasks.md) for the highest-leverage first PR, then [`.claude/skills/add-vendor-adapter/`](../.claude/skills/add-vendor-adapter/SKILL.md) for the step-by-step workflow |
| Know what will fail my PR | [`reference/invariants.md`](reference/invariants.md) for the rules and what enforces them, [`reference/test-requirements.md`](reference/test-requirements.md) for the testing bar per contribution type |
| Understand why it's built this way | [`adr/README.md`](adr/README.md) — the accepted decision records, in order |
| Configure a deployment | [`reference/configuration.md`](reference/configuration.md) — every `collector.yaml` field: type, default, validation |

## Other task guides in `.claude/skills/`

- [`add-vendor-adapter`](../.claude/skills/add-vendor-adapter/SKILL.md) — new vendor × device-kind integrations
- [`add-domain-facet`](../.claude/skills/add-domain-facet/SKILL.md) — new facets, differs, and domain events
- [`wire-emitter`](../.claude/skills/wire-emitter/SKILL.md) — openits-models mappings and release pin bumps
- [`.claude/skills/README.md`](../.claude/skills/README.md) — the contract new skills are written against

A tutorial, four how-to guides, and an explanation tier promoted from the
specs below are tracked as follow-on work and will be linked here as they
land — this hub only points at documents that exist today.
