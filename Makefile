# Agent Monitor (amo) — always run fresh so you never watch a stale build.
.PHONY: run build test vet

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X agentmon/internal/render.Version=$(VERSION)

run:            ## build+run from source (use this, not a stale ./amo)
	go run .

build:          ## produce ./amo (gitignored)
	go build -ldflags "$(LDFLAGS)" -o amo .

test:
	go test ./...

vet:
	go vet ./...
