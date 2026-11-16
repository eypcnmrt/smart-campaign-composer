#!/bin/sh
# Runs the web toolchain (npm, vite, vitest, vue-tsc) in a pinned node container, never on the host: third-party
# package code stays off this machine (and Windows Application Control cannot block it).
#
#   sh scripts/web.sh npm ci                       install from the lock file
#   sh scripts/web.sh npm install --save-exact X   add a dependency (package.json and the lock file are written to web/)
#   sh scripts/web.sh npx vue-tsc -p tsconfig.app.json --noEmit
#   sh scripts/web.sh npx vitest run
#   sh scripts/web.sh dev                          dev server on http://127.0.0.1:5173 (proxies /api to the host's :8080)
#                                                  (for Docker Desktop: on Linux host.docker.internal does not resolve, and the
#                                                  API is published on 127.0.0.1 only; run `npm run dev` another way there)
#
# Only web/ is mounted, plus the shared test fixtures of the backend (read-only, JSON only, at the same relative place:
# web/src/domain/x.test.ts reaches ../../../backend/internal/content/testdata, which is /backend/... in the container).
# node_modules and the npm cache live in named volumes, so no package is written to the checkout.
# Lifecycle scripts are off (npm_config_ignore_scripts); a package that needs one must be reviewed by a human first.
set -eu
cd "$(dirname "$0")/.."

IMAGE="node:24.14.0-alpine"
# Git Bash on Windows needs a drive path for -v and must not rewrite the container paths.
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"
src="$root/web"

set -- "$@"
if [ "$#" -eq 0 ]; then
	echo "usage: sh scripts/web.sh <command...> | dev" >&2
	exit 2
fi

if [ "$1" = "dev" ]; then
	exec docker run --rm \
		--security-opt=no-new-privileges \
		-v "$src:/app" -v scc-web-node-modules:/app/node_modules -v scc-npm-cache:/root/.npm -w /app \
		-e npm_config_ignore_scripts=true -e API_PROXY_TARGET=http://host.docker.internal:8080 -e WATCH_POLLING=1 \
		-p 127.0.0.1:5173:5173 \
		"$IMAGE" npm run dev -- --host 0.0.0.0
fi

exec docker run --rm \
	--security-opt=no-new-privileges \
	-v "$src:/app" -v scc-web-node-modules:/app/node_modules -v scc-npm-cache:/root/.npm -w /app \
	-v "$root/backend/internal/content/testdata:/backend/internal/content/testdata:ro" \
	-v "$root/backend/internal/segment/testdata:/backend/internal/segment/testdata:ro" \
	-e npm_config_ignore_scripts=true \
	"$IMAGE" "$@"
