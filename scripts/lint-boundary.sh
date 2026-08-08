#!/usr/bin/env bash
# Boundary rule (ADR 0002): sdk/ and internal/vendors/ must never depend on
# the wire model. Only internal/wire/ may.
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

errfile=$(mktemp)
trap 'rm -f "$errfile"' EXIT

fail=0
for pkgroot in ./sdk/... ./internal/vendors/...; do
  # A `go list` failure must never read as "no violations found": a partial
  # or empty dep list would make any grep below vacuously clean.
  if ! deps=$(go list -deps "$pkgroot" 2>"$errfile"); then
    if grep -q 'matched no packages' "$errfile"; then
      continue # tree not populated yet; nothing to check
    fi
    echo "lint-boundary: go list -deps $pkgroot failed:" >&2
    sed 's/^/  /' "$errfile" >&2
    exit 2
  fi
  if grep -q -- "$forbidden" <<<"$deps"; then
    echo "BOUNDARY VIOLATION: $pkgroot depends on $forbidden" >&2
    grep -- "$forbidden" <<<"$deps" | sort -u | sed 's/^/  /' >&2
    fail=1
  fi
done
exit $fail
