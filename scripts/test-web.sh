#!/bin/sh
# The web checks of `make test`, run inside the pinned node container of docker-compose.test.yml (nothing is installed on
# the host). The repository is mounted read-only at /src; the app is copied to a writable place first, together with the
# backend test fixtures that the Go and the TypeScript tests share, at the same relative place as in the repository
# (web/src/domain/x.test.ts reaches ../../../backend/internal/content/testdata).
#
#   1. npm ci            exactly the packages of the lock file, no install scripts
#   2. vue-tsc           types of the app, its tests and vite.config.ts
#   3. vitest run        the tests
set -eu

work=/work
mkdir -p "$work/web" "$work/backend/internal/content" "$work/backend/internal/segment"
# node_modules of the checkout (an empty mount point left by scripts/web.sh) is not copied.
tar -C /src/web --exclude=./node_modules -cf - . | tar -C "$work/web" -xf -
cp -r /src/backend/internal/content/testdata "$work/backend/internal/content/testdata"
cp -r /src/backend/internal/segment/testdata "$work/backend/internal/segment/testdata"

cd "$work/web"
export npm_config_ignore_scripts=true npm_config_fund=false npm_config_update_notifier=false

echo "-- npm ci"
npm ci
echo "-- npm audit (high and critical must be absent)"
npm audit --audit-level=high
echo "-- vue-tsc"
npm run typecheck
echo "-- vitest"
npx vitest run
