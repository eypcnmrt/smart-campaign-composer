# Workflow.
#
#   make test       the whole suite in Docker (gofmt, go vet, go test -race, PostgreSQL integration tests, hook
#                   and script tests). Needs only Docker and make, no API key, no Go on the host.
#   make e2e        the browser tests (Playwright, in the official image) against the real stack with a mock model, in a compose
#                   project of its own that is removed afterwards. Not part of `make test`; needs only Docker and make.
#                   SCREENSHOTS=1 also writes docs/screenshots, GREP=word runs the matching tests, KEEP=1 leaves the project up.
#   make soak       5,000 messages through the real worker and a chaos-configured mock provider; prints a report
#                   and fails on a double send, a broken 100/s cap or an early retry. Not part of `make test`.
#   make eval       the golden set (eval/golden.json) against the REAL model, scored by deterministic checks; costs money, so it
#                   needs LLM_MODE=anthropic and a key in the dotenv file: make eval LLM_MODE=anthropic (EVAL_MAX_USD=1.00 is the
#                   budget, EVAL_MODEL=... another model, EVAL_ARGS="-only id1,id2" some briefs). Results go to eval/results/.
#                   The model is the application's default, claude-sonnet-5-5 (EVAL_MODEL=claude-opus-5-5 for Opus).
#                   make eval EVAL_ARGS="-mock" is a free dry run. Not part of `make test` or of CI.
#
# Local, quick, without Docker (Go installed; the integration tests are not included):
#   make fmt        format Go code
#   make vet        go vet
#   make test-unit  go test ./...   (extra flags: make test-unit GOTESTFLAGS=-vet=off)
#   make fuzz       fuzz the segment compiler, the stream extractor and the demo mock's reading of a brief (FUZZTIME=30s each by default)
#
# -race needs a C compiler, so it runs only in the Docker-based `make test`.

GO ?= go
GOTESTFLAGS ?=
FUZZTIME ?= 30s

.PHONY: test e2e soak eval fmt vet test-unit fuzz

test:
	sh scripts/test.sh

e2e:
	sh scripts/e2e.sh

soak:
	sh scripts/soak.sh

eval:
	sh scripts/eval.sh

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
	$(GO) -C backend test $(GOTESTFLAGS) ./internal/llm -fuzz=^FuzzDemoSegment$$ -fuzztime=$(FUZZTIME)
