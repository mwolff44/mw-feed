#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Tests the Git hooks of .githooks/ in a throwaway repository: the pre-commit
# hook must refuse a staged secret, unformatted Go code and a missing gitleaks,
# and accept clean work; the pre-push hook must run scripts/ci-local.sh and
# honour CI_LOCAL_SKIP.
#
#   scripts/test-hooks.sh
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
failures=0

ok()   { echo "ok   - $1"; }
fail() { echo "FAIL - $1" >&2; failures=$((failures + 1)); }

# A repository with the project's hooks installed.
newrepo() {
  rm -rf "$work/repo" && mkdir -p "$work/repo" && cd "$work/repo" || exit 1
  git init -q . && git config user.name test && git config user.email test@example.invalid && git config commit.gpgsign false
  mkdir -p .githooks scripts
  cp "$root"/.githooks/* .githooks/ 2>/dev/null
  cp "$root"/scripts/ci-local.sh scripts/ 2>/dev/null
  git config core.hooksPath .githooks
}

# A made-up secret, written into the throwaway repository only. The marker on
# the next line tells gitleaks that this line of the test is deliberate; the
# file written into the throwaway repository carries no marker, so it is found.
fake_secret() { printf '%s%s%s' 'aws_secret_access_key = "' 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYzR3dKmQ9sZ' '"'; } # gitleaks:allow

# 1. A clean commit goes through.
newrepo
echo "hello" > README.md && git add README.md
if git commit -q -m clean >/dev/null 2>&1; then ok "a clean commit is accepted"; else fail "a clean commit was refused"; fi

# 2. A staged secret is refused and nothing is committed.
newrepo
echo "base" > a.txt && git add a.txt && git commit -q -m base --no-verify
fake_secret > config.env && git add config.env
if out="$(git commit -m leak 2>&1)"; then fail "a staged secret was accepted"; else
  if [ "$(git rev-list --count HEAD)" = 1 ]; then ok "a staged secret is refused and not committed"; else fail "the refused commit was created anyway"; fi
  case "$out" in *wJalrXUtnFEMI*) fail "the secret was printed in clear: $out" ;; *) ok "the refusal does not print the secret" ;; esac
fi

# 3. Without gitleaks the hook fails with a clear message instead of skipping the check.
newrepo
echo "x" > b.txt && git add b.txt
bin="$work/bin"; mkdir -p "$bin"
for tool in git bash; do ln -sf "$(command -v "$tool")" "$bin/$tool"; done
if out="$(PATH="$bin" git commit -m nogitleaks 2>&1)"; then fail "the commit went through without gitleaks"; else
  case "$out" in *gitleaks*) ok "a missing gitleaks is reported" ;; *) fail "no clear message when gitleaks is missing: $out" ;; esac
fi

# 4. Unformatted staged Go code is refused.
newrepo
mkdir src && printf 'module example.invalid/x\n\ngo 1.27\n' > src/go.mod
printf 'package x\nfunc  F( ){ }\n' > src/x.go && git add src
if out="$(git commit -m badfmt 2>&1)"; then fail "unformatted Go code was accepted"; else
  case "$out" in *x.go*) ok "unformatted Go code is refused and named" ;; *) fail "gofmt refusal does not name the file: $out" ;; esac
fi

# 5. Formatted Go code that vets cleanly goes through.
newrepo
mkdir src && printf 'module example.invalid/x\n\ngo 1.27\n' > src/go.mod
printf 'package x\n\n// F does nothing.\nfunc F() {}\n' > src/x.go && git add src
if git commit -q -m goodfmt >/dev/null 2>&1; then ok "formatted Go code is accepted"; else fail "formatted Go code was refused"; fi

# 6. ci-local.sh refuses a name it does not know.
newrepo
if CI_LOCAL_SKIP=bogus scripts/ci-local.sh >/dev/null 2>&1; then fail "ci-local.sh accepted an unknown CI_LOCAL_SKIP name"; else ok "ci-local.sh refuses an unknown skip name"; fi

# 7. The pre-push hook runs ci-local.sh and says what was skipped.
newrepo
echo "x" > c.txt && git add c.txt && git commit -q -m c --no-verify
git init -q --bare "$work/remote.git" && git remote add origin "$work/remote.git"
if out="$(CI_LOCAL_SKIP=go,e2e,docker git push -q origin HEAD:main 2>&1)"; then
  case "$out" in *skipped*go*e2e*docker*|*skipped*) ok "pre-push runs ci-local.sh and reports what was skipped" ;; *) fail "pre-push did not report the skipped steps: $out" ;; esac
else fail "pre-push failed although every step was skipped: $out"; fi
if out="$(CI_LOCAL_SKIP=bogus git push -q origin HEAD:other 2>&1)"; then fail "pre-push let an invalid CI_LOCAL_SKIP through"; else ok "pre-push stops when ci-local.sh fails"; fi

echo
if [ "$failures" -eq 0 ]; then echo "all hook tests passed"; else echo "$failures hook test(s) failed" >&2; fi
exit "$failures"
