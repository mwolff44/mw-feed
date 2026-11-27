#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Runs the checks of .github/workflows/ci.yml on this machine: the go, e2e and
# docker jobs, and prints the test coverage. Keep it in step with that file.
#
#   scripts/ci-local.sh
#   CI_LOCAL_SKIP=e2e,docker scripts/ci-local.sh   # leave steps out, on purpose
#
# CI_LOCAL_SKIP is a comma-separated list of: go, e2e, docker.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
skip=",${CI_LOCAL_SKIP:-},"

IFS=',' read -r -a requested <<< "${CI_LOCAL_SKIP:-}"
for name in "${requested[@]}"; do
  case "$name" in
    ""|go|e2e|docker) ;;
    *) echo "ci-local: unknown step \"$name\" in CI_LOCAL_SKIP (known: go, e2e, docker)" >&2; exit 2 ;;
  esac
done

skipped=()
step() { # step NAME DESCRIPTION
  if [[ "$skip" == *",$1,"* ]]; then skipped+=("$1"); return 1; fi
  printf '\n==> %s\n' "$2"
}
need() { command -v "$1" >/dev/null 2>&1 || { echo "ci-local: $1 is required for this step" >&2; exit 1; }; }

if step go "go job: format, lint, vet, tests with the race detector, build without CGO"; then
  need go; need golangci-lint
  cd "$root/src"
  unformatted="$(gofmt -l .)"
  if [ -n "$unformatted" ]; then echo "not gofmt-formatted:" >&2; echo "$unformatted" >&2; exit 1; fi
  golangci-lint run ./...
  go vet ./...
  go test -race -count=1 -coverpkg=./... -coverprofile=cover.out ./...
  "$root/scripts/coverage.sh" --profile "$PWD/cover.out"
  CGO_ENABLED=0 go build ./...
fi

if step e2e "e2e job: script logic and browser tests"; then
  need go; need node; need npm
  cd "$root/e2e"
  [ -d node_modules ] || npm ci
  npm run test:unit
  npm test
fi

if step docker "docker job: build the image, start it, check /healthz, healthcheck and licences"; then
  need docker; need curl
  cd "$root"
  name="mw-feed-ci-local-$$"
  trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
  docker build -t mw-feed:ci-local .
  docker run -d --name "$name" -p 127.0.0.1::8080 mw-feed:ci-local >/dev/null
  port="$(docker port "$name" 8080/tcp | head -n1 | sed 's/.*://')"
  healthy=no
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then healthy=yes; break; fi
    sleep 1
  done
  if [ "$healthy" != yes ]; then docker logs "$name" >&2; echo "ci-local: /healthz never answered" >&2; exit 1; fi
  docker exec "$name" /mw-feed healthcheck
  docker inspect --format '{{.Config.Healthcheck.Test}}' mw-feed:ci-local | grep healthcheck >/dev/null
  docker cp "$name":/licenses - | tar t | grep THIRD_PARTY_LICENSES.md >/dev/null
fi

echo
if [ "${#skipped[@]}" -gt 0 ]; then
  echo "ci-local: skipped on purpose: ${skipped[*]}"
fi
echo "ci-local: done"
