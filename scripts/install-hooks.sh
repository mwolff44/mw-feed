#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Turns on the Git hooks of .githooks/ for this clone (run it once).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chmod +x "$root"/.githooks/* "$root"/scripts/*.sh
git -C "$root" config core.hooksPath .githooks
echo "Git hooks enabled: pre-commit (gitleaks, gofmt, vet) and pre-push (the CI, see scripts/ci-local.sh)."
command -v gitleaks >/dev/null 2>&1 || echo "Note: gitleaks is not installed; commits will be refused until it is (https://github.com/gitleaks/gitleaks#installing)."
