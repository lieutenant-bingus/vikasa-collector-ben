#!/usr/bin/env bash
# Two structural checks on documentation.
#
#   A. Every relative markdown link under docs/, in the root documents, in
#      .claude/skills/, and in the pull-request template resolves to a real
#      file, and
#      every #fragment on a link to a local markdown file matches a real
#      heading in that file (GitHub's slug rules: lowercase, punctuation
#      dropped, spaces to hyphens). The documentation tiers link heavily by
#      design — rules live in exactly one place and everything else points
#      at it — so a broken link, or a link whose heading moved out from
#      under it, is not a cosmetic defect, it is a rule becoming
#      unreachable.
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

# heading_slugs prints one GitHub-style slug per ATX heading ("# ".."######")
# in the given file, fenced code blocks excluded (reuses strip_fences so a
# "#" inside an example code block is never mistaken for a real heading).
heading_slugs() {
  strip_fences "$1" \
    | grep -E '^#{1,6}[[:space:]]' \
    | sed -E 's/^#{1,6}[[:space:]]+//; s/[[:space:]]+$//' \
    | while IFS= read -r heading; do
        printf '%s\n' "$heading" \
          | tr '[:upper:]' '[:lower:]' \
          | sed -E 's/[^a-z0-9 _-]//g; s/[[:space:]]+/ /g; s/^ //; s/ $//; s/ /-/g'
      done
}

# anchor_exists checks whether $2, a #fragment already stripped of its
# leading "#", matches some heading's slug in file $1.
#
# Deliberately not "heading_slugs "$1" | grep -Fxq -- "$2"": under
# `set -o pipefail` a "-q" grep that matches early closes the pipe before
# heading_slugs finishes writing, so heading_slugs can exit SIGPIPE (141)
# and pipefail reports that as the pipeline's failure even though the match
# was found — turning a real anchor into a false BROKEN ANCHOR. Reading via
# process substitution and returning from inside the loop avoids ever
# putting heading_slugs on the failing side of a pipe.
anchor_exists() {
  local file="$1" frag="$2" slug
  while IFS= read -r slug; do
    if [ "$slug" = "$frag" ]; then
      return 0
    fi
  done < <(heading_slugs "$file")
  return 1
}

# `.github/pull_request_template.md` is in the set because invariants.md's own
# preamble names it as one of the files that LINKS a rule rather than
# restating it. It carries three anchor links into that table, so renaming a
# heading there breaks it exactly as it would break any other linker -- but it
# sat outside this scan, so it was the one reference that would have rotted
# silently while every other one failed loudly. Its depth differs again: it
# lives one level below the root, so a link into docs/ starts `../`.
#
# `.claude/skills/**/*.md` is in the set for the same reason `docs/` is: the
# skills link into `docs/reference/invariants.md` and into the how-to guides
# by relative path, and a skill is read by an agent that will follow a dead
# link without noticing. Their depth differs from every other scanned file —
# a SKILL.md sits two levels below the root, so a link into docs/ starts
# `../../../` — which is exactly the mistake this catches.
#
# The root documents are listed with printf, not `ls`. A process substitution
# runs in a subshell whose exit status `set -e` never sees, so an `ls` that
# failed because one of the five was renamed would abort the list at that point
# and silently drop the rest -- fewer documents checked, still "clean". printf
# cannot fail that way, and a genuinely missing file now surfaces as a real
# error from the loop body instead of as a shorter list.
while IFS= read -r md; do
  if [ ! -f "$md" ]; then
    echo "MISSING DOCUMENT: $md is listed as a required document but does not exist" >&2
    fail=1
    continue
  fi
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
    # A fragment on a link to a local markdown file is a heading reference,
    # not just a file reference — check it resolves too. Anything that is
    # not a markdown file (or carries no fragment at all) cannot be parsed
    # for headings, so it is left to the file-existence check above only.
    case "$target" in
      *.md)
        frag="${link#*#}"
        if [ "$frag" != "$link" ] && [ -n "$frag" ]; then
          if ! anchor_exists "$dir/$target" "$frag"; then
            echo "BROKEN ANCHOR: $md -> $link (no heading in $dir/$target slugs to '#$frag')" >&2
            exit 1
          fi
        fi
        ;;
    esac
  done || fail=1
  checked=$((checked + 1))
done < <({ find docs -name '*.md' -not -path 'docs/specs/*'; \
           find .claude/skills -name '*.md'; \
           printf '%s\n' README.md CONTRIBUTING.md AGENTS.md CODE_OF_CONDUCT.md SECURITY.md \
                         .github/pull_request_template.md; })

# ---- B: skill structure -----------------------------------------------------
required=("## When this applies" "## Invariants" "## Procedure" "## Verify" "## Canonical doc")
skills=0
for skill in .claude/skills/*/SKILL.md; do
  [ -e "$skill" ] || continue
  skills=$((skills + 1))

  # Skills opt in by declaring the contract version. All five skills in this
  # repo were retargeted to it during the documentation Phase 2 effort, so
  # this branch is inert today — but it stays rather than being deleted, as
  # a safety net for whatever skill shows up next without the declaration:
  # such a skill is listed as non-conforming here, not silently skipped.
  if ! grep -qF 'contract: v1' "$skill"; then
    echo "lint-docs: $skill predates the skill contract"
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
