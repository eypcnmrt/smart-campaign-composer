#!/bin/sh
# `make test`: the whole suite in Docker, no API key. Hook and script tests first (seconds), then the
# web checks (npm ci, audit, vue-tsc, vitest), then the Go checks against a throwaway PostgreSQL. The containers and the database are removed afterwards,
# whether the tests pass or not.
set -eu
cd "$(dirname "$0")/.."

compose="docker compose -f docker-compose.test.yml"
cleanup() { $compose down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

echo "== hook and script tests (node)"
$compose run --rm hooks

# The npm cache volume is shared with scripts/web.sh and lives outside the compose project; make sure it exists.
docker volume create scc-npm-cache >/dev/null

echo "== web (npm ci, npm audit, vue-tsc, vitest)"
$compose run --rm web

echo "== backend (gofmt, vet, race tests, PostgreSQL integration tests)"
$compose run --rm backend
