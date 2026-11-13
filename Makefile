# Local Go workflow. The Docker-based `make test` (no API key needed) comes later.
#
#   make fmt        format Go code
#   make vet        go vet
#   make test-unit  go test ./...   (extra flags: make test-unit GOTESTFLAGS=-vet=off)
#   make fuzz       fuzz the segment compiler (FUZZTIME=30s each by default)
#
# -race needs a C compiler on Windows, so it runs in the Docker-based test target.

GO ?= go
GOTESTFLAGS ?=
FUZZTIME ?= 30s

.PHONY: fmt vet test-unit fuzz

fmt:
	gofmt -w backend

vet:
	$(GO) -C backend vet ./...

test-unit:
	$(GO) -C backend test $(GOTESTFLAGS) ./...

fuzz:
	$(GO) -C backend test $(GOTESTFLAGS) ./internal/segment -fuzz=^FuzzCompile$$ -fuzztime=$(FUZZTIME)
	$(GO) -C backend test $(GOTESTFLAGS) ./internal/segment -fuzz=^FuzzCompileValues$$ -fuzztime=$(FUZZTIME)
