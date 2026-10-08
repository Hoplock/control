# Hoplock Control — developer entry points.
#
# Every target here is also a CI step or a step CI depends on. If you add one,
# say what it is for: a target nobody can explain is a target nobody runs.

MODULE  := github.com/hoplock/control
BINARY  := hoplock-control
BIN_DIR := bin

# The version a build reports is the one Go stamps from the repository: the tag
# at a tagged commit, a pseudo-version after it, a +dirty suffix for a modified
# tree (PLAN M23). So `make build` and `go build` report the same version, and
# nothing here stamps one. VERSION=... on the command line overrides it, for a
# build outside a git checkout, where Go has nothing to stamp and the binary
# would otherwise call itself "dev".
VERSION =
LDFLAGS := $(if $(VERSION),-X main.version=$(VERSION))

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

# The API-diff tool `release-check` runs (PLAN M23). golang.org/x/exp has no
# tags, so the pin is a pseudo-version. It is pinned, and not free-floating, for
# golangci-lint's reason (the lint job says it in full): apidiff reads the
# compiler's export data through the x/tools it was built with, so a Go release
# whose export data that x/tools cannot read breaks the check. Bump it then, in
# the commit that moves the toolchain, and keep it a version that reports an
# added interface method as incompatible — `make release-check-guard` proves it.
APIDIFF_VERSION ?= v0.0.0-20261007192929-f45ad48fbe92

.PHONY: all build test vet lint exhaustive-guard fmt license-check tidy clean run migrate \
        contract-check contract-sync conform release-check release-check-guard check help

all: build

## build: compile the server into bin/. VERSION=... outside a git checkout.
build:
	$(GO) build $(if $(LDFLAGS),-ldflags '$(LDFLAGS)') -o $(BIN_DIR)/$(BINARY) ./cmd/hoplock-control

## test: run the unit tests with the race detector.
test:
	$(GO) test -race ./...

## vet: run go vet across the module.
vet:
	$(GO) vet ./...

## lint: run golangci-lint with the repository's linter set.
lint:
	$(GOLANGCI_LINT) run

## exhaustive-guard: prove the `exhaustive` linter rejects an unhandled enum member.
#
# M3's closed vocabulary rests on that linter and on nothing else (PLAN M13), and
# a linter that is enabled but silent is worse than none. This target is what
# checks the check. It needs golangci-lint, so it runs in the lint job.
exhaustive-guard:
	GOLANGCI_LINT=$(GOLANGCI_LINT) ./scripts/exhaustive-guard.sh

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
check: build vet test lint license-check release-check

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

# --- Releases (PLAN M23) ------------------------------------------------------

## release-check: say what merging this tree releases, and refuse a wrong number.
#
# Reads CHANGELOG.md, the v* tags and the tree; diffs the public packages
# against the latest release. Its last line is the one a reviewer reads. It
# never tags: only CI's release job does, on the merge commit.
release-check:
	APIDIFF_VERSION=$(APIDIFF_VERSION) ./scripts/release-check.sh

## release-check-guard: prove the release check refuses what it must.
#
# Runs the check against a throwaway repository, one case per refusal, in the
# style of exhaustive-guard: a check that is wired up but silent is worse than
# none. It needs the network once, to install the pinned apidiff.
release-check-guard:
	APIDIFF_VERSION=$(APIDIFF_VERSION) ./scripts/release-check-guard.sh

## help: list the targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
