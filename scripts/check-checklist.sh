#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Checks docs/CHECKLIST.md against the code: every Go test it names must exist,
# and so must every browser-test file. It does not run them (go test does).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checklist="$root/docs/CHECKLIST.md"
status=0

while read -r name; do
  if ! grep -rqE "^func ${name}\(" --include='*_test.go' "$root/src"; then
    echo "missing Go test: $name" >&2
    status=1
  fi
done < <(grep -oE '`Test[A-Za-z0-9_]+`' "$checklist" | tr -d '`' | sort -u)

while read -r file; do
  if [ ! -e "$root/e2e/tests/$file" ] && [ ! -e "$root/e2e/unit/$file" ]; then
    echo "missing browser-test file: $file" >&2
    status=1
  fi
done < <(grep -oE '`[a-z0-9-]+(\.spec|\.test)\.js`' "$checklist" | tr -d '`' | sort -u)

[ "$status" -eq 0 ] && echo "checklist ok: $(grep -oE '`Test[A-Za-z0-9_]+`' "$checklist" | sort -u | wc -l) Go tests, $(grep -oE '`[a-z0-9-]+(\.spec|\.test)\.js`' "$checklist" | sort -u | wc -l) browser-test files"
exit "$status"
