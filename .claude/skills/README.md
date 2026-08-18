# Skill contract

Every `SKILL.md` in this directory shares one structure, so the seventh
skill matches the first. An agent reading a skill will not notice a missing
section on its own — it will just proceed without the guardrail — so the
shape is enforced mechanically: `scripts/lint-docs.sh`, run by `make check`,
checks that any skill declaring the contract (see "Opting in," below)
carries every section below except frontmatter.

## The six parts

1. **Frontmatter** — `name`, and a `description` naming explicit trigger
   phrases (the concrete requests that should cause an agent to reach for
   this skill, not a generic summary of its topic).
2. **`## When this applies`** — and, as important, when it does not.
3. **`## Invariants`** — links into
   [`docs/reference/invariants.md`](../../docs/reference/invariants.md)
   rows. Never restated here: if a rule changes, this file changing along
   with it silently is exactly the drift the reference tier exists to
   prevent.
4. **`## Procedure`** — an ordered checklist.
5. **`## Verify`** — literal commands, with the expected result.
6. **`## Canonical doc`** — a link to the how-to guide holding the full
   narrative. Skills stay terse — trigger, guardrails, procedure — and
   leave the explanation to the doc they point at.

## Opting in

`scripts/lint-docs.sh` only holds a skill to sections 2–6 once the skill's
frontmatter declares `contract: v1`. Every skill in this directory declares
it today. A skill that doesn't would be listed as non-conforming rather
than either silently skipped or failing the build — but that's a
mechanism for a future skill that hasn't been retargeted yet, not the
current state. A new skill should declare `contract: v1` and follow the
shape from the start.

## Template

```markdown
---
name: <skill-name>
description: <what it does, and the explicit phrases that should trigger it>
contract: v1
---

# <Skill title>

## When this applies

...and when it does not.

## Invariants

- [Rule name](../../docs/reference/invariants.md#anchor) — one line on how
  this skill's procedure respects it.

## Procedure

1. ...
2. ...

## Verify

```bash
<literal command>
```

Expected: <result>.

## Canonical doc

[<how-to title>](<path>) — the full narrative.
```
