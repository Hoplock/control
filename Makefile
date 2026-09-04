# Hoplock Control — developer entry points.
#
# Every target here is also a CI step or a step CI depends on. If you add one,
# say what it is for: a target nobody can explain is a target nobody runs.

MODULE  := github.com/hoplock/control
BINARY  := hoplock-control
BIN_DIR := bin

# Version metadata, derived from git and stamped into the binary. A checkout
# with no tags still produces something true ("<sha>" or "<sha>-dirty"), and a
# tree with no git at all falls back to "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Config used by `make run`. Never committed; copy config.example.yaml.
CONFIG ?= config.yaml

GO             ?= go
GOLANGCI_LINT  ?= golangci-lint

.PHONY: all build test vet lint fmt license-check tidy clean run \
        contract-check contract-sync conform check help

all: build

## build: compile the server into bin/, stamped with the git version.
build:
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/hoplock-control

## test: run the unit tests with the race detector.
test:
	$(GO) test -race ./...

## vet: run go vet across the module.
vet:
	$(GO) vet ./...

## lint: run golangci-lint with the repository's linter set.
lint:
	$(GOLANGCI_LINT) run

## fmt: format every Go file in place.
fmt:
	gofmt -s -w $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './contract/*')

## license-check: verify the per-file SPDX header (docs/LICENSE-HEADER.md).
license-check:
	./scripts/license-check.sh

## tidy: reconcile go.mod/go.sum with the imports actually used.
tidy:
	$(GO) mod tidy

## clean: remove build output.
clean:
	rm -rf $(BIN_DIR)

## run: run the server against $(CONFIG) without installing it.
run:
	$(GO) run ./cmd/hoplock-control --config $(CONFIG)

## check: everything CI runs on a pull request, in CI's order.
check: build vet test lint license-check

# --- Placeholders ------------------------------------------------------------
# These targets exist so that the Definition-of-Done checklist in
# docs/PROTOCOL.md can name them before they do anything. A target that is
# missing and a target that fails read very differently in a checklist: the
# first looks like a typo, the second says "not yet".

## contract-check: verify the vendored contract is unmodified (phase 0002).
contract-check:
	@echo "contract-check: implemented in phase 0002" >&2
	@exit 1

## contract-sync: pull the contract from the Hoplock Proxy repository (phase 0002).
contract-sync:
	@echo "contract-sync: implemented in phase 0002" >&2
	@exit 1

## conform: run the black-box contract conformance suite (phase 0002).
conform:
	@echo "conform: implemented in phase 0002" >&2
	@exit 1

## help: list the targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
