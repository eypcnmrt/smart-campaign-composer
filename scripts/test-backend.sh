#!/bin/sh
# Runs inside the golang container started by docker-compose.test.yml (see `make test`).
# Fails on unformatted code, vet findings, and any failing test; the integration tests need
# TEST_DATABASE_URL and fail (they are not skipped) when the database is unreachable.
set -eu

cd /src/backend

echo "== gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
    echo "these files are not gofmt-formatted:"
    echo "$unformatted"
    exit 1
fi

echo "== go vet"
go vet -tags=integration ./...

echo "== go test -race (unit and integration)"
go test -race -tags=integration -count=1 -timeout 10m ./...
