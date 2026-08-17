#!/usr/bin/env bash
# Two structural checks on documentation.
#
#   A. Every relative markdown link under docs/ resolves to a real file. The
#      documentation tiers link heavily by design — rules live in exactly one
#      place and everything else points at it — so a broken link is not a
#      cosmetic defect, it is a rule becoming unreachable.
#
#   B. Every SKILL.md carries the sections the skill contract requires. Skills
#      are read by agents that will not notice a missing section; they will
#      just proceed without the guardrail.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
checked=0

# ---- A: relative links resolve ---------------------------------------------
# Fenced code blocks are skipped. Documents that TEACH markdown — this repo's
# plans and skills show example link syntax — would otherwise be flagged for
# links that were never meant to resolve from the containing file's directory.
strip_fences() {
  awk '/^[[:space:]]*```/ { infence = !infence; next } !infence { print }' "$1"
}

while IFS= read -r md; do
  dir=$(dirname "$md")
  # [text](target) where target is neither absolute nor a URL nor an anchor.
  # The grep's own exit status is neutralized with `|| true`: under
  # pipefail, a doc with zero matching links would otherwise make grep's
  # "no match" status (1) win the pipeline even though every later stage
  # succeeds, turning "this doc has no links" into a silent, unexplained
  # FAILED with no BROKEN LINK line to explain why.
  strip_fences "$md" | { grep -oE '\]\([^)#][^)]*\)' || true; } | sed 's/^](//; s/)$//' | while read -r link; do
    case "$link" in
      http*://* | mailto:*) continue ;;
    esac
    target="${link%%#*}"
    [ -z "$target" ] && continue
    if [ ! -e "$dir/$target" ]; then
      echo "BROKEN LINK: $md -> $link" >&2
      exit 1
    fi
  done || fail=1
  checked=$((checked + 1))
done < <({ find docs -name '*.md' -not -path 'docs/specs/*'; \
           ls README.md CONTRIBUTING.md AGENTS.md CODE_OF_CONDUCT.md SECURITY.md; })

# ---- B: skill structure -----------------------------------------------------
required=("## When this applies" "## Invariants" "## Procedure" "## Verify" "## Canonical doc")
skills=0
for skill in .claude/skills/*/SKILL.md; do
  [ -e "$skill" ] || continue
  skills=$((skills + 1))

  # Skills opt in by declaring the contract version. Retargeting the
  # pre-contract skills is Phase 2; until then they are listed as
  # non-conforming rather than silently skipped.
  if ! grep -qF 'contract: v1' "$skill"; then
    echo "lint-docs: $skill predates the skill contract (Phase 2 retargets it)"
    continue
  fi

  for section in "${required[@]}"; do
    if ! grep -qF "$section" "$skill"; then
      echo "SKILL MISSING SECTION: $skill lacks '$section'" >&2
      fail=1
    fi
  done
done

if [ "$checked" -eq 0 ] || [ "$skills" -eq 0 ]; then
  echo "lint-docs: inspected $checked docs and $skills skills — proved nothing" >&2
  exit 2
fi

if [ "$fail" -ne 0 ]; then
  echo "lint-docs: FAILED" >&2
  exit 1
fi
echo "lint-docs: clean ($checked docs, $skills skills)"
