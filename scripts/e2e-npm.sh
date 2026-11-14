#!/bin/sh
# Runs npm for e2e/ in the pinned node container, never on the host (the same rule as scripts/web.sh): third-party package code
# stays off this machine, install scripts are off, and node_modules lives in a named volume.
#
#   sh scripts/e2e-npm.sh install --save-exact --package-lock-only pkg@x.y.z     change the dependencies (writes package.json and the lock)
#   sh scripts/e2e-npm.sh ci                                                    install the locked packages into the volume
#   sh scripts/e2e-npm.sh audit --audit-level=high
#
# The volume (scc-e2e-node-modules) is what the e2e container of docker-compose.e2e.yml mounts, so the test run itself needs no
# network: the browser tests run in the official Playwright image of the same version as @playwright/test, with its browsers.
set -eu
cd "$(dirname "$0")/.."

IMAGE="node:24.14.0-alpine"
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"

docker volume create scc-e2e-node-modules >/dev/null
docker volume create scc-npm-cache >/dev/null

# The checkout is writable only for the files npm may change (package.json, the lock); node_modules is the volume.
exec docker run --rm \
	--security-opt=no-new-privileges \
	-v "$root/e2e:/app" -v scc-e2e-node-modules:/app/node_modules -v scc-npm-cache:/root/.npm -w /app \
	-e npm_config_ignore_scripts=true -e npm_config_fund=false -e npm_config_update_notifier=false \
	"$IMAGE" npm "$@"
