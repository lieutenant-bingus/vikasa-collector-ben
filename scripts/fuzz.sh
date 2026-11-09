#!/usr/bin/env bash
# Runs every native Go fuzz target in the module for FUZZTIME each (default
# 10s). `make check` already runs each target's seed corpus as an ordinary
# test; this adds coverage-guided mutation on top. CI runs it briefly on every
# change; run it longer locally, e.g. `make fuzz FUZZTIME=10m`.
#
# Targets are discovered, not listed, so a new Fuzz function cannot be left
# out. A crash writes its input under the package's testdata/fuzz/; commit it
# with the fix so it stays in the seed corpus as a regression test.
set -euo pipefail
cd "$(dirname "$0")/.."

fuzztime="${FUZZTIME:-10s}"
found=0
while IFS=: read -r file name; do
  dir="./$(dirname "$file")"
  echo "fuzz: $dir $name ($fuzztime)"
  go test "$dir" -run '^$' -fuzz "^${name}\$" -fuzztime "$fuzztime"
  found=$((found + 1))
done < <(grep -rEo --include='*_test.go' '^func (Fuzz[A-Za-z0-9_]*)' --exclude-dir=.git . \
  | sed -E 's/:func /:/' | sed 's|^\./||' | sort)

if [ "$found" -eq 0 ]; then
  echo "fuzz: found no fuzz targets — the discovery proved nothing" >&2
  exit 2
fi
echo "fuzz: $found targets clean"
