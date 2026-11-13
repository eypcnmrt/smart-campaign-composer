#!/bin/sh
# `make soak`: 5,000 messages through the real worker and a chaos-configured mock provider, in their
# own compose project (scc-soak) so the normal stack's data is untouched. The driver prints a report
# and exits non-zero if a message was delivered twice, the 100/s cap was exceeded, a retry came before
# its Retry-After, or a delivery did not reach a final state. Not part of `make test`.
set -eu
cd "$(dirname "$0")/.."

compose="docker compose -p scc-soak -f docker-compose.yml -f docker-compose.soak.yml"
cleanup() { $compose down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

echo "== starting database, migrations, chaos provider and worker"
$compose up -d --build worker

echo "== soak run"
$compose run --rm --build soak
