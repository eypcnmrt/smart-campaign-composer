#!/bin/sh
# `make e2e`: the browser tests (Playwright) against the real stack with a mock model, in a compose project of its own
# (scc-e2e: its own network and database volume, nothing published on the host). Not part of `make test`: it builds the images
# and drives a browser, which takes minutes.
#
#   make e2e                  run the tests
#   make e2e SCREENSHOTS=1    also write the pictures of docs/screenshots
#   make e2e GREP="outage"    only the tests whose title matches
#   make e2e KEEP=1           leave the project up afterwards (the next run reuses it: faster while writing tests)
#
# The project (containers, network, volume) is removed when it ends, whether the tests pass or not.
set -eu
cd "$(dirname "$0")/.."

compose="docker compose -p scc-e2e -f docker-compose.yml -f docker-compose.e2e.yml"
cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		# What a failed run leaves behind must not be only the last line of the reporter: the logs of the stack go before it is removed.
		echo "== the last log lines of the stack (the run ended with status $status)"
		$compose logs --no-color --tail=60 api worker mock-provider web 2>&1 || true
	fi
	if [ "${KEEP:-0}" = "1" ]; then
		echo "== the scc-e2e project is left up (KEEP=1); remove it with: $compose down --volumes --remove-orphans"
	else
		$compose down --volumes --remove-orphans >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT INT TERM

started=$(date +%s)
mark() { echo "== $1 ($(($(date +%s) - started)) s)"; }

# A project that was left over from an earlier run must not leak its database into this one (unless it is being reused).
if [ "${KEEP:-0}" != "1" ]; then
	$compose down --volumes --remove-orphans >/dev/null 2>&1 || true
fi

mark "packages (npm ci, npm audit)"
sh scripts/e2e-npm.sh ci
sh scripts/e2e-npm.sh audit --audit-level=high

mark "stack (build, migrate, seed, start)"
$compose up -d --build --wait web worker

mark "browser tests"
status=0
E2E_SCREENSHOTS="${SCREENSHOTS:-0}" $compose run --rm e2e npx playwright test ${GREP:+--grep "$GREP"} || status=$?

mark "done, exit status $status"
exit "$status"
