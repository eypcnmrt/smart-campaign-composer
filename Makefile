# Workflow.
#
#   make test       the whole suite in Docker (gofmt, go vet, go test -race, PostgreSQL integration tests, hook
#                   and script tests). Needs only Docker and make, no API key, no Go on the host.
#   make e2e        the browser tests (Playwright, in the official image) against the real stack with a mock model, in a compose
#                   project of its own that is removed afterwards. Not part of `make test`; needs only Docker and make.
#                   SCREENSHOTS=1 also writes docs/screenshots, GREP=word runs the matching tests, KEEP=1 leaves the project up.
#   make soak       5,000 messages through the real worker and a chaos-configured mock provider; prints a report
#                   and fails on a double send, a broken 100/s cap or an early retry. Not part of `make test`.
#
# Local, quick, without Docker (Go installed; the integration tests are not included):
#   make fmt        format Go code
#   make vet        go vet
#   make test-unit  go test ./...   (extra flags: make test-unit GOTESTFLAGS=-vet=off)
#   make fuzz       fuzz the segment compiler and the stream extractor (FUZZTIME=30s each by default)
#
# -race needs a C compiler, so it runs only in the Docker-based `make test`.

GO ?= go
GOTESTFLAGS ?=
FUZZTIME ?= 30s

.PHONY: test e2e soak fmt vet test-unit fuzz

test:
	sh scripts/test.sh

e2e:
	sh scripts/e2e.sh

soak:
	sh scripts/soak.sh

fmt:
	gofmt -w backend

vet:
	$(GO) -C backend vet ./...

test-unit:
	$(GO) -C backend test $(GOTESTFLAGS) ./...

fuzz:
	$(GO) -C backend test $(GOTESTFLAGS) ./internal/segment -fuzz=^FuzzCompile$$ -fuzztime=$(FUZZTIME)
	$(GO) -C backend test $(GOTESTFLAGS) ./internal/segment -fuzz=^FuzzCompileValues$$ -fuzztime=$(FUZZTIME)
	$(GO) -C backend test $(GOTESTFLAGS) ./internal/streamjson -fuzz=^FuzzExtractor$$ -fuzztime=$(FUZZTIME)
