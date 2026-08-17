#!/usr/bin/env bash
# Boundary rule (ADR 0002): the wire model stays confined to internal/wire, so
# a models release is always a one-package edit.
#
# Enforced as two rules, because "depends on" means two different things and
# only one of them is achievable:
#
#   A. TRANSITIVE — sdk/... and internal/vendors/... must not reach the wire
#      model by any path at all. These are the contributor-facing layers; a
#      vendor adapter that could touch wire types even indirectly would make
#      every models release an every-adapter problem, which is the whole
#      failure ADR 0002 exists to prevent.
#
#   B. DIRECT — no package outside internal/wire may IMPORT the wire model.
#      This cannot be a transitive check: internal/app constructs the emitter
#      and cmd/collector builds the app, so both legitimately have the model in
#      their transitive closure. Requiring otherwise would forbid wiring the
#      emitter up at all. What matters is that no code outside internal/wire
#      names a wire type.
#
# An earlier version applied only rule A, and only to those two roots. That
# left internal/app, internal/cloudevents and internal/publish free to import
# openits-models directly with the lint still green — a rule reporting success
# over packages it never inspected.
#
# Matched on the REPO name, not a full import path. openits-models declares
# `module github.com/Vikasa2M/openits-models` today, but the model layer is
# intended to outlive this org — pinning one full import path here would
# silently stop matching if it ever moves, and a boundary lint that quietly
# matches nothing reads exactly like a boundary that is being respected.
set -euo pipefail
cd "$(dirname "$0")/.."

# Overridable so the rule itself is testable — see `make lint-boundary-selftest`.
# A lint that can never fail is indistinguishable from one that always passes.
forbidden="${LINT_FORBIDDEN:-openits-models}"

# Overridable so Rule C is testable against a fixture — see
# `make lint-boundary-replace-selftest`.
gomod="${LINT_GOMOD:-go.mod}"

errfile=$(mktemp)
trap 'rm -f "$errfile"' EXIT

module=$(go list -m)
allowed_prefix="$module/internal/wire"

fail=0
checked=0

# ---- Rule A: transitive, over the contributor-facing layers ----------------
for pkgroot in ./sdk/... ./internal/vendors/...; do
  # A `go list` failure must never read as "no violations found": a partial or
  # empty dep list would make the grep below vacuously clean.
  if ! deps=$(go list -deps "$pkgroot" 2>"$errfile"); then
    if grep -q 'matched no packages' "$errfile"; then
      continue # tree not populated yet; nothing to check
    fi
    echo "lint-boundary: go list -deps $pkgroot failed:" >&2
    sed 's/^/  /' "$errfile" >&2
    exit 2
  fi
  checked=$((checked + 1))
  if grep -q -- "$forbidden" <<<"$deps"; then
    echo "BOUNDARY VIOLATION (transitive): $pkgroot reaches $forbidden" >&2
    grep -- "$forbidden" <<<"$deps" | sort -u | sed 's/^/  /' >&2
    fail=1
  fi
done

# ---- Rule B: direct imports, everywhere except internal/wire ---------------
if ! pkgs=$(go list ./... 2>"$errfile"); then
  echo "lint-boundary: go list ./... failed:" >&2
  sed 's/^/  /' "$errfile" >&2
  exit 2
fi

direct=0
for pkg in $pkgs; do
  case "$pkg" in
    "$allowed_prefix" | "$allowed_prefix"/*) continue ;;
  esac
  # Test imports count: a fixture that names a wire type puts schema knowledge
  # outside internal/wire just as surely as production code does.
  if ! imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}{{range .TestImports}}{{println .}}{{end}}{{range .XTestImports}}{{println .}}{{end}}' "$pkg" 2>"$errfile"); then
    echo "lint-boundary: go list -f imports $pkg failed:" >&2
    sed 's/^/  /' "$errfile" >&2
    exit 2
  fi
  direct=$((direct + 1))
  if grep -q -- "$forbidden" <<<"$imports"; then
    echo "BOUNDARY VIOLATION (direct import): $pkg imports $forbidden" >&2
    grep -- "$forbidden" <<<"$imports" | sort -u | sed 's/^/  /' >&2
    fail=1
  fi
done

# ---- Rule C: no replace directive for the model module (ADR 0010) ----------
# The pin is a main-HEAD pseudo-version while both repos move in lockstep. A
# `replace` would make every developer's build depend on a local checkout, so
# CI would be testing a tree nobody else has. Rules A and B cannot see this:
# a replaced module still imports cleanly.
#
# Parsed textually rather than via `go mod edit -json` so the check stays
# offline and works against a fixture path.
replaces=$(awk '
  /^replace[[:space:]]*\(/ { inblock=1; next }
  inblock && /^\)/         { inblock=0; next }
  inblock                  { print; next }
  /^replace[[:space:]]/    { print }
' "$gomod")

if grep -q -- "$forbidden" <<<"$replaces"; then
  echo "BOUNDARY VIOLATION (replace directive): $gomod replaces $forbidden (ADR 0010)" >&2
  grep -- "$forbidden" <<<"$replaces" | sed 's/^/  /' >&2
  fail=1
fi

if [ "$checked" -eq 0 ] && [ "$direct" -eq 0 ]; then
  echo "lint-boundary: inspected 0 packages — the rule proved nothing" >&2
  exit 2
fi

if [ "$fail" -ne 0 ]; then
  echo "lint-boundary: FAILED against $forbidden" >&2
  exit 1
fi
echo "lint-boundary: clean ($checked roots transitively, $direct packages for direct imports) against $forbidden"
