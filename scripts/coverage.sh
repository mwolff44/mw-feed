#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Shows the test coverage per package and in total.
#
#   scripts/coverage.sh                        run the tests, print the table
#   scripts/coverage.sh --profile FILE         read a profile instead of running
#   scripts/coverage.sh --markdown             a Markdown table (job summary)
#   scripts/coverage.sh --html FILE            also write the line-by-line report
#
# Coverage counts a line as covered when ANY test of the module runs it
# (-coverpkg=./...): the store is mostly exercised by the tests of the feed, web
# and auth packages, so measuring each package by its own tests alone would
# make it look untested.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
profile="" markdown=no html=""

while [ $# -gt 0 ]; do
  case "$1" in
    --profile)  [ $# -ge 2 ] || { echo "coverage: --profile needs a file" >&2; exit 2; }; profile="$2"; shift 2 ;;
    --markdown) markdown=yes; shift ;;
    --html)     [ $# -ge 2 ] || { echo "coverage: --html needs a file" >&2; exit 2; }; html="$2"; shift 2 ;;
    *) echo "coverage: unknown option \"$1\" (known: --profile FILE, --markdown, --html FILE)" >&2; exit 2 ;;
  esac
done

cd "$root/src"
if [ -n "$profile" ]; then
  [ -r "$profile" ] || { echo "coverage: cannot read the profile $profile" >&2; exit 1; }
else
  profile="$(mktemp)"
  trap 'rm -f "$profile"' EXIT
  go test -count=1 -coverpkg=./... -coverprofile="$profile" ./... >&2
fi

module="$(go list -m)"

# A block counts once, and as covered if any test ran it.
table="$(awk -v module="$module/" '
  NR == 1 && /^mode:/ { next }
  {
    key = $1; stmts[key] = $2
    if ($3 > 0) hit[key] = 1
  }
  END {
    for (key in stmts) {
      file = key; sub(/:.*/, "", file)
      sub("^" module, "", file); sub(/\/[^\/]*$/, "", file)
      total[file] += stmts[key]; if (key in hit) covered[file] += stmts[key]
      allTotal += stmts[key]; if (key in hit) allCovered += stmts[key]
    }
    for (pkg in total) printf "%s %d %d %.1f\n", pkg, total[pkg], covered[pkg], 100 * covered[pkg] / total[pkg]
    printf "~TOTAL %d %d %.1f\n", allTotal, allCovered, (allTotal ? 100 * allCovered / allTotal : 0)
  }' "$profile" | LC_ALL=C sort)"

if [ "$markdown" = yes ]; then
  echo "### Test coverage"
  echo
  echo "| Package | Statements | Covered | Coverage |"
  echo "|---------|-----------:|--------:|---------:|"
  while read -r pkg stmts covered pct; do
    if [ "$pkg" = "~TOTAL" ]; then echo "| **Total** | **$stmts** | **$covered** | **$pct%** |"; else echo "| $pkg | $stmts | $covered | $pct% |"; fi
  done <<< "$table"
  echo
  echo "_A line counts as covered when any test of the module runs it._"
else
  printf '%-28s %10s %8s %8s\n' PACKAGE STATEMENTS COVERED COVERAGE
  while read -r pkg stmts covered pct; do
    if [ "$pkg" = "~TOTAL" ]; then pkg=TOTAL; fi
    printf '%-28s %10d %8d %7s%%\n' "$pkg" "$stmts" "$covered" "$pct"
  done <<< "$table"
fi

if [ -n "$html" ]; then
  go tool cover -html="$profile" -o "$html"
  echo "coverage: HTML report written to $html" >&2
fi
