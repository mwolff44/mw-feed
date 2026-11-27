#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Tests scripts/coverage.sh against the real code base. The expected total comes
# from `go tool cover`, which computes it on its own.
#
#   scripts/test-coverage.sh
#   COVER_PROFILE=src/cover.out scripts/test-coverage.sh   # reuse a profile
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
failures=0
ok()   { echo "ok   - $1"; }
fail() { echo "FAIL - $1" >&2; failures=$((failures + 1)); }

if [ -n "${COVER_PROFILE:-}" ]; then # a profile that CI has just made: no need to run the tests again
  cp "$COVER_PROFILE" "$work/cover.out"
else
  (cd "$root/src" && go test -count=1 -coverpkg=./... -coverprofile="$work/cover.out" ./... >/dev/null 2>&1) \
    || { echo "the tests themselves fail: fix them first" >&2; exit 1; }
fi

expected="$(cd "$root/src" && go tool cover -func="$work/cover.out" | awk '/^total:/ {print $NF}' | tr -d '%')"

# 1. The plain table ends with a total that agrees with go tool cover. It may
# not match to the digit: `go tool cover -func` leaves out the few statements
# that sit outside any function (initialisers of package variables, 17 of 2716
# when this was written), which the HTML report and `go test -cover` count and
# so does coverage.sh. The tolerance is far smaller than any real mistake.
plain="$("$root/scripts/coverage.sh" --profile "$work/cover.out" 2>&1)" || fail "coverage.sh failed on a good profile: $plain"
got="$(echo "$plain" | awk '/^TOTAL/ {print $NF}' | tr -d '%')"
if [ -n "$got" ] && awk -v a="$got" -v b="$expected" 'BEGIN { d = a - b; if (d < 0) d = -d; exit !(d <= 0.5) }'; then
  ok "the total ($got%) agrees with go tool cover ($expected%) within 0.5"
else fail "total is '$got', go tool cover says $expected"; fi

# 2. Every package of the module that has code is listed, with a percentage.
for pkg in cmd/mw-feed internal/store internal/web internal/feed; do
  if echo "$plain" | grep -qE "^$pkg +[0-9]+ +[0-9]+ +[0-9.]+%"; then ok "$pkg is listed"; else fail "$pkg is missing from the table:
$plain"; fi
done

# 3. The Markdown form is a table with a total, for the job summary.
md="$("$root/scripts/coverage.sh" --profile "$work/cover.out" --markdown 2>&1)"
case "$md" in
  *"| Package |"*"| internal/store |"*"**Total"*) ok "the Markdown output has a table and a total" ;;
  *) fail "the Markdown output is not a table with a total:
$md" ;;
esac

# 4. The HTML report is written where asked.
"$root/scripts/coverage.sh" --profile "$work/cover.out" --html "$work/cover.html" >/dev/null 2>&1
if [ -s "$work/cover.html" ] && grep -q "<html" "$work/cover.html"; then ok "the HTML report is written"; else fail "no HTML report at the requested path"; fi

# 5. Bad use is refused.
if "$root/scripts/coverage.sh" --bogus >/dev/null 2>&1; then fail "an unknown option was accepted"; else ok "an unknown option is refused"; fi
if "$root/scripts/coverage.sh" --profile "$work/missing.out" >/dev/null 2>&1; then fail "a missing profile was accepted"; else ok "a missing profile is refused"; fi

echo
if [ "$failures" -eq 0 ]; then echo "all coverage tests passed"; else echo "$failures coverage test(s) failed" >&2; fi
exit "$failures"
