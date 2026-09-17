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

# `make contract-sync` takes the ref to vendor, so a session can pin one.
REF ?= main

# `make migrate` passes these through. DRY_RUN=1 is shorthand for --dry-run.
MIGRATE_FLAGS ?= $(if $(DRY_RUN),--dry-run,)

# `make conform` inputs. BASE_URL is the server under test; EXPECT is the
# expectation file describing what that server is configured to serve
# (cmd/pdpconform/README.md). TOKEN is the proxy bearer token.
BASE_URL ?= http://127.0.0.1:8080
TOKEN    ?=
EXPECT   ?= cmd/pdpconform/testdata/mock-expectations.yaml
CONFORM_FLAGS ?=

GO             ?= go
GOLANGCI_LINT  ?= golangci-lint

.PHONY: all build test vet lint fmt license-check tidy clean run migrate \
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

## migrate: apply pending migrations to $(CONFIG)'s database. DRY_RUN=1 to preview.
#
# Never run on boot (PLAN §8): two nodes starting together must not race to
# build the schema, so applying migrations is a thing an operator does.
migrate:
	$(GO) run ./cmd/hoplock-control migrate --config $(CONFIG) $(MIGRATE_FLAGS)

## check: everything CI runs on a pull request, in CI's order.
check: build vet test lint license-check

# --- The vendored contract (M1) and the conformance suite -------------------

## contract-check: verify the vendored contract is unmodified (PLAN M1).
contract-check:
	./scripts/contract-check.sh

## contract-sync: pull the contract from the Hoplock Proxy repository. REF=<ref>
contract-sync:
	REF=$(REF) ./scripts/contract-sync.sh

## conform: run the black-box conformance suite. BASE_URL=, TOKEN=, EXPECT=
conform:
	$(GO) run ./cmd/pdpconform \
	    -base-url '$(BASE_URL)' \
	    -token '$(TOKEN)' \
	    -expectations '$(EXPECT)' \
	    $(CONFORM_FLAGS)

## help: list the targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
