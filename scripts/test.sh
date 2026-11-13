#!/bin/sh
# `make test`: the whole suite in Docker, no API key. Hook and script tests first (seconds), then the
# Go checks against a throwaway PostgreSQL. The containers and the database are removed afterwards,
# whether the tests pass or not.
set -eu
cd "$(dirname "$0")/.."

compose="docker compose -f docker-compose.test.yml"
cleanup() { $compose down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

echo "== hook and script tests (node)"
$compose run --rm hooks

echo "== backend (gofmt, vet, race tests, PostgreSQL integration tests)"
$compose run --rm backend
